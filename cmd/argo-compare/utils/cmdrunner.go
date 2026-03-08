// Package utils provides utility implementations for file system operations,
// command execution, and Helm chart processing used by the argo-compare CLI.
package utils

import (
	"bytes"
	"context"
	"os/exec"
)

// RealCmdRunner executes shell commands using the operating system.
type RealCmdRunner struct{}

// Run executes cmd with args and captures stdout and stderr strings.
// The context can be used to cancel the command or set a timeout.
func (r *RealCmdRunner) Run(ctx context.Context, cmd string, args ...string) (string, string, error) {
	command := exec.CommandContext(ctx, cmd, args...) // #nosec G204 -- cmd is always a hardcoded binary name from internal callers

	var stdoutBuffer, stderrBuffer bytes.Buffer
	command.Stdout = &stdoutBuffer
	command.Stderr = &stderrBuffer

	err := command.Run()

	return stdoutBuffer.String(), stderrBuffer.String(), err
}
