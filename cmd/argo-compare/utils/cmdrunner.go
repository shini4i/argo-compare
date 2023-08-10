package utils

import (
	"bytes"
	"os/exec"
)

type RealCmdRunner struct{}

func (r *RealCmdRunner) Run(cmd string, args ...string) (string, string, error) {
	command := exec.Command(cmd, args...)

	var stdoutBuffer, stderrBuffer bytes.Buffer
	command.Stdout = &stdoutBuffer
	command.Stderr = &stderrBuffer

	err := command.Run()

	return stdoutBuffer.String(), stderrBuffer.String(), err
}
