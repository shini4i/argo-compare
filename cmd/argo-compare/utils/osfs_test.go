package utils

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRealOsFs_CreateTemp(t *testing.T) {
	r := RealOsFs{}
	dir := "" // Use the default temporary directory
	pattern := "testfile*"

	// Create a temporary file
	file, err := r.CreateTemp(dir, pattern)
	assert.NoError(t, err)
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			t.Fatal("Failed to close temp file.")
		}
	}(file)

	// Check that the file exists
	_, err = os.Stat(file.Name())
	assert.NoError(t, err)

	// Clean up
	err = r.Remove(file.Name())
	assert.NoError(t, err)
}

func TestRealOsFs_Remove(t *testing.T) {
	r := RealOsFs{}
	dir := "" // Use the default temporary directory
	pattern := "testfile*"

	// Create a temporary file
	file, err := r.CreateTemp(dir, pattern)
	assert.NoError(t, err)
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			t.Fatal("Failed to close temp file.")
		}
	}(file)

	// Remove the file
	err = r.Remove(file.Name())
	assert.NoError(t, err)

	// Check that the file no longer exists
	_, err = os.Stat(file.Name())
	assert.True(t, os.IsNotExist(err))
}
