package utils

import (
	"errors"
	"os"
)

type OsFileReader struct{}

func (r OsFileReader) ReadFile(file string) []byte {
	if readFile, err := os.ReadFile(file); errors.Is(err, os.ErrNotExist) {
		return nil
	} else {
		return readFile
	} // #nosec G304
}
