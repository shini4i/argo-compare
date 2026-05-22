//go:build unix

package utils

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOsFileReader_ReadFile_PermissionDenied covers the unreadable-file path of
// the FileReader contract. Restricted to unix builds because the test relies on
// POSIX file-mode semantics: chmod 0o000 producing EACCES on read. Windows does
// not implement that, and macOS/Linux behave identically here.
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
