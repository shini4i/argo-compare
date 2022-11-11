package helpers

import (
	"fmt"
	"os"
)

const (
	ColorRed   = "\033[0;31m"
	ColorCyan  = "\033[0;36m"
	ColorReset = "\033[0m"
)

func ReadFile(file string) []byte {
	readFile, err := os.ReadFile(file)
	if err != nil {
		fmt.Println(err)
	}

	return readFile
}

func Contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
