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

func TestOsFileReader_ReadFile_PermissionDenied(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission checks are ineffective when running as root")
	}

	tempFile, err := os.CreateTemp("", "testfile-noperm")
	require.NoError(t, err)
	defer func() {
		// Restore permissions before removal so the deferred Remove succeeds.
		_ = os.Chmod(tempFile.Name(), 0o600)
		require.NoError(t, os.Remove(tempFile.Name()))
	}()
	require.NoError(t, tempFile.Close())
	require.NoError(t, os.Chmod(tempFile.Name(), 0o000))

	reader := OsFileReader{}

	// Unreadable file: nil content and a non-nil error (previously swallowed).
	readContent, err := reader.ReadFile(tempFile.Name())
	require.Error(t, err)
	assert.Nil(t, readContent)
}
