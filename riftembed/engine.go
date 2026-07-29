package riftembed

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/achird-labs/rift-go/rift"
)

// Options configure an embedded engine.
type Options struct {
	// LibraryPath is an explicit path to the native library. Empty means discover it —
	// see resolveLibrary for the order.
	LibraryPath string

	// SkipVersionCheck disables the C-ABI preflight. Only useful when deliberately testing
	// against a pre-release library; a mismatch otherwise means undefined behaviour.
	SkipVersionCheck bool
}

// Engine is an in-process Rift engine: a Tokio runtime on its own threads, the engine it
// drives, and optionally an admin plane served over it.
//
// An Engine is safe for concurrent use. Every method pins its goroutine to an OS thread for the
// duration of the call so the engine's thread-local error slot is read on the thread that wrote
// it; a mutex additionally serialises access to the handle's lifetime so a Close racing a call
// cannot use a freed handle.
type Engine struct {
	sym *symbols
	lib uintptr

	mu     sync.RWMutex
	handle uintptr
	closed bool
}

// Start loads the native library and starts an engine.
//
// The returned Engine owns native resources; call Close to release them.
func Start(opts Options) (*Engine, error) {
	if !platformSupported() {
		return nil, fmt.Errorf("%w: %w", rift.ErrEngineUnavailable, errUnsupportedPlatform)
	}

	path, err := resolveLibrary(opts.LibraryPath)
	if err != nil {
		return nil, err
	}
	lib, err := dlopen(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", rift.ErrEngineUnavailable, err)
	}
	sym, err := bind(lib)
	if err != nil {
		_ = dlclose(lib)
		return nil, fmt.Errorf("%w: %s: %w", rift.ErrEngineUnavailable, path, err)
	}

	if !opts.SkipVersionCheck {
		if got := sym.abiVersion(); got != abiVersion {
			_ = dlclose(lib)
			return nil, fmt.Errorf("%w: library %s reports C-ABI v%d, this SDK requires v%d",
				rift.ErrVersionMismatch, path, got, abiVersion)
		}
	}

	runtime.LockOSThread()
	h := sym.start()
	runtime.UnlockOSThread()
	if h == 0 {
		_ = dlclose(lib)
		return nil, fmt.Errorf("%w: rift_start returned null (could not create the runtime)",
			rift.ErrEngineUnavailable)
	}

	return &Engine{sym: sym, lib: lib, handle: h}, nil
}

// BuildInfo returns the native library's build metadata as raw JSON. It reports the engine
// version and the capability lists SDKs feature-detect against — notably serveOptions, whose
// *absence* of a key means an engine too old to accept it.
func (e *Engine) BuildInfo() (json.RawMessage, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return nil, rift.ErrClosed
	}
	// rift_build_info returns a static string — read it, never free it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s := goString(e.sym.buildInfo())
	if s == "" {
		return nil, fmt.Errorf("%w: rift_build_info returned nothing", rift.ErrEngineUnavailable)
	}
	return json.RawMessage(s), nil
}

// ABIVersion reports the C-ABI version of the loaded library.
func (e *Engine) ABIVersion() uint32 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return 0
	}
	return e.sym.abiVersion()
}

// Close stops the engine and unloads the library. It is idempotent.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	h := e.handle
	e.handle = 0

	runtime.LockOSThread()
	e.sym.stop(h)
	runtime.UnlockOSThread()

	return dlclose(e.lib)
}

// --- call plumbing ---
//
// Every operation runs through one of these helpers, which together enforce the two invariants
// that are easy to get wrong once and then wrong everywhere: the handle is live for the
// duration of the call, and the (downcall, error-read) pair happens on one OS thread.

// withHandle runs fn with the handle held live and the goroutine pinned.
//
// The context is checked before the downcall and not after: an FFI call is synchronous and
// cannot be interrupted once it has started. Honouring cancellation at the boundary is the
// honest amount of support to offer, and it is enough for the case that matters — a cancelled
// test not queueing more work against an engine it is about to close.
func (e *Engine) withHandle(ctx context.Context, fn func(h uintptr) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", rift.ErrEngineUnavailable, err)
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return rift.ErrClosed
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return fn(e.handle)
}

// lastError builds an error from the engine's thread-local error slot. It must be called on the
// same locked OS thread as the failing downcall — which withHandle guarantees.
func (e *Engine) lastError(op string) error {
	msg := e.sym.takeString(e.sym.lastError())
	if msg == "" {
		msg = "engine reported no detail"
	}
	return newEngineError(op, msg)
}

// checkRC turns the engine's 0/-1 integer convention into an error.
func (e *Engine) checkRC(op string, rc int32) error {
	if rc == 0 {
		return nil
	}
	return e.lastError(op)
}

// takeJSON reads an owned `char*` result, treating null as failure.
func (e *Engine) takeJSON(op string, p unsafe.Pointer) (json.RawMessage, error) {
	if p == nil {
		return nil, e.lastError(op)
	}
	return json.RawMessage(e.sym.takeString(p)), nil
}

// newEngineError wraps an engine-reported message. The embedded lane carries no HTTP status, so
// it classifies by message. "not found" is the one distinction worth making, because callers
// legitimately branch on a missing imposter; everything else is a definition problem, since a
// call that never reached the engine fails earlier with ErrEngineUnavailable.
func newEngineError(op, msg string) error {
	kind := rift.ErrInvalidDefinition
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "not found") || strings.Contains(lower, "no imposter") {
		kind = rift.ErrImposterNotFound
	}
	return rift.NewEngineError(op, msg, 0, kind)
}

// platformSupported reports whether purego can load shared libraries on this platform.
func platformSupported() bool {
	switch runtime.GOOS {
	case "darwin", "linux", "freebsd", "windows":
		return true
	default:
		return false
	}
}

// Engine implements rift.Client, so a test suite — including the SDK conformance corpus — runs
// unchanged against an embedded engine or a remote one.
var _ rift.Client = (*Engine)(nil)
