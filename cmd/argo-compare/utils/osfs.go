package utils

import "os"

// RealOsFs wraps basic temporary file helpers from the os package.
type RealOsFs struct{}

// CreateTemp mirrors os.CreateTemp to allow abstraction in tests.
func (r *RealOsFs) CreateTemp(dir, pattern string) (f *os.File, err error) {
	return os.CreateTemp(dir, pattern)
}

// Remove deletes the named file or directory.
func (r *RealOsFs) Remove(name string) error {
	return os.Remove(name)
}
