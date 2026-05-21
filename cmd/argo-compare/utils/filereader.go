package utils

import (
	"errors"
	"os"
)

// OsFileReader reads files from the local filesystem.
type OsFileReader struct{}

// ReadFile returns the content of the named file.
// It returns (nil, nil) when the file does not exist, allowing callers to
// distinguish "absent" from "unreadable". All other errors are returned as-is.
func (r OsFileReader) ReadFile(file string) ([]byte, error) {
	data, err := os.ReadFile(file) // #nosec G304
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}
