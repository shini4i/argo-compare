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

	stdout, stderr, err := runner.RunWithStdin(context.Background(), "piped-input", "cat")

	assert.NoError(t, err)
	assert.Equal(t, "piped-input", stdout)
	assert.Equal(t, "", stderr)
}

func TestRealCmdRunner_RunWithStdinFailure(t *testing.T) {
	runner := &RealCmdRunner{}

	stdout, stderr, err := runner.RunWithStdin(context.Background(), "x",
		"sh", "-c", "cat >/dev/null; echo boom >&2; exit 3")

	assert.Error(t, err)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "boom\n", stderr)
}
