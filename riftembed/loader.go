package riftembed

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/achird-labs/rift-go/rift"
)

// EnvLibPath names an explicit path to the native library, overriding discovery.
const EnvLibPath = "RIFT_FFI_LIB"

// EnvCacheDir overrides where rift-fetch stores downloaded libraries.
const EnvCacheDir = "RIFT_FFI_CACHE"

// LibraryPath reports where riftembed would load the native library from, without loading it.
//
// Useful for two things: diagnosing a discovery failure (the error lists every path tried), and
// letting a test suite skip cleanly when no library is installed rather than failing.
func LibraryPath() (string, error) { return resolveLibrary("") }

// platformLibNames returns the shared-library file names for the running platform.
func platformLibNames() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"rift_ffi.dll", "librift_ffi.dll"}
	case "darwin":
		return []string{"librift_ffi.dylib"}
	default:
		return []string{"librift_ffi.so"}
	}
}

// CacheDir is where downloaded native libraries are stored, keyed by version and platform.
// cmd/rift-fetch populates it; this package only reads.
func CacheDir() string {
	if dir := os.Getenv(EnvCacheDir); dir != "" {
		return dir
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "rift-go")
	}
	return filepath.Join(base, "rift-go", "natives")
}

// platformTag is the cache subdirectory for the running platform, e.g. "darwin-arm64".
func platformTag() string { return runtime.GOOS + "-" + runtime.GOARCH }

// resolveLibrary finds the native library, in order:
//
//  1. an explicit path from Options.LibraryPath
//  2. $RIFT_FFI_LIB
//  3. the on-disk cache populated by rift-fetch
//  4. the process working directory and its ./natives subdirectory (a vendored copy)
//
// Deliberately absent: an implicit network download. A test run that silently reaches out to
// the internet is a bad default; use cmd/rift-fetch to populate the cache explicitly, which is
// also what makes air-gapped and CI use predictable.
func resolveLibrary(explicit string) (string, error) {
	var tried []string

	check := func(p string) (string, bool) {
		if p == "" {
			return "", false
		}
		tried = append(tried, p)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, true
		}
		return "", false
	}

	if explicit != "" {
		if p, ok := check(explicit); ok {
			return p, nil
		}
		return "", fmt.Errorf("%w: library path %s does not exist", rift.ErrEngineUnavailable, explicit)
	}

	if p, ok := check(os.Getenv(EnvLibPath)); ok {
		return p, nil
	}

	names := platformLibNames()
	dirs := []string{
		filepath.Join(CacheDir(), platformTag()),
		".",
		"natives",
		filepath.Join("natives", platformTag()),
	}
	for _, dir := range dirs {
		for _, name := range names {
			if p, ok := check(filepath.Join(dir, name)); ok {
				return p, nil
			}
		}
	}

	return "", fmt.Errorf(
		"%w: no Rift native library found (looked for %s)\n"+
			"  fix: set %s=/path/to/%s, or run:\n"+
			"    go run github.com/achird-labs/rift-go/cmd/rift-fetch@latest\n"+
			"  searched:\n    %s",
		rift.ErrEngineUnavailable,
		strings.Join(names, ", "),
		EnvLibPath, names[0],
		strings.Join(tried, "\n    "),
	)
}

// errUnsupportedPlatform is returned when purego cannot load libraries here.
var errUnsupportedPlatform = errors.New(
	"riftembed: in-process embedding is not supported on this platform; " +
		"use rift.Connect or rift.Spawn instead")
