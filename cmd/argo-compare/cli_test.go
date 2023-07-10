package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDropCacheBeforeApply(t *testing.T) {
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
}
