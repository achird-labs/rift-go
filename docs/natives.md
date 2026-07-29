# Native library & CI

The [embedded transport](embedded.md) needs the Rift shared library. `Connect` and `Spawn` do not.

## Fetching it

```sh
go run github.com/achird-labs/rift-go/cmd/rift-fetch@latest -version v0.16.0
```

That downloads the right artifact for your platform, verifies it, and installs it where
`riftembed` looks. Print the destination without downloading:

```sh
rift-fetch -print-cache-path
# ~/Library/Caches/rift-go/natives/darwin-arm64/librift_ffi.dylib
```

## Why fetching is explicit

`riftembed` never downloads anything on its own. A test run that quietly reaches out to the network
is a bad default: it makes CI non-hermetic, breaks on an air-gapped host, and turns a missing
dependency into a slow mysterious hang instead of an error that tells you what to do.

The loader's error already says what to run.

## Verification is not optional

Every download is checked against the SHA-256 the release manifest publishes. **There is no flag to
skip it** — an unverified shared library is one you are about to `dlopen` into your own process.

The download streams to a temporary file in the destination directory and is renamed only after
the checksum holds, so:

- a concurrent loader can never observe a partially-written library
- a failed verification installs nothing and leaves no `.part` litter
- an existing file is **re-verified rather than trusted**, which makes a truncated download from an
  interrupted run self-healing instead of a baffling crash later

## Platforms

Release assets are named by C-style architecture, which is not Go's spelling. `riftfetch` bridges
the two, but the mapping is worth knowing when reading a release page:

| Go | Release classifier |
|---|---|
| `linux/amd64` | `linux-x86_64` |
| `linux/arm64` | `linux-aarch64` |
| `darwin/amd64` | `darwin-x86_64` |
| `darwin/arm64` | `darwin-aarch64` |
| `windows/amd64` | `windows-x86_64` |

### Alpine and musl

Go cannot distinguish musl from glibc at runtime, so it has to be declared:

```sh
rift-fetch -version v0.16.0 -platform linux-x86_64-musl
```

Guessing would be worse than asking: the wrong library loads and then misbehaves.

## Air-gapped hosts and mirrors

```sh
RIFT_RELEASE_BASE=https://mirror.internal/rift rift-fetch -version v0.16.0
```

The manifest is read from `<base>/<version>/ffi-manifest.json` and each artifact URL has its base
rewritten to match, so mirroring the release assets is enough — the manifest does not need editing.

`RIFT_MANIFEST_URL` overrides manifest resolution entirely.

| Variable | Effect |
|---|---|
| `RIFT_FFI_LIB` | explicit library path; overrides discovery |
| `RIFT_FFI_CACHE` | where the library is installed and looked for |
| `RIFT_RELEASE_BASE` | release download base, for a mirror |
| `RIFT_MANIFEST_URL` | explicit `ffi-manifest.json` URL |
| `RIFT_BINARY` | explicit `rift` binary, for `Spawn` |

## Programmatic use

```go
res, err := riftfetch.Fetch(ctx, riftfetch.Options{
	Version: "v0.16.0",
	Log:     func(f string, a ...any) { log.Printf(f, a...) },
})
// res.Path, res.Cached
```

## In CI

The library and the corpus are version-locked to the engine release, so pin one version and take
all three assets from it:

```yaml
- name: fetch the engine assets
  env:
    GH_TOKEN: ${{ github.token }}
    RIFT_VERSION: v0.16.0
  run: |
    set -euo pipefail
    mkdir -p natives dist
    case "${{ runner.os }}-${{ runner.arch }}" in
      Linux-X64)   lib=librift_ffi-linux-x86_64.so;      triple=x86_64-unknown-linux-gnu ;;
      macOS-ARM64) lib=librift_ffi-darwin-aarch64.dylib; triple=aarch64-apple-darwin ;;
    esac
    gh release download "$RIFT_VERSION" --repo achird-labs/rift --dir dist \
      --pattern "$lib" \
      --pattern "rift-$RIFT_VERSION-$triple.tar.gz" \
      --pattern "sdk-conformance-$RIFT_VERSION.tar.gz"
    mv "dist/$lib" natives/
    tar -xzf "dist/rift-$RIFT_VERSION-$triple.tar.gz" -C dist
    tar -xzf "dist/sdk-conformance-$RIFT_VERSION.tar.gz" -C dist
    echo "RIFT_FFI_LIB=$PWD/natives/$lib" >> "$GITHUB_ENV"
    echo "RIFT_BINARY=$PWD/$(find dist -type f -name rift -perm -u+x | head -1)" >> "$GITHUB_ENV"
```

Locate the binary and the manifest inside the extracted archives rather than assuming a layout —
neither archive's internal structure is contractual.

!!! tip "Keep the suite green without the library"
    Skip rather than fail when no engine is available, so `go test ./...` works on a fresh
    checkout while staying required in CI:

    ```go
    if _, err := riftembed.LibraryPath(); err != nil {
        t.Skipf("no native library: %v", err)
    }
    ```
