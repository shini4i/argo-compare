package utils

import (
	"bytes"
	"os/exec"
)

// RealCmdRunner executes shell commands using the operating system.
type RealCmdRunner struct{}

// Run executes cmd with args and captures stdout and stderr strings.
func (r *RealCmdRunner) Run(cmd string, args ...string) (string, string, error) {
	command := exec.Command(cmd, args...)

	var stdoutBuffer, stderrBuffer bytes.Buffer
	command.Stdout = &stdoutBuffer
	command.Stderr = &stderrBuffer

	err := command.Run()

	return stdoutBuffer.String(), stderrBuffer.String(), err
}
