package utils

import "os"

type CmdRunner interface {
	Run(cmd string, args ...string) (stdout string, stderr string, err error)
}

type OsFs interface {
	CreateTemp(dir, pattern string) (f *os.File, err error)
	Remove(name string) error
}

type FileReader interface {
	ReadFile(file string) []byte
}

type Globber interface {
	Glob(pattern string) ([]string, error)
}

type HelmValuesGenerator interface {
	GenerateValuesFile(chartName, tmpDir, targetType, values string) error
}
