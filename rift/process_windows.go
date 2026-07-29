//go:build windows

package rift

import (
	"os"
	"os/exec"
)

// configureProcessGroup is a no-op on Windows: there is no process-group semantics to opt into
// here, and CommandContext already arranges for the child to be killed with the context.
func configureProcessGroup(cmd *exec.Cmd) {}

// terminate stops the child. Windows has no SIGTERM, and console-control events only reach
// processes attached to the same console, which a test binary's child generally is not — so a
// direct Kill is the honest option rather than a graceful-shutdown pretence.
func terminate(p *os.Process) error {
	return p.Kill()
}
