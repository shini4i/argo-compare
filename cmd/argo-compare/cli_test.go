package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDropCacheBeforeApply(t *testing.T) {
	t.Run("cacheDir successfully removed", func(t *testing.T) {
		tempDir := t.TempDir()

		cacheDir = tempDir
		d := DropCache(true)

		err := d.BeforeApply(nil)
		assert.NoError(t, err)

		_, err = os.Stat(tempDir)
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("cacheDir removal fails", func(t *testing.T) {
		tmpDir := t.TempDir()

		tmpFile, err := os.Create(filepath.Join(tmpDir, "readonly"))
		if err != nil {
			t.Fatalf("failed to create temporary file: %v", err)
		}
		assert.NoError(t, tmpFile.Close())

		assert.NoError(t, os.Chmod(tmpDir, 0400))
		defer func() {
			assert.NoError(t, os.Chmod(tmpDir, 0700))
		}()

		cacheDir = tmpDir
		d := DropCache(true)

		err = d.BeforeApply(nil)
		assert.Error(t, err)
	})

	t.Run("drop cache disabled", func(t *testing.T) {
		cacheDir = "/tmp/unused"
		d := DropCache(false)

		err := d.BeforeApply(nil)
		assert.NoError(t, err)
	})
}
