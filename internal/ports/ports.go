package ports

import (
	"os"

	"github.com/shini4i/argo-compare/internal/models"
)

// CmdRunner executes shell commands and returns captured output.
type CmdRunner interface {
	Run(cmd string, args ...string) (stdout string, stderr string, err error)
}

// OsFs abstracts temporary file creation and removal.
type OsFs interface {
	CreateTemp(dir, pattern string) (f *os.File, err error)
	Remove(name string) error
}

// FileReader exposes read access to file contents.
type FileReader interface {
	ReadFile(file string) []byte
}

// Globber expands filesystem patterns into matching paths.
type Globber interface {
	Glob(pattern string) ([]string, error)
}

// HelmChartsProcessor coordinates the Helm chart lifecycle required for comparisons.
type HelmChartsProcessor interface {
	GenerateValuesFile(chartName, tmpDir, targetType, values string, valuesObject map[string]interface{}) error
	DownloadHelmChart(cmdRunner CmdRunner, globber Globber, cacheDir, repoUrl, chartName, targetRevision string, repoCredentials []models.RepoCredentials) error
	ExtractHelmChart(cmdRunner CmdRunner, globber Globber, chartName, chartVersion, chartLocation, tmpDir, targetType string) error
	RenderAppSource(cmdRunner CmdRunner, releaseName, chartName, chartVersion, tmpDir, targetType, namespace string) error
}
