package riftfetch_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/achird-labs/rift-go/riftfetch"
)

// fakeRelease serves a manifest and one artifact, so the whole fetch path is exercised without
// touching the network.
type fakeRelease struct {
	*httptest.Server
	payload  []byte
	sum      string
	platform string
	requests int
}

func newFakeRelease(t *testing.T, payload []byte, corruptSHA bool) *fakeRelease {
	t.Helper()

	sum := sha256.Sum256(payload)
	fr := &fakeRelease{payload: payload, sum: hex.EncodeToString(sum[:]), platform: "test-platform"}

	declared := fr.sum
	if corruptSHA {
		declared = strings.Repeat("0", 64)
	}

	mux := http.NewServeMux()
	fr.Server = httptest.NewServer(mux)
	t.Cleanup(fr.Close)

	mux.HandleFunc("/v9.9.9/ffi-manifest.json", func(w http.ResponseWriter, _ *http.Request) {
		m := riftfetch.Manifest{
			Version: "v9.9.9",
			ABI:     "v2",
			Artifacts: []riftfetch.Artifact{{
				Platform: fr.platform,
				File:     "librift_ffi-test.so",
				SHA256:   declared,
				URL:      fr.URL + "/v9.9.9/librift_ffi-test.so",
			}},
		}
		_ = json.NewEncoder(w).Encode(m)
	})
	mux.HandleFunc("/v9.9.9/librift_ffi-test.so", func(w http.ResponseWriter, _ *http.Request) {
		fr.requests++
		_, _ = w.Write(fr.payload)
	})
	return fr
}

// pointAtFakeRelease makes the fetcher resolve everything through the test server.
func pointAtFakeRelease(t *testing.T, fr *fakeRelease) {
	t.Helper()
	t.Setenv(riftfetch.EnvManifestURL, fr.URL+"/v9.9.9/ffi-manifest.json")
	t.Setenv(riftfetch.EnvReleaseBase, fr.URL)
}

func TestFetchDownloadsAndVerifies(t *testing.T) {
	fr := newFakeRelease(t, []byte("pretend this is a shared library"), false)
	pointAtFakeRelease(t, fr)
	dir := t.TempDir()

	res, err := riftfetch.Fetch(context.Background(), riftfetch.Options{
		Version:  "v9.9.9",
		Platform: fr.platform,
		Dir:      filepath.Join(dir, "librift_ffi.so"),
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Cached {
		t.Error("first fetch reported a cache hit")
	}

	got, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read installed file: %v", err)
	}
	if string(got) != string(fr.payload) {
		t.Errorf("installed content = %q", got)
	}
}

// A mismatched checksum must leave nothing behind: the whole point is that an unverified library
// is never made loadable.
func TestChecksumMismatchInstallsNothing(t *testing.T) {
	fr := newFakeRelease(t, []byte("payload"), true /* declare a wrong sha */)
	pointAtFakeRelease(t, fr)
	dest := filepath.Join(t.TempDir(), "librift_ffi.so")

	_, err := riftfetch.Fetch(context.Background(), riftfetch.Options{
		Version: "v9.9.9", Platform: fr.platform, Dir: dest,
	})
	if err == nil {
		t.Fatal("want an error for a mismatched checksum")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want a checksum mismatch", err)
	}
	if _, statErr := os.Stat(dest); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a file was installed despite the checksum failing: %v", statErr)
	}
	// The temp file must be cleaned up too, not left as .part litter.
	entries, _ := os.ReadDir(filepath.Dir(dest))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Errorf("left a partial file behind: %s", e.Name())
		}
	}
}

func TestSecondFetchIsCachedAndDoesNotRedownload(t *testing.T) {
	fr := newFakeRelease(t, []byte("stable content"), false)
	pointAtFakeRelease(t, fr)
	dest := filepath.Join(t.TempDir(), "librift_ffi.so")
	opts := riftfetch.Options{Version: "v9.9.9", Platform: fr.platform, Dir: dest}

	if _, err := riftfetch.Fetch(context.Background(), opts); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	if fr.requests != 1 {
		t.Fatalf("first fetch made %d artifact requests, want 1", fr.requests)
	}

	res, err := riftfetch.Fetch(context.Background(), opts)
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if !res.Cached {
		t.Error("second fetch did not report a cache hit")
	}
	if fr.requests != 1 {
		t.Errorf("second fetch re-downloaded (%d requests)", fr.requests)
	}
}

// A file that exists but does not verify — a truncated download from an interrupted run — must be
// replaced, not trusted.
func TestCorruptExistingFileIsReplaced(t *testing.T) {
	fr := newFakeRelease(t, []byte("the real library"), false)
	pointAtFakeRelease(t, fr)
	dest := filepath.Join(t.TempDir(), "librift_ffi.so")

	if err := os.WriteFile(dest, []byte("truncated garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := riftfetch.Fetch(context.Background(), riftfetch.Options{
		Version: "v9.9.9", Platform: fr.platform, Dir: dest,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Cached {
		t.Error("a corrupt existing file was treated as a cache hit")
	}
	got, _ := os.ReadFile(res.Path)
	if string(got) != "the real library" {
		t.Errorf("content = %q, want the downloaded library", got)
	}
}

func TestForceRedownloads(t *testing.T) {
	fr := newFakeRelease(t, []byte("content"), false)
	pointAtFakeRelease(t, fr)
	dest := filepath.Join(t.TempDir(), "librift_ffi.so")
	opts := riftfetch.Options{Version: "v9.9.9", Platform: fr.platform, Dir: dest}

	if _, err := riftfetch.Fetch(context.Background(), opts); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	opts.Force = true
	if _, err := riftfetch.Fetch(context.Background(), opts); err != nil {
		t.Fatalf("forced Fetch: %v", err)
	}
	if fr.requests != 2 {
		t.Errorf("artifact requests = %d, want 2 with -force", fr.requests)
	}
}

func TestUnknownPlatformIsActionable(t *testing.T) {
	fr := newFakeRelease(t, []byte("x"), false)
	pointAtFakeRelease(t, fr)

	_, err := riftfetch.Fetch(context.Background(), riftfetch.Options{
		Version: "v9.9.9", Platform: "plan9-vax", Dir: filepath.Join(t.TempDir(), "lib.so"),
	})
	if !errors.Is(err, riftfetch.ErrNoArtifact) {
		t.Fatalf("err = %v, want ErrNoArtifact", err)
	}
	// The message must name what *is* available, or the user is left guessing.
	if !strings.Contains(err.Error(), fr.platform) {
		t.Errorf("error does not list available platforms: %v", err)
	}
}

func TestVersionIsRequired(t *testing.T) {
	_, err := riftfetch.Fetch(context.Background(), riftfetch.Options{})
	if err == nil || !strings.Contains(err.Error(), "Version is required") {
		t.Errorf("err = %v, want a required-version error", err)
	}
}

func TestMissingReleaseIsActionable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	t.Setenv(riftfetch.EnvManifestURL, srv.URL+"/nope/ffi-manifest.json")

	_, err := riftfetch.Fetch(context.Background(), riftfetch.Options{
		Version: "v0.0.0-nope", Platform: "linux-x86_64",
	})
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "published release") {
		t.Errorf("error should hint at an unpublished release, got: %v", err)
	}
}

// Platform must map Go's vocabulary onto the release classifiers, or every fetch on a normal
// host looks for an asset that does not exist.
func TestPlatformMapsGoArchToReleaseClassifier(t *testing.T) {
	p, err := riftfetch.Platform()
	if err != nil {
		t.Skipf("no published cdylib for this host: %v", err)
	}
	for _, bad := range []string{"amd64", "arm64"} {
		if strings.Contains(p, bad) {
			t.Errorf("Platform() = %q — it still carries Go's %q instead of the release spelling", p, bad)
		}
	}
	if !strings.Contains(p, "x86_64") && !strings.Contains(p, "aarch64") {
		t.Errorf("Platform() = %q, want a release-style architecture", p)
	}
	fmt.Println("host platform classifier:", p)
}
