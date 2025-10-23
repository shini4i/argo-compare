package utils

import (
	"errors"
	"os"
)

// OsFileReader reads files from the local filesystem.
type OsFileReader struct{}

// ReadFile returns the content of file or nil when the file does not exist.
func (r OsFileReader) ReadFile(file string) []byte {
	if readFile, err := os.ReadFile(file); errors.Is(err, os.ErrNotExist) /* #nosec G304 */ {
		return nil
	} else {
		return readFile
	}
}
