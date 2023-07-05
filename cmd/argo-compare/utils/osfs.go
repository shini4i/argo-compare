package utils

import "os"

type RealOsFs struct{}

func (r *RealOsFs) CreateTemp(dir, pattern string) (f *os.File, err error) {
	return os.CreateTemp(dir, pattern)
}

func (r *RealOsFs) Remove(name string) error {
	return os.Remove(name)
}
