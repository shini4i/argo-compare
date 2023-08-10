package utils

import (
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func TestOsFileReader_ReadFile(t *testing.T) {
	// Temporary file content
	content := "Hello, World!"

	// Create a temporary file and write some content to it
	tempFile, err := os.CreateTemp("", "testfile")
	if err != nil {
		t.Fatal("Failed to create temp file.")
	}
	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			t.Fatal("Failed to remove temp file.")
		}
	}(tempFile.Name()) // Clean up after test

	_, err = tempFile.Write([]byte(content))
	if err != nil {
		t.Fatal("Failed to write to temp file.")
	}

	if err := tempFile.Close(); err != nil {
		t.Fatal("Failed to close temp file.")
	}

	// Test reading an existing file
	reader := OsFileReader{}
	readContent := reader.ReadFile(tempFile.Name())
	assert.Equal(t, content, string(readContent))

	// Test reading a non-existing file
	nonExistingFileName := "non_existing_file.txt"
	readContent = reader.ReadFile(nonExistingFileName)
	assert.Nil(t, readContent)
}
