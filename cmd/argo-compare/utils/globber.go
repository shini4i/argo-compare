package utils

import "github.com/mattn/go-zglob"

type CustomGlobber struct{}

func (g CustomGlobber) Glob(pattern string) ([]string, error) {
	return zglob.Glob(pattern)
}
