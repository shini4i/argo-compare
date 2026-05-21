package utils

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRealCmdRunner_Run(t *testing.T) {
	runner := &RealCmdRunner{}
	cmd := "echo"
	args := []string{"hello"}

	stdout, stderr, err := runner.Run(context.Background(), cmd, args...)

	assert.NoError(t, err)
	assert.Equal(t, "hello\n", stdout)
	assert.Equal(t, "", stderr)
}

func TestRealCmdRunner_RunWithStdin(t *testing.T) {
	runner := &RealCmdRunner{}
	stdin := "secret-payload"

	stdout, stderr, err := runner.RunWithStdin(context.Background(), stdin, "cat")

	assert.NoError(t, err)
	assert.Equal(t, stdin, stdout)
	assert.Equal(t, "", stderr)
}
