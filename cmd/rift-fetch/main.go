// Command rift-fetch downloads and verifies the Rift native library, installing it where
// riftembed looks for it.
//
//	go run github.com/achird-labs/rift-go/cmd/rift-fetch@latest -version v0.1.0
//
// This exists as a separate step rather than as an implicit download inside riftembed: a test
// run that quietly reaches out to the network is a bad default, and being explicit is what makes
// CI and air-gapped hosts predictable.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/achird-labs/rift-go/riftembed"
	"github.com/achird-labs/rift-go/riftfetch"
)

func main() {
	var (
		version  = flag.String("version", "", "engine release tag to fetch, e.g. v0.1.0 (required)")
		dir      = flag.String("dir", "", "install directory (default: the riftembed cache)")
		platform = flag.String("platform", "", "release classifier override, e.g. linux-x86_64-musl on Alpine")
		force    = flag.Bool("force", false, "re-download even if a verified copy is present")
		quiet    = flag.Bool("quiet", false, "print only the installed path")
		showPath = flag.Bool("print-cache-path", false, "print where riftembed expects the library, then exit")
	)
	flag.Usage = usage
	flag.Parse()

	if *showPath {
		fmt.Println(riftembed.CachePath())
		return
	}
	if *version == "" {
		fmt.Fprint(os.Stderr, "rift-fetch: -version is required\n\n")
		usage()
		os.Exit(2)
	}

	// Ctrl-C must not leave a half-written library behind; the download writes to a temp file
	// and only renames after verifying, so cancellation is always safe.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
	if *quiet {
		logf = nil
	}

	res, err := riftfetch.Fetch(ctx, riftfetch.Options{
		Version:  *version,
		Platform: *platform,
		Dir:      *dir,
		Force:    *force,
		Log:      logf,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "rift-fetch: %v\n", err)
		if errors.Is(err, riftfetch.ErrNoArtifact) {
			fmt.Fprintf(os.Stderr,
				"\n  on Alpine or another musl host, pass -platform linux-x86_64-musl\n")
		}
		os.Exit(1)
	}

	// The path goes to stdout so it can be captured; progress went to stderr.
	fmt.Println(res.Path)
	if !*quiet && res.Cached {
		fmt.Fprintf(os.Stderr, "(already present; use -force to re-download)\n")
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `rift-fetch — download and verify the Rift native library

Usage:
  rift-fetch -version <tag> [flags]

Flags:
`)
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
Environment:
  %s   explicit ffi-manifest.json URL, overriding all URL construction
  %s   release download base, for a mirror or internal proxy
  %s     where the library is installed (also read by riftembed)

Examples:
  rift-fetch -version v0.1.0
  rift-fetch -version v0.1.0 -platform linux-x86_64-musl     # Alpine
  %s=https://mirror.internal/rift rift-fetch -version v0.1.0

Every download is verified against the SHA-256 the release manifest publishes. There is no flag
to skip that: an unverified shared library is one you are about to load into your own process.
`, riftfetch.EnvManifestURL, riftfetch.EnvReleaseBase, riftembed.EnvCacheDir, riftfetch.EnvReleaseBase)
}
