// Package ports defines the interface contracts (ports) that external
// adapters must implement to integrate with the application core.
package ports

import (
	"context"
	"os"

	"github.com/shini4i/argo-compare/internal/models"
)

// CmdRunner executes shell commands and returns captured output.
// The context can be used for cancellation and timeout control.
type CmdRunner interface {
	Run(ctx context.Context, cmd string, args ...string) (stdout string, stderr string, err error)
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

// SensitiveDataMasker rewrites manifest content to remove or obscure sensitive information.
type SensitiveDataMasker interface {
	Mask(content []byte) ([]byte, bool, error)
}

// HelmChartsProcessor coordinates the Helm chart lifecycle required for comparisons.
// Methods that perform I/O operations accept a context for cancellation and timeout control.
type HelmChartsProcessor interface {
	GenerateValuesFile(chartName, tmpDir, targetType, values string, valuesObject map[string]interface{}) error
	DownloadHelmChart(ctx context.Context, cmdRunner CmdRunner, globber Globber, cacheDir, repoUrl, chartName, targetRevision string, repoCredentials []models.RepoCredentials) error
	ExtractHelmChart(ctx context.Context, cmdRunner CmdRunner, globber Globber, chartName, chartVersion, chartLocation, tmpDir, targetType string) error
	RenderAppSource(ctx context.Context, cmdRunner CmdRunner, releaseName, chartName, chartVersion, tmpDir, targetType, namespace string) error
}
