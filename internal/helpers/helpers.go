package helpers

import (
	"fmt"
	"os"
)

func ReadFile(file string) []byte {
	readFile, err := os.ReadFile(file)
	if err != nil {
		fmt.Println(err)
	}

	return readFile
}

func GetEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func Contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
