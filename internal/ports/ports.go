// Package ports defines the interface contracts (ports) that external
// adapters must implement to integrate with the application core.
package ports

import (
	"context"
	"os"

	"github.com/shini4i/argo-compare/internal/anchor"
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
// ReadFile returns (nil, nil) when the file does not exist and (nil, err) for
// any other I/O or permission failure, allowing callers to distinguish
// "absent" from "unreadable".
//
// Note: an absent file and a genuinely empty file are observably the same to
// callers — both surface as (nil, nil) followed by an empty []byte from
// successful reads. If a caller needs to distinguish them, it must os.Stat
// the path itself; this contract does not.
type FileReader interface {
	ReadFile(file string) ([]byte, error)
}

// Globber expands filesystem patterns into matching paths.
type Globber interface {
	Glob(pattern string) ([]string, error)
}

// SensitiveDataMasker rewrites manifest content to remove or obscure sensitive information.
type SensitiveDataMasker interface {
	Mask(content []byte) ([]byte, bool, error)
}

// RegistryCredentials holds authentication details for a Helm registry.
type RegistryCredentials struct {
	Username string
	Password string // #nosec G101 -- credential field for registry auth, populated from provider
}

// CredentialProvider resolves authentication credentials for a registry URL.
//
// Usage protocol: callers must invoke Matches first. GetCredentials must only be
// called when Matches returns true. Matches is expected to be a cheap, local check
// (e.g. regex or string comparison). GetCredentials may perform network calls (e.g.
// token exchange) and should respect the context for cancellation and timeouts.
type CredentialProvider interface {
	// Matches reports whether this provider can supply credentials for the given registry URL.
	Matches(registryURL string) bool
	// GetCredentials returns credentials for the given registry URL.
	// It must only be called after Matches returns true.
	// Implementations may perform network calls (e.g. token exchange) and should respect the context.
	GetCredentials(ctx context.Context, registryURL string) (RegistryCredentials, error)
}

// HelmDeps bundles the external dependencies required by Helm operations.
type HelmDeps struct {
	CmdRunner           CmdRunner
	Globber             Globber
	CredentialProviders []CredentialProvider
}

// ChartDownloadRequest contains the parameters for downloading a Helm chart.
type ChartDownloadRequest struct {
	CacheDir       string
	RepoURL        string
	ChartName      string
	TargetRevision string
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
// ValueFiles lists paths (relative to the chart directory) supplied via
// Application.spec.source.helm.valueFiles. They are applied in order, before
// inline values from Application.spec.source.helm.values / valuesObject.
type ChartRenderRequest struct {
	ReleaseName  string
	ChartName    string
	ChartVersion string
	TmpDir       string
	TargetType   string
	Namespace    string
	ValueFiles   []string
}

// HelmChartsProcessor coordinates the Helm chart lifecycle required for comparisons.
// Methods that perform I/O operations accept a context for cancellation and timeout control.
type HelmChartsProcessor interface {
	GenerateValuesFile(chartName, tmpDir, targetType, values string, valuesObject map[string]interface{}) error
	DownloadHelmChart(ctx context.Context, deps HelmDeps, req ChartDownloadRequest) error
	ExtractHelmChart(ctx context.Context, deps HelmDeps, req ChartExtractRequest) error
	RenderAppSource(ctx context.Context, cmdRunner CmdRunner, req ChartRenderRequest) error
}

// ValidationError represents a single validation error for a Kubernetes manifest.
type ValidationError struct {
	// Filename is the path to the manifest file that failed validation.
	Filename string
	// Kind is the Kubernetes resource kind (e.g. "Deployment", "Service").
	Kind string
	// Name is the metadata.name of the resource.
	Name string
	// Message describes the validation failure.
	Message string
}

// ValidationResult captures the outcome of validating a directory of manifests.
type ValidationResult struct {
	// Target identifies which side of the comparison was validated (e.g. "src" or "dst").
	Target string
	// Valid reports whether all manifests passed validation.
	Valid bool
	// ResourceCount is the total number of resources validated.
	ResourceCount int
	// ErrorCount is the number of resources that failed validation.
	ErrorCount int
	// Errors contains structured details for each validation failure.
	Errors []ValidationError
	// InvocationError is non-empty when the validator itself failed to run (e.g. binary not found).
	// When set, Valid is false and ResourceCount/ErrorCount are zero.
	InvocationError string
}

// ManifestValidator validates rendered Kubernetes manifests against schemas.
// Implementations typically wrap an external validator such as kubeconform.
type ManifestValidator interface {
	// Validate runs schema validation against all manifest files in manifestDir.
	// The target argument is a tag (e.g. "src", "dst") used to label the result.
	// The context can be used for cancellation and timeout control.
	// A non-nil error indicates the validator itself failed to run; schema
	// errors in the manifests are returned via ValidationResult.
	Validate(ctx context.Context, target, manifestDir string) (ValidationResult, error)
}

// ApplicationFetcher resolves an anchor.ApplicationRef to a parsed
// Application model.
//
// For same-repo refs (Repo == ""), implementations read from the working tree
// at localRepoRoot. For cross-repo refs, implementations fetch the file from
// the named remote at Branch tip without affecting the local working tree.
//
// Any failure (network, missing file, parse error) is returned as a hard
// error so the caller can fail loudly; partial results are not supported.
type ApplicationFetcher interface {
	Fetch(ctx context.Context, ref anchor.ApplicationRef, localRepoRoot string) (models.Application, error)
}
