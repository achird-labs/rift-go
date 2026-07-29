package rift

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// EnvBinary names an explicit path to the rift binary, overriding discovery.
const EnvBinary = "RIFT_BINARY"

// SpawnOptions configure a managed engine process.
type SpawnOptions struct {
	// Binary is an explicit path to the rift executable. Empty means discover it:
	// $RIFT_BINARY, then "rift" on PATH.
	Binary string

	// AdminPort pins the admin port. Zero picks a free one, which is what makes parallel test
	// binaries safe.
	AdminPort uint16

	// Args are extra arguments appended to the command line, e.g. "--allowInjection".
	Args []string

	// Dir is the child's working directory. This matters for configs with relative data paths —
	// the conformance corpus, for instance, requires cwd to be the corpus root.
	Dir string

	// Env replaces the child's environment when non-nil; otherwise it inherits.
	Env []string

	// Stdout and Stderr receive the child's output. Nil discards it.
	Stdout, Stderr *os.File

	// StartTimeout bounds the wait for the admin API to answer. Zero means 20s.
	StartTimeout time.Duration

	// APIKey is passed to the engine and used on admin calls.
	APIKey string

	// RemoteOptions customise the admin client built for the spawned engine. Its APIKey and
	// Host are set from this struct and ignored here.
	RemoteOptions RemoteOptions
}

// process is a running child engine.
//
// A single goroutine owns cmd.Wait for the process's whole life. That is what makes liveness
// checks portable — probing with signal 0 is a Unix-ism — and it means Wait is called exactly
// once, which is the only way it is safe to call at all.
type process struct {
	cmd      *exec.Cmd
	waitDone chan struct{}
	waitErr  error // written before waitDone closes; read only after

	stopOnce sync.Once
	stopErr  error
}

func newProcess(cmd *exec.Cmd) *process {
	p := &process{cmd: cmd, waitDone: make(chan struct{})}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.waitDone)
	}()
	return p
}

// Spawn starts a rift process and returns a Remote wired to it.
//
// ctx bounds *startup only*. The engine's lifetime is owned by the returned Remote: it runs
// until Close, regardless of what happens to ctx.
//
// That distinction is deliberate. exec.CommandContext would tie the child to ctx, so passing the
// obvious `ctx, cancel := context.WithTimeout(...); defer cancel()` would kill the engine the
// moment the calling function returned — the engine would be dead before the first request, and
// the failure would look like a refused connection rather than a lifetime bug.
//
// The engine is started on a free admin port unless one is pinned, so parallel test binaries do
// not collide, and Spawn does not return until the admin API answers — a client that raced
// startup would fail intermittently in exactly the way that is hardest to debug.
func Spawn(ctx context.Context, opts SpawnOptions) (*Remote, error) {
	bin, err := findBinary(opts.Binary)
	if err != nil {
		return nil, err
	}

	port := opts.AdminPort
	if port == 0 {
		if port, err = freePort(); err != nil {
			return nil, err
		}
	}

	args := []string{"--port", strconv.Itoa(int(port))}
	if opts.APIKey != "" {
		args = append(args, "--apiKey", opts.APIKey)
	}
	args = append(args, opts.Args...)

	// Deliberately exec.Command, not exec.CommandContext — see the lifetime note above.
	cmd := exec.Command(bin, args...) //nolint:gosec // path is caller-supplied by design
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	configureProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: start %s: %w", ErrEngineUnavailable, bin, err)
	}
	proc := newProcess(cmd)

	timeout := opts.StartTimeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}

	adminURL := "http://127.0.0.1:" + strconv.Itoa(int(port))
	ro := opts.RemoteOptions
	ro.APIKey = opts.APIKey
	ro.Host = "127.0.0.1"
	remote, err := Connect(adminURL, ro)
	if err != nil {
		_ = proc.stop()
		return nil, err
	}

	if err := waitReady(ctx, remote, proc, timeout); err != nil {
		_ = proc.stop()
		return nil, err
	}
	remote.ownedProc = proc
	return remote, nil
}

// waitReady polls the admin API until it answers, the child dies, or the deadline passes.
// Noticing a dead child matters: without it a crash-on-startup becomes a timeout, which reads
// as "slow machine" and sends people looking in the wrong place.
func waitReady(ctx context.Context, r *Remote, p *process, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	backoff := 10 * time.Millisecond

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: waiting for engine: %w", ErrEngineUnavailable, err)
		}
		if exited, state := p.exited(); exited {
			return fmt.Errorf("%w: engine exited during startup (%s)", ErrEngineUnavailable, state)
		}

		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := r.Ping(pingCtx)
		cancel()
		if err == nil {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("%w: engine did not become ready within %s (last error: %v)",
				ErrEngineUnavailable, timeout, err)
		}
		time.Sleep(backoff)
		if backoff < 250*time.Millisecond {
			backoff *= 2
		}
	}
}

// exited reports whether the child has already terminated, and how.
func (p *process) exited() (bool, string) {
	select {
	case <-p.waitDone:
		if p.cmd.ProcessState != nil {
			return true, p.cmd.ProcessState.String()
		}
		return true, "exited"
	default:
		return false, ""
	}
}

// stop terminates the child, asking politely before insisting. It is idempotent.
func (p *process) stop() error {
	p.stopOnce.Do(func() {
		if p.cmd.Process == nil {
			return
		}
		select {
		case <-p.waitDone: // already gone
			p.stopErr = ignoreExpectedExit(p.waitErr)
			return
		default:
		}

		// SIGTERM lets the engine close listeners and flush; a Kill after the grace period
		// guarantees a test binary never hangs on a wedged child.
		_ = terminate(p.cmd.Process)
		select {
		case <-p.waitDone:
		case <-time.After(5 * time.Second):
			_ = p.cmd.Process.Kill()
			<-p.waitDone
		}
		p.stopErr = ignoreExpectedExit(p.waitErr)
	})
	return p.stopErr
}

// ignoreExpectedExit drops the errors a deliberate shutdown always produces, so Close returns
// nil on the happy path instead of "signal: terminated".
func ignoreExpectedExit(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

// findBinary resolves the rift executable: explicit path, then $RIFT_BINARY, then PATH.
func findBinary(explicit string) (string, error) {
	candidates := []string{explicit, os.Getenv(EnvBinary)}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		abs, err := filepath.Abs(c)
		if err != nil {
			return "", wrapInvalid("resolve rift binary path", err)
		}
		if st, err := os.Stat(abs); err == nil && !st.IsDir() {
			return abs, nil
		}
		return "", fmt.Errorf("%w: rift binary %q does not exist", ErrEngineUnavailable, c)
	}

	// The released binary is "rift"; a cargo build of the engine produces "rift-http-proxy".
	// Accepting both means a contributor running against a local build needs no extra setup.
	names := []string{"rift", "rift-http-proxy"}
	if runtime.GOOS == "windows" {
		names = []string{"rift.exe", "rift-http-proxy.exe"}
	}
	for _, name := range names {
		if found, err := exec.LookPath(name); err == nil {
			return found, nil
		}
	}
	return "", fmt.Errorf(
		"%w: no rift binary found on PATH (looked for %s)\n"+
			"  fix: install rift, or set %s=/path/to/rift",
		ErrEngineUnavailable, strings.Join(names, ", "), EnvBinary)
}

// freePort asks the OS for an unused TCP port. There is an unavoidable race between closing the
// listener and the child binding it; in practice the window is microseconds and the alternative
// (a fixed port) collides far more often under parallel tests.
func freePort() (uint16, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("%w: reserve an admin port: %w", ErrEngineUnavailable, err)
	}
	defer func() { _ = l.Close() }()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("%w: unexpected listener address type %T", ErrEngineUnavailable, l.Addr())
	}
	return uint16(addr.Port), nil //nolint:gosec // an OS-assigned port always fits in uint16
}
