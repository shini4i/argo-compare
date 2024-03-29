package utils

import (
	"os"

	"github.com/shini4i/argo-compare/internal/models"
)

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

type HelmChartsProcessor interface {
	GenerateValuesFile(chartName, tmpDir, targetType, values string, valuesObject map[string]interface{}) error
	DownloadHelmChart(cmdRunner CmdRunner, globber Globber, cacheDir, repoUrl, chartName, targetRevision string, repoCredentials []models.RepoCredentials) error
	ExtractHelmChart(cmdRunner CmdRunner, globber Globber, chartName, chartVersion, chartLocation, tmpDir, targetType string) error
	RenderAppSource(cmdRunner CmdRunner, releaseName, chartName, chartVersion, tmpDir, targetType string) error
}
