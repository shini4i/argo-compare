package utils

import (
	"errors"
	"os"
)

type OsFileReader struct{}

func (r OsFileReader) ReadFile(file string) []byte {
	// #nosec G304
	if readFile, err := os.ReadFile(file); errors.Is(err, os.ErrNotExist) {
		return nil
	} else {
		return readFile
	}
}
