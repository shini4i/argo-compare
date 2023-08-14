package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDropCacheBeforeApply(t *testing.T) {
	t.Run("cacheDir successfully removed", func(t *testing.T) {
		tempDir, err := os.MkdirTemp(testsDir, "test-")
		if err != nil {
			t.Fatal(err)
		}
		defer func(path string) {
			err := os.RemoveAll(path)
			if err != nil {
				t.Fatal(err)
			}
		}(tempDir) // clean up in case of failure

		cacheDir = tempDir

		d := DropCache(true)
		err = d.BeforeApply(nil)
		assert.Nil(t, err)

		// check if cacheDir does not exist anymore
		_, err = os.Stat(cacheDir)
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("cacheDir removal fails", func(t *testing.T) {
		// Test case 2: cacheDir removal fails
		tmpDir, err := os.MkdirTemp("", "test-failure-")
		if err != nil {
			t.Fatal(err)
		}
		defer func(path string) {
			if err := os.Chmod(tmpDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
		}(tmpDir)

		// Create a read-only file inside the directory
		tmpFile, err := os.Create(filepath.Join(tmpDir, "readonlyfile.txt"))
		if err != nil {
			t.Fatalf("Failed to create temporary file: %v", err)
		}

		if err := tmpFile.Close(); err != nil {
			t.Fatalf("Failed to close temporary file: %v", err)
		}

		// Make the directory unwritable
		err = os.Chmod(tmpDir, 0400)
		if err != nil {
			t.Fatalf("Failed to change permissions: %v", err)
		}

		cacheDir = tmpDir

		d := DropCache(true)

		err = d.BeforeApply(nil)
		assert.Error(t, err)
	})
}
