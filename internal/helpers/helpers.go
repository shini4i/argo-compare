package helpers

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
)

func GetEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func ReadFile(file string) []byte {
	if readFile, err := os.ReadFile(file); errors.Is(err, os.ErrNotExist) {
		fmt.Printf("File [%s] was removed in a source branch, skipping...\n", file)
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

func StripHelmLabels(file string) {
	// list of labels to remove
	labels := []string{
		"app.kubernetes.io/managed-by",
		"helm.sh/chart",
		"chart",
		"app.kubernetes.io/version",
	}

	regex := fmt.Sprintf(`%s`, strings.Join(labels, "|"))

	// remove helm labels as they are not needed for comparison
	// it might be error-prone, as those labels are not always the same
	re := regexp.MustCompile("(?m)[\r\n]+^.*(" + regex + "):.*$")

	fileData, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}

	fileData = re.ReplaceAll(fileData, []byte(""))
	err = os.WriteFile(file, fileData, 0644)
	if err != nil {
		panic(err)
	}
}

func GetFileSha(file string) hash.Hash {
	// We are using SHA as a way to detect if two files are identical
	f, err := os.Open(file)
	if err != nil {
		log.Fatal(err.Error())
	}

	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			panic(err)
		}
	}(f)

	fileHash := sha256.New()
	if _, err := io.Copy(fileHash, f); err != nil {
		log.Fatal(err.Error())
	}

	return fileHash
}
