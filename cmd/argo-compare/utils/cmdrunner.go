// Package utils provides utility implementations for file system operations,
// command execution, and Helm chart processing used by the argo-compare CLI.
package utils

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

// RealCmdRunner executes shell commands using the operating system.
type RealCmdRunner struct{}

// Run executes cmd with args and captures stdout and stderr strings.
// The context can be used to cancel the command or set a timeout.
func (r *RealCmdRunner) Run(ctx context.Context, cmd string, args ...string) (string, string, error) {
	command := exec.CommandContext(ctx, cmd, args...) // #nosec G204 -- callers validate cmd via validateExecutable before invoking Run

	var stdoutBuffer, stderrBuffer bytes.Buffer
	command.Stdout = &stdoutBuffer
	command.Stderr = &stderrBuffer

	err := command.Run()

	return stdoutBuffer.String(), stderrBuffer.String(), err
}

// RunWithStdin executes cmd with args and stdin, capturing stdout and stderr strings.
// The context can be used to cancel the command or set a timeout.
// This method should be used when passing sensitive data (like credentials) to avoid exposing them in process listings.
func (r *RealCmdRunner) RunWithStdin(ctx context.Context, stdin string, cmd string, args ...string) (string, string, error) {
	command := exec.CommandContext(ctx, cmd, args...) // #nosec G204 -- callers validate cmd via validateExecutable before invoking RunWithStdin
	command.Stdin = strings.NewReader(stdin)

	var stdoutBuffer, stderrBuffer bytes.Buffer
	command.Stdout = &stdoutBuffer
	command.Stderr = &stderrBuffer

	err := command.Run()

	return stdoutBuffer.String(), stderrBuffer.String(), err
}
