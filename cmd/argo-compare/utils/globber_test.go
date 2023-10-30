package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCustomGlobber_Glob(t *testing.T) {
	// Create a pattern that matches some files in the filesystem
	pattern := "*.go" // Matches all Go source files in the current directory

	// Use the CustomGlobber to find matching files
	globber := CustomGlobber{}
	matches, err := globber.Glob(pattern)

	// Expect no error
	assert.NoError(t, err)

	// Expect at least one match (since there should be at least one Go source file)
	assert.True(t, len(matches) > 0)
}
