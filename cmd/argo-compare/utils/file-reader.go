package utils

import (
	"errors"
	"fmt"
	"os"
)

type OsFileReader struct{}

func (r OsFileReader) ReadFile(file string) []byte {
	if readFile, err := os.ReadFile(file); errors.Is(err, os.ErrNotExist) {
		fmt.Printf("File [%s] was removed in a source branch, skipping...\n", file)
		return nil
	} else {
		return readFile
	}
}
