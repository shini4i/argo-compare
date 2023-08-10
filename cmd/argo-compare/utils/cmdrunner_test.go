package utils

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestRealCmdRunner_Run(t *testing.T) {
	runner := &RealCmdRunner{}
	cmd := "echo"
	args := []string{"hello"}

	stdout, stderr, err := runner.Run(cmd, args...)

	assert.NoError(t, err)
	assert.Equal(t, "hello\n", stdout)
	assert.Equal(t, "", stderr)
}
