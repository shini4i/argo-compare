package helpers

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
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

func StripHelmLabels(file string) {
	re := regexp.MustCompile("(?m)[\r\n]+^.*(helm.sh/chart|chart):.*$")

	fileData, err := ioutil.ReadFile(file)
	if err != nil {
		fmt.Println(err)
	}

	fileData = re.ReplaceAll(fileData, []byte(""))
	err = ioutil.WriteFile(file, fileData, 0644)
	if err != nil {
		fmt.Println(err)
	}
}
