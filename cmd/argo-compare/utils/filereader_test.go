package utils

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOsFileReader_ReadFile(t *testing.T) {
	content := "Hello, World!"

	tempFile, err := os.CreateTemp("", "testfile")
	require.NoError(t, err, "Failed to create temp file")
	defer func() {
		require.NoError(t, os.Remove(tempFile.Name()), "Failed to remove temp file")
	}()

	_, err = tempFile.Write([]byte(content))
	require.NoError(t, err, "Failed to write to temp file")
	require.NoError(t, tempFile.Close(), "Failed to close temp file")

	reader := OsFileReader{}

	// Existing file: content returned, no error.
	readContent, err := reader.ReadFile(tempFile.Name())
	require.NoError(t, err)
	assert.Equal(t, content, string(readContent))

	// Non-existing file: nil content, no error (treated as absent).
	readContent, err = reader.ReadFile("non_existing_file.txt")
	require.NoError(t, err)
	assert.Nil(t, readContent)
}
