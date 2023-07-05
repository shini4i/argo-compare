package utils

import "os"

type CmdRunner interface {
	Run(cmd string, args ...string) (stdout string, stderr string, err error)
}

type OsFs interface {
	CreateTemp(dir, pattern string) (f *os.File, err error)
	Remove(name string) error
}
