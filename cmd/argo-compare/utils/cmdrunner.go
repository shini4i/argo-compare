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
	command := exec.CommandContext(ctx, cmd, args...) // #nosec G204 -- cmd is helm or an operator-configured binary; callers validate PR-supplied args

	var stdoutBuffer, stderrBuffer bytes.Buffer
	command.Stdout = &stdoutBuffer
	command.Stderr = &stderrBuffer

	err := command.Run()

	return stdoutBuffer.String(), stderrBuffer.String(), err
}

// RunWithStdin executes cmd with args, feeding stdin to the command, and
// captures stdout and stderr strings. It exists so secrets (e.g. registry
// passwords) can be piped to commands instead of being passed as argv
// elements, which are world-readable via /proc/<pid>/cmdline.
// The context can be used to cancel the command or set a timeout.
func (r *RealCmdRunner) RunWithStdin(ctx context.Context, stdin, cmd string, args ...string) (string, string, error) {
	command := exec.CommandContext(ctx, cmd, args...) // #nosec G204 -- cmd is helm or an operator-configured binary; callers validate PR-supplied args

	command.Stdin = strings.NewReader(stdin)

	var stdoutBuffer, stderrBuffer bytes.Buffer
	command.Stdout = &stdoutBuffer
	command.Stderr = &stderrBuffer

	err := command.Run()

	return stdoutBuffer.String(), stderrBuffer.String(), err
}
