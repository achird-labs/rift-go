//go:build !windows

package rift

import (
	"os"
	"os/exec"
	"syscall"
)

// configureProcessGroup puts the child in its own process group so a stray Ctrl-C in an
// interactive run does not kill the test binary's engine out from under it, and so terminate
// can signal the whole group.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate asks the child's process group to shut down cleanly.
func terminate(p *os.Process) error {
	// Negative pid targets the group. If the group signal fails — the child may have exited
	// already, or never got its own group — fall back to the process itself.
	if err := syscall.Kill(-p.Pid, syscall.SIGTERM); err == nil {
		return nil
	}
	return p.Signal(syscall.SIGTERM)
}
