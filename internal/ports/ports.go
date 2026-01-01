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

// HelmDeps bundles the external dependencies required by Helm operations.
type HelmDeps struct {
	CmdRunner CmdRunner
	Globber   Globber
}

// ChartDownloadRequest contains the parameters for downloading a Helm chart.
type ChartDownloadRequest struct {
	CacheDir        string
	RepoURL         string
	ChartName       string
	TargetRevision  string
	RepoCredentials []models.RepoCredentials
}

// ChartExtractRequest contains the parameters for extracting a Helm chart.
type ChartExtractRequest struct {
	ChartName     string
	ChartVersion  string
	ChartLocation string
	TmpDir        string
	TargetType    string
}

// ChartRenderRequest contains the parameters for rendering a Helm chart.
type ChartRenderRequest struct {
	ReleaseName  string
	ChartName    string
	ChartVersion string
	TmpDir       string
	TargetType   string
	Namespace    string
}

// HelmChartsProcessor coordinates the Helm chart lifecycle required for comparisons.
// Methods that perform I/O operations accept a context for cancellation and timeout control.
type HelmChartsProcessor interface {
	GenerateValuesFile(chartName, tmpDir, targetType, values string, valuesObject map[string]interface{}) error
	DownloadHelmChart(ctx context.Context, deps HelmDeps, req ChartDownloadRequest) error
	ExtractHelmChart(ctx context.Context, deps HelmDeps, req ChartExtractRequest) error
	RenderAppSource(ctx context.Context, cmdRunner CmdRunner, req ChartRenderRequest) error
}
