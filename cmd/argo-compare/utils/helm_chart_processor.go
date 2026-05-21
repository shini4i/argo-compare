package utils

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/shini4i/argo-compare/internal/ui"
)

// ErrFailedToDownloadChart indicates Helm failed to pull the requested chart.
var ErrFailedToDownloadChart = errors.New("failed to download chart")

// isOCIRegistry returns true if the repo URL refers to an OCI registry (no http/https scheme).
func isOCIRegistry(repoURL string) bool {
	return repoURL != "" &&
		!strings.HasPrefix(repoURL, "http://") &&
		!strings.HasPrefix(repoURL, "https://")
}

// RealHelmChartProcessor coordinates Helm CLI interactions for chart lifecycle tasks.
type RealHelmChartProcessor struct {
	Log *logger.Logger
}

// GenerateValuesFile creates a Helm values file for a given chart in a specified directory.
// It takes a chart name, a temporary directory for storing the file, the target type categorizing the application,
// and the content of the values file in string format.
// The function first attempts to create the file and writes the provided values content to disk.
func (g RealHelmChartProcessor) GenerateValuesFile(chartName, tmpDir, targetType, values string, valuesObject map[string]interface{}) error {
	yamlFile, err := os.Create(fmt.Sprintf("%s/%s-values-%s.yaml", tmpDir, chartName, targetType))
	if err != nil {
		return err
	}

	defer func(yamlFile *os.File) {
		if err := yamlFile.Close(); err != nil {
			g.Log.Errorf("failed to close values file %s: %v", yamlFile.Name(), err)
		}
	}(yamlFile)

	var data []byte
	if values != "" {
		// Write the 'values' field if it is provided
		data = []byte(values)
	} else if valuesObject != nil {
		// Serialize the 'valuesObject' if it is provided
		data, err = yaml.Marshal(valuesObject)
		if err != nil {
			return err
		}
	} else {
		return errors.New("either 'values' or 'valuesObject' must be provided")
	}

	if _, err := yamlFile.Write(data); err != nil {
		return err
	}

	return nil
}

// DownloadHelmChart fetches a specified version of a Helm chart from a given repository URL and
// stores it in a cache directory. The function leverages the provided CmdRunner to execute
// the helm pull command and Globber to deal with possible non-standard chart naming.
// If the chart is already present in the cache, the function just logs the information and doesn't download it again.
// The context can be used to cancel the download or set a timeout.
func (g RealHelmChartProcessor) DownloadHelmChart(ctx context.Context, deps ports.HelmDeps, req ports.ChartDownloadRequest) error {
	// Normalize OCI URLs: ArgoCD manifests may specify repoURL with an "oci://" scheme prefix.
	// Strip it so that cache paths, credential matching, and helm commands receive a bare hostname.
	req.RepoURL = strings.TrimPrefix(req.RepoURL, "oci://")

	chartLocation := fmt.Sprintf("%s/%s", req.CacheDir, req.RepoURL)

	if err := os.MkdirAll(chartLocation, 0750); err != nil {
		return fmt.Errorf("failed to create chart cache directory %q: %w", chartLocation, err)
	}

	// A bit hacky, but we need to support cases when helm chart tgz filename does not follow the standard naming convention
	// For example, sonarqube-4.0.0+315.tgz
	chartFileName, err := deps.Globber.Glob(fmt.Sprintf("%s/%s-%s*.tgz", chartLocation, req.ChartName, req.TargetRevision))
	if err != nil {
		return fmt.Errorf("failed to search for chart %s version %s in %s: %w", req.ChartName, req.TargetRevision, chartLocation, err)
	}

	if len(chartFileName) == 0 {
		if err := g.downloadChartFromRepo(ctx, deps, req, chartLocation); err != nil {
			return err
		}
	} else {
		g.Log.Debugf("Version [%s] of [%s] chart is present in the cache...",
			ui.Cyan(req.TargetRevision),
			ui.Cyan(req.ChartName))
	}

	return nil
}

// downloadChartFromRepo performs the actual helm pull operation with retry logic.
// It resolves credentials via the provider chain and delegates to OCI or HTTP-specific pull methods.
func (g RealHelmChartProcessor) downloadChartFromRepo(ctx context.Context, deps ports.HelmDeps, req ports.ChartDownloadRequest, chartLocation string) error {
	creds := resolveCredentials(ctx, g.Log, deps.CredentialProviders, req.RepoURL)

	g.Log.Debugf("Downloading version [%s] of [%s] chart...",
		ui.Cyan(req.TargetRevision),
		ui.Cyan(req.ChartName))

	if isOCIRegistry(req.RepoURL) {
		return g.pullOCIChart(ctx, deps.CmdRunner, req, creds, chartLocation)
	}

	return g.pullHTTPChart(ctx, deps.CmdRunner, req, creds, chartLocation)
}

// resolveCredentials iterates the provider chain and returns the first matching credentials.
// Returns empty credentials if no provider matches. Provider errors are logged and cause
// fallthrough to the next provider in the chain.
func resolveCredentials(ctx context.Context, log *logger.Logger, providers []ports.CredentialProvider, registryURL string) ports.RegistryCredentials {
	for _, p := range providers {
		if p.Matches(registryURL) {
			creds, err := p.GetCredentials(ctx, registryURL)
			if err != nil {
				log.Warningf("Credential provider failed for [%s]: %v; trying next provider", registryURL, err)
				continue
			}
			if creds.Username != "" && creds.Password != "" {
				return creds
			}
		}
	}
	return ports.RegistryCredentials{}
}

// pullOCIChart downloads a chart from an OCI registry.
// If credentials are available, it first runs "helm registry login" before pulling.
func (g RealHelmChartProcessor) pullOCIChart(ctx context.Context, cmdRunner ports.CmdRunner, req ports.ChartDownloadRequest, creds ports.RegistryCredentials, chartLocation string) error {
	// Authenticate with the OCI registry if credentials are available.
	if creds.Username != "" && creds.Password != "" {
		g.Log.Debugf("Logging into OCI registry [%s]...", ui.Cyan(req.RepoURL))

		stdout, stderr, err := cmdRunner.RunWithStdin(ctx, creds.Password, "helm",
			"registry", "login",
			req.RepoURL,
			"--username", creds.Username,
			"--password-stdin")

		g.logOutput(stdout, stderr)

		if err != nil {
			return fmt.Errorf("failed to login to OCI registry %q: %w", req.RepoURL, err)
		}
	}

	// Pull the chart from OCI registry (no --repo, --username, --password flags).
	pullRef := fmt.Sprintf("oci://%s/%s", req.RepoURL, req.ChartName)

	retryCfg := helpers.DefaultRetryConfig()
	err := helpers.WithRetry(ctx, retryCfg, func() error {
		stdout, stderr, runErr := cmdRunner.Run(ctx, "helm",
			"pull", pullRef,
			"--destination", chartLocation,
			"--version", req.TargetRevision)

		g.logOutput(stdout, stderr)
		return runErr
	})

	if err != nil {
		return fmt.Errorf("%w: %w", ErrFailedToDownloadChart, err)
	}

	return nil
}

// pullHTTPChart downloads a chart from an HTTP/HTTPS Helm repository.
// When credentials are present, it routes through pullHTTPChartWithCreds, which
// keeps the password off argv. Anonymous pulls use the simple --repo flow.
func (g RealHelmChartProcessor) pullHTTPChart(ctx context.Context, cmdRunner ports.CmdRunner, req ports.ChartDownloadRequest, creds ports.RegistryCredentials, chartLocation string) error {
	if creds.Username != "" && creds.Password != "" {
		return g.pullHTTPChartWithCreds(ctx, cmdRunner, req, creds, chartLocation)
	}

	retryCfg := helpers.DefaultRetryConfig()
	err := helpers.WithRetry(ctx, retryCfg, func() error {
		stdout, stderr, runErr := cmdRunner.Run(ctx, "helm",
			"pull",
			"--repo", req.RepoURL,
			req.ChartName,
			"--version", req.TargetRevision,
			"--destination", chartLocation,
		)
		g.logOutput(stdout, stderr)
		return runErr
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFailedToDownloadChart, err)
	}
	return nil
}

// pullHTTPChartWithCreds downloads an authenticated HTTP chart without exposing
// the password on argv. It uses an isolated --repository-config so credentials
// never land in the user's shared repositories.yaml:
//  1. `helm repo add` (password via stdin) writes the repo entry into a temp
//     config file.
//  2. `helm pull <name>/<chart>` reads that same config to authenticate.
//  3. The temp directory is removed on return.
//
// `helm pull` itself does not accept --password-stdin; this two-step is the
// only path that both works and keeps the secret off the process argument list.
func (g RealHelmChartProcessor) pullHTTPChartWithCreds(ctx context.Context, cmdRunner ports.CmdRunner, req ports.ChartDownloadRequest, creds ports.RegistryCredentials, chartLocation string) error {
	helmCfgDir, err := os.MkdirTemp("", "argo-compare-helm-*")
	if err != nil {
		return fmt.Errorf("create temp helm config dir: %w", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(helmCfgDir); rmErr != nil {
			g.Log.Warningf("failed to remove temp helm config dir %s: %v", helmCfgDir, rmErr)
		}
	}()

	repoConfig := filepath.Join(helmCfgDir, "repositories.yaml")
	repoCache := filepath.Join(helmCfgDir, "cache")
	const repoName = "argo-compare-tmp"

	addStdout, addStderr, addErr := cmdRunner.RunWithStdin(ctx, creds.Password, "helm",
		"repo", "add", repoName, req.RepoURL,
		"--username", creds.Username,
		"--password-stdin",
		"--repository-config", repoConfig,
		"--repository-cache", repoCache,
	)
	g.logOutput(addStdout, addStderr)
	if addErr != nil {
		return fmt.Errorf("%w: helm repo add: %w", ErrFailedToDownloadChart, addErr)
	}

	retryCfg := helpers.DefaultRetryConfig()
	err = helpers.WithRetry(ctx, retryCfg, func() error {
		stdout, stderr, runErr := cmdRunner.Run(ctx, "helm",
			"pull", fmt.Sprintf("%s/%s", repoName, req.ChartName),
			"--version", req.TargetRevision,
			"--destination", chartLocation,
			"--repository-config", repoConfig,
			"--repository-cache", repoCache,
		)
		g.logOutput(stdout, stderr)
		return runErr
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFailedToDownloadChart, err)
	}
	return nil
}

// logOutput logs stdout and stderr from command execution if non-empty.
func (g RealHelmChartProcessor) logOutput(stdout, stderr string) {
	if len(stdout) > 0 {
		g.Log.Info(stdout)
	}
	if len(stderr) > 0 {
		g.Log.Error(stderr)
	}
}

// ExtractHelmChart extracts a specific version of a Helm chart from a cache directory
// and stores it in a temporary directory. The function uses the provided CmdRunner to
// execute the tar command and Globber to match the chart file in the cache.
// If multiple files matching the pattern are found, an error is returned.
// The context can be used to cancel the extraction or set a timeout.
func (g RealHelmChartProcessor) ExtractHelmChart(ctx context.Context, deps ports.HelmDeps, req ports.ChartExtractRequest) error {
	g.Log.Debugf("Extracting [%s] chart version [%s] to %s/charts/%s...",
		ui.Cyan(req.ChartName),
		ui.Cyan(req.ChartVersion),
		req.TmpDir, req.TargetType)

	path := fmt.Sprintf("%s/charts/%s/%s", req.TmpDir, req.TargetType, req.ChartName)
	if err := os.MkdirAll(path, 0750); err != nil {
		return fmt.Errorf("failed to create chart extraction directory %q: %w", path, err)
	}

	searchPattern := fmt.Sprintf("%s/%s-%s*.tgz",
		req.ChartLocation,
		req.ChartName,
		req.ChartVersion)

	chartFileName, err := deps.Globber.Glob(searchPattern)
	if err != nil {
		return err
	}

	if len(chartFileName) == 0 {
		return errors.New("chart file not found")
	}

	// It's highly unlikely that we will have more than one file matching the pattern
	// Nevertheless we need to handle this case, please submit an issue if you encounter this
	if len(chartFileName) > 1 {
		return errors.New("more than one chart file found, please check your cache directory")
	}

	stdout, stderr, err := deps.CmdRunner.Run(ctx, "tar",
		"xf",
		chartFileName[0],
		"-C", fmt.Sprintf("%s/charts/%s", req.TmpDir, req.TargetType),
	)

	g.logOutput(stdout, stderr)

	return err
}

// RenderAppSource uses the Helm CLI to render the templates of a given chart.
// It takes a cmdRunner to run the Helm command and a request containing the chart
// information needed for rendering.
// The context can be used to cancel the rendering or set a timeout.
func (g RealHelmChartProcessor) RenderAppSource(ctx context.Context, cmdRunner ports.CmdRunner, req ports.ChartRenderRequest) error {
	g.Log.Debugf("Rendering [%s] chart's version [%s] templates using release name [%s]",
		ui.Cyan(req.ChartName),
		ui.Cyan(req.ChartVersion),
		ui.Cyan(req.ReleaseName))

	_, stderr, err := cmdRunner.Run(ctx,
		"helm",
		"template",
		"--release-name", req.ReleaseName,
		fmt.Sprintf("%s/charts/%s/%s", req.TmpDir, req.TargetType, req.ChartName),
		"--output-dir", fmt.Sprintf("%s/templates/%s", req.TmpDir, req.TargetType),
		"--values", fmt.Sprintf("%s/charts/%s/%s/values.yaml", req.TmpDir, req.TargetType, req.ChartName),
		"--values", fmt.Sprintf("%s/%s-values-%s.yaml", req.TmpDir, req.ChartName, req.TargetType),
		"--namespace", req.Namespace,
	)

	if len(stderr) > 0 {
		// Helm may emit warnings via stderr even on success; log them for visibility.
		g.Log.Error(stderr)
	}

	return err
}
