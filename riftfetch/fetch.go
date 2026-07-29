// Package riftfetch downloads and verifies the Rift native library.
//
// riftembed deliberately never downloads anything: a test run that quietly reaches out to the
// network is a bad default, and it makes CI and air-gapped hosts unpredictable. Fetching is
// therefore explicit — a separate step, run by a human or a CI job, through this package or the
// cmd/rift-fetch binary.
//
// Every download is verified against the SHA-256 the release manifest publishes. An unverified
// shared library is a shared library you are about to dlopen into your own process, so the
// checksum is not optional and there is no flag to skip it.
package riftfetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/achird-labs/rift-go/riftembed"
)

const (
	// EnvManifestURL points at an explicit ffi-manifest.json, overriding all URL construction.
	EnvManifestURL = "RIFT_MANIFEST_URL"
	// EnvReleaseBase overrides the release download base, for a mirror or an internal proxy.
	// The manifest is read from <base>/<version>/ffi-manifest.json and each artifact's URL has
	// its own base rewritten to match.
	EnvReleaseBase = "RIFT_RELEASE_BASE"
)

// DefaultReleaseBase is where releases live when nothing overrides it.
const DefaultReleaseBase = "https://github.com/achird-labs/rift/releases/download"

// ErrNoArtifact means the manifest carries no library for the requested platform.
var ErrNoArtifact = errors.New("riftfetch: no artifact for this platform")

// Manifest is the release's platform → asset map (ffi-manifest.json).
type Manifest struct {
	Version   string     `json:"version"`
	ABI       string     `json:"abi"`
	Artifacts []Artifact `json:"artifacts"`
}

// Artifact is one platform's cdylib.
type Artifact struct {
	// Platform is the release classifier, e.g. "linux-x86_64", "darwin-aarch64".
	Platform string `json:"platform"`
	File     string `json:"file"`
	SHA256   string `json:"sha256"`
	URL      string `json:"url"`
}

// Find returns the artifact for a platform classifier.
func (m Manifest) Find(platform string) (Artifact, error) {
	for _, a := range m.Artifacts {
		if a.Platform == platform {
			return a, nil
		}
	}
	available := make([]string, 0, len(m.Artifacts))
	for _, a := range m.Artifacts {
		available = append(available, a.Platform)
	}
	return Artifact{}, fmt.Errorf("%w: %q (manifest %s has: %s)",
		ErrNoArtifact, platform, m.Version, strings.Join(available, ", "))
}

// Platform returns the release classifier for the running host.
//
// The release names assets by C-style architecture (x86_64, aarch64) while Go names them amd64
// and arm64, so the two vocabularies have to be bridged somewhere; here is that somewhere.
func Platform() (string, error) {
	var osName string
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		osName = runtime.GOOS
	default:
		return "", fmt.Errorf("%w: GOOS %q has no published cdylib", ErrNoArtifact, runtime.GOOS)
	}

	var arch string
	switch runtime.GOARCH {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	default:
		return "", fmt.Errorf("%w: GOARCH %q has no published cdylib", ErrNoArtifact, runtime.GOARCH)
	}
	return osName + "-" + arch, nil
}

// Options configure a fetch.
type Options struct {
	// Version is the engine release tag, e.g. "v0.1.0". Required: there is no "latest" default,
	// because a test suite that silently changes engine version between runs is worse than one
	// that fails to start.
	Version string

	// Platform overrides the detected release classifier. Set it to "linux-x86_64-musl" on
	// Alpine — Go cannot distinguish musl from glibc at runtime, so that one must be declared.
	Platform string

	// Dir overrides the install directory. Empty installs where riftembed looks.
	Dir string

	// Force re-downloads even when a verified copy is already present.
	Force bool

	// HTTPClient overrides the client used. Nil uses a 5-minute-timeout client, which is
	// generous for a ~20 MB library on a slow link.
	HTTPClient *http.Client

	// Log receives progress lines. Nil discards them.
	Log func(format string, args ...any)
}

func (o *Options) logf(format string, args ...any) {
	if o.Log != nil {
		o.Log(format, args...)
	}
}

func (o *Options) client() *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// Result describes what a fetch produced.
type Result struct {
	Path     string
	Artifact Artifact
	Version  string
	// Cached is true when a verified copy was already present and nothing was downloaded.
	Cached bool
}

// Fetch downloads the native library for the host platform and installs it where riftembed
// looks, returning the installed path.
func Fetch(ctx context.Context, opts Options) (*Result, error) {
	if opts.Version == "" {
		return nil, errors.New("riftfetch: Version is required (e.g. \"v0.1.0\")")
	}

	platform := opts.Platform
	if platform == "" {
		var err error
		if platform, err = Platform(); err != nil {
			return nil, err
		}
	}

	dest := opts.Dir
	if dest == "" {
		dest = riftembed.CachePath()
	} else if fi, err := os.Stat(dest); err == nil && fi.IsDir() {
		dest = filepath.Join(dest, filepath.Base(riftembed.CachePath()))
	}

	manifest, err := FetchManifest(ctx, opts)
	if err != nil {
		return nil, err
	}
	art, err := manifest.Find(platform)
	if err != nil {
		return nil, err
	}

	// An existing file is only trusted after it verifies. A truncated or half-written library
	// left by an interrupted run would otherwise be dlopen'd on the next test run.
	if !opts.Force {
		if sum, err := sha256File(dest); err == nil && strings.EqualFold(sum, art.SHA256) {
			opts.logf("already present and verified: %s", dest)
			return &Result{Path: dest, Artifact: art, Version: manifest.Version, Cached: true}, nil
		}
	}

	url := rewriteBase(art.URL, manifest.Version)
	opts.logf("downloading %s (%s)", art.File, url)
	if err := download(ctx, opts, url, dest, art.SHA256); err != nil {
		return nil, err
	}
	opts.logf("installed %s", dest)
	return &Result{Path: dest, Artifact: art, Version: manifest.Version}, nil
}

// FetchManifest downloads and parses ffi-manifest.json.
func FetchManifest(ctx context.Context, opts Options) (*Manifest, error) {
	url := manifestURL(opts.Version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("riftfetch: build manifest request: %w", err)
	}
	resp, err := opts.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("riftfetch: fetch manifest %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("riftfetch: fetch manifest %s: HTTP %d (is %q a published release?)",
			url, resp.StatusCode, opts.Version)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("riftfetch: read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("riftfetch: parse manifest %s: %w", url, err)
	}
	if len(m.Artifacts) == 0 {
		return nil, fmt.Errorf("riftfetch: manifest %s lists no artifacts", url)
	}
	return &m, nil
}

func manifestURL(version string) string {
	if u := os.Getenv(EnvManifestURL); u != "" {
		return u
	}
	return releaseBase() + "/" + version + "/ffi-manifest.json"
}

func releaseBase() string {
	if b := os.Getenv(EnvReleaseBase); b != "" {
		return strings.TrimRight(b, "/")
	}
	return DefaultReleaseBase
}

// rewriteBase repoints an artifact URL at a configured mirror. The manifest is generated with
// GitHub URLs baked in, so an air-gapped host mirroring the assets needs the base swapped rather
// than the manifest edited.
func rewriteBase(artifactURL, version string) string {
	base := os.Getenv(EnvReleaseBase)
	if base == "" {
		return artifactURL
	}
	return strings.TrimRight(base, "/") + "/" + version + "/" + filepath.Base(artifactURL)
}

// download streams url to dest, verifying the checksum before anything lands at the final path.
func download(ctx context.Context, opts Options, url, dest, wantSHA string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("riftfetch: create %s: %w", filepath.Dir(dest), err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("riftfetch: build request: %w", err)
	}
	resp, err := opts.client().Do(req)
	if err != nil {
		return fmt.Errorf("riftfetch: download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("riftfetch: download %s: HTTP %d", url, resp.StatusCode)
	}

	// Write to a temporary file in the destination directory so the rename below is atomic —
	// a concurrent loader must never observe a partially-written library.
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".rift-ffi-*.part")
	if err != nil {
		return fmt.Errorf("riftfetch: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName) // no-op once renamed
	}()

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), resp.Body); err != nil {
		return fmt.Errorf("riftfetch: write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("riftfetch: close %s: %w", tmpName, err)
	}

	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, wantSHA) {
		return fmt.Errorf("riftfetch: checksum mismatch for %s\n  want %s\n  got  %s\n"+
			"  the download was corrupted or the asset does not match the manifest; not installing",
			url, wantSHA, got)
	}

	// Executable bits: a shared library does not strictly need them everywhere, but some
	// loaders and container runtimes care, and it costs nothing to be conventional.
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("riftfetch: chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("riftfetch: install to %s: %w", dest, err)
	}
	return nil
}

// sha256File hashes an existing file.
func sha256File(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path is computed, not user input
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
