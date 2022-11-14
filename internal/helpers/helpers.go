package helpers

import (
	"errors"
	"fmt"
	"os"
)

const (
	ColorRed   = "\033[0;31m"
	ColorCyan  = "\033[0;36m"
	ColorReset = "\033[0m"
)

func ReadFile(file string) []byte {
	if readFile, err := os.ReadFile(file); errors.Is(err, os.ErrNotExist) {
		fmt.Printf("File [%s%s%s] was removed in a source branch, skipping...\n",
			ColorRed, file, ColorReset)
		return nil
	} else {
		return readFile
	}
}

func Contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
