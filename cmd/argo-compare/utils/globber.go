package utils

import "github.com/mattn/go-zglob"

// CustomGlobber resolves glob patterns using mattn/go-zglob.
type CustomGlobber struct{}

// Glob expands pattern and returns the matching file paths.
func (g CustomGlobber) Glob(pattern string) ([]string, error) {
	return zglob.Glob(pattern)
}
