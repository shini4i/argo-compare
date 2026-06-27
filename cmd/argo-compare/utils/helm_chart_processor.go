package utils

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/shini4i/argo-compare/internal/ui"
)

// ErrFailedToDownloadChart indicates Helm failed to pull the requested chart.
var ErrFailedToDownloadChart = errors.New("failed to download chart")

// ErrInvalidValueFile is returned when a helm.valueFiles entry is rejected for
// security reasons (empty, absolute path, or parent-directory traversal).
var ErrInvalidValueFile = errors.New("invalid valueFile path")

// validateValueFile rejects valueFiles entries that could read files outside
// the chart directory. It enforces three rules that together prevent the
// Application YAML (an untrusted, PR-author-controlled input) from exfiltrating
// host secrets through the rendered diff posted to MR comments:
//   - non-empty (empty paths have no valid use and are a sign of misconfiguration)
//   - not absolute (absolute paths bypass the chart-dir prefix entirely on POSIX)
//   - no parent traversal (filepath.Clean("../foo") would escape the chart dir)
func validateValueFile(vf string) error {
	if vf == "" {
		return fmt.Errorf("%w: path must not be empty", ErrInvalidValueFile)
	}
	if filepath.IsAbs(vf) {
		return fmt.Errorf("%w: absolute paths are not allowed: %q", ErrInvalidValueFile, vf)
	}
	cleaned := filepath.Clean(vf)
	if strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf("%w: path traversal is not allowed: %q", ErrInvalidValueFile, vf)
	}
	return nil
}

// helmParamNameRe is the allowlist for helm parameter names forwarded to
// --set / --set-string. Dots and brackets have legitimate meaning
// (path separators and array indices in Helm's strvals grammar). Characters
// outside this set — particularly '=', ',', '{', '}', and '\' — either
// corrupt the key=value format or inject new assignments when the argv element
// is re-parsed by helm's strvals library.
var helmParamNameRe = regexp.MustCompile(`^[A-Za-z0-9_.[\]-]+$`)

// validateHelmParamName rejects a parameter name that contains strvals
// metacharacters which would silently overwrite unrelated chart values.
// Dots and brackets are intentional and therefore allowed.
func validateHelmParamName(name string) error {
	if !helmParamNameRe.MatchString(name) {
		return fmt.Errorf("helm parameter name contains characters invalid for --set: %q", name)
	}
	return nil
}

// escapeHelmSetValue escapes special characters in a helm `--set` /
// `--set-string` value so helm's strvals parser treats them literally:
//   - '\' is an escape character in strvals; escape it first
//   - ',' separates assignments; escape to keep the value atomic
//   - '{...}' is interpreted as a list literal; escape braces
//
// The parameter name is validated separately by validateHelmParamName; dots in
// names are intentional path separators and must not be escaped.
func escapeHelmSetValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, ",", `\,`)
	v = strings.ReplaceAll(v, "{", `\{`)
	v = strings.ReplaceAll(v, "}", `\}`)
	return v
}

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
func (g RealHelmChartProcessor) GenerateValuesFile(chartName, tmpDir, targetType, values string, valuesObject map[string]any) error {
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
// If credentials are available, it first runs "helm registry login" before
// pulling. The password is piped to helm via stdin (--password-stdin) so it
// never appears in argv, where it would be readable by any local user through
// /proc/<pid>/cmdline.
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
			flagDestination, chartLocation,
			flagVersion, req.TargetRevision)

		g.logOutput(stdout, stderr)
		return runErr
	})

	if err != nil {
		return fmt.Errorf("%w: %w", ErrFailedToDownloadChart, err)
	}

	return nil
}

// pullRepoName is the repository entry name used in the temporary
// repositories.yaml generated for authenticated HTTP chart pulls.
const pullRepoName = "argo-compare-repo"

// Helm CLI flag names shared by multiple helm invocations in this file.
const (
	flagDestination      = "--destination"
	flagVersion          = "--version"
	flagRepositoryConfig = "--repository-config"
	flagRepositoryCache  = "--repository-cache"
)

// pullHTTPChart downloads a chart from an HTTP/HTTPS Helm repository.
//
// Without credentials the chart is pulled directly via --repo. With
// credentials, `helm pull` offers no --password-stdin equivalent and passing
// --password in argv would expose the secret to any local user through
// /proc/<pid>/cmdline. Instead, credentials are written to a temporary
// repositories.yaml and the chart is pulled through that named repo entry
// (helm resolves repo-entry credentials only for name-based pulls, not for
// --repo URLs). `helm repo update` is required first because name-based pulls
// read the repo index from the local cache.
//
// The generated repositories.yaml lives in the OS temp directory with
// owner-only (0600) permissions and is removed by the deferred cleanup.
func (g RealHelmChartProcessor) pullHTTPChart(ctx context.Context, cmdRunner ports.CmdRunner, req ports.ChartDownloadRequest, creds ports.RegistryCredentials, chartLocation string) error {
	hasCreds := creds.Username != "" && creds.Password != ""

	var repoCfgPath, repoCachePath string
	if hasCreds {
		entry := helmRepoEntry{
			Name:     pullRepoName,
			URL:      req.RepoURL,
			Username: creds.Username,
			Password: creds.Password,
		}
		var cleanup func()
		var err error
		repoCfgPath, repoCachePath, cleanup, err = writeRepoEntriesConfig("", []helmRepoEntry{entry})
		if err != nil {
			return err
		}
		defer cleanup()
	}

	retryCfg := helpers.DefaultRetryConfig()
	err := helpers.WithRetry(ctx, retryCfg, func() error {
		if hasCreds {
			return g.pullThroughRepoConfig(ctx, cmdRunner, req, chartLocation, repoCfgPath, repoCachePath)
		}

		stdout, stderr, runErr := cmdRunner.Run(ctx, "helm",
			"pull",
			"--repo", req.RepoURL,
			req.ChartName,
			flagVersion, req.TargetRevision,
			flagDestination, chartLocation,
		)

		g.logOutput(stdout, stderr)
		return runErr
	})

	if err != nil {
		return fmt.Errorf("%w: %w", ErrFailedToDownloadChart, err)
	}

	return nil
}

// pullThroughRepoConfig refreshes the index cache for the temporary repo entry
// and pulls the requested chart through it. Both helm invocations reference the
// isolated repository config and cache so the host helm configuration is never
// touched and credentials stay inside the generated repositories.yaml.
func (g RealHelmChartProcessor) pullThroughRepoConfig(ctx context.Context, cmdRunner ports.CmdRunner, req ports.ChartDownloadRequest, chartLocation, repoCfgPath, repoCachePath string) error {
	stdout, stderr, err := cmdRunner.Run(ctx, "helm",
		"repo", "update", pullRepoName,
		flagRepositoryConfig, repoCfgPath,
		flagRepositoryCache, repoCachePath,
	)
	g.logOutput(stdout, stderr)
	if err != nil {
		return fmt.Errorf("update repo index for %q: %w", req.RepoURL, err)
	}

	stdout, stderr, err = cmdRunner.Run(ctx, "helm",
		"pull", fmt.Sprintf("%s/%s", pullRepoName, req.ChartName),
		flagVersion, req.TargetRevision,
		flagDestination, chartLocation,
		flagRepositoryConfig, repoCfgPath,
		flagRepositoryCache, repoCachePath,
	)
	g.logOutput(stdout, stderr)
	return err
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

// chartMetadata is the slice of Chart.yaml we need to detect subchart
// dependencies. Helm charts can declare many more fields; we deliberately
// ignore them.
type chartMetadata struct {
	Dependencies []chartDependency `yaml:"dependencies"`
}

// chartDependency mirrors the on-disk shape of a Chart.yaml dependency entry.
type chartDependency struct {
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	Repository string `yaml:"repository"`
}

// helmRepoFile mirrors helm's repositories.yaml schema as written by
// `helm repo add`. We generate one per-render so each subchart-bearing chart
// gets an isolated repo config and the host environment is left untouched.
type helmRepoFile struct {
	APIVersion   string          `yaml:"apiVersion"`
	Repositories []helmRepoEntry `yaml:"repositories"`
}

// helmRepoEntry represents a single repository entry in helm's repositories.yaml.
type helmRepoEntry struct {
	Name     string `yaml:"name"`
	URL      string `yaml:"url"`
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
}

// noopCleanup is the cleanup func returned by writeRepoConfig on its early
// error paths. Those paths fail before any temp file or directory is created
// (or after cleaning up inline), so the caller's deferred cleanup must be safe
// to call yet has nothing to remove.
func noopCleanup() {
	// Intentionally empty: no resources were acquired on this path.
}

// BuildChartDependencies runs `helm dependency build` against chartDir so that
// subcharts listed in Chart.yaml are fetched into chartDir/charts/. It is a
// no-op when the chart has no Chart.yaml or no dependencies.
//
// Credentials for each unique HTTP(S) dependency repository URL are resolved
// via the credential provider chain and written into a temporary
// repositories.yaml passed to helm via --repository-config. The repository
// cache is also redirected to a temp dir so concurrent runs and the host's
// helm config never interfere.
//
// scratchDir bounds the lifetime of the generated credentials file and helm
// repo cache to the caller's cleanup boundary (typically the per-run tmpDir
// under cfg.TempDirBase). This keeps credentials inside the directory the
// orchestrator will RemoveAll, instead of leaking under /tmp on hard
// termination outside our deferred cleanup.
//
// OCI subchart dependencies (`repository: oci://...`) are not yet supported:
// helm's OCI auth uses a separate registry config, and the few private OCI
// helm registries we ship against today are referenced as top-level
// `spec.source.chart` entries rather than as subcharts. Charts with OCI deps
// will surface helm's own error message verbatim — easier to diagnose than a
// silent skip.
func (g RealHelmChartProcessor) BuildChartDependencies(ctx context.Context, deps ports.HelmDeps, chartDir, scratchDir string) error {
	meta, err := readChartMetadata(chartDir)
	if err != nil {
		return err
	}
	if meta == nil || len(meta.Dependencies) == 0 {
		return nil
	}

	repoCfgPath, repoCachePath, cleanup, err := g.writeRepoConfig(ctx, deps, meta.Dependencies, scratchDir)
	if err != nil {
		return err
	}
	defer cleanup()

	g.Log.Debugf("Building subchart dependencies for chart at [%s]", ui.Cyan(chartDir))

	stdout, stderr, err := deps.CmdRunner.Run(ctx, "helm",
		"dependency", "build",
		flagRepositoryConfig, repoCfgPath,
		flagRepositoryCache, repoCachePath,
		chartDir,
	)
	g.logOutput(stdout, stderr)
	if err != nil {
		return fmt.Errorf("helm dependency build for %q: %w", chartDir, err)
	}
	return nil
}

// readChartMetadata reads chartDir/Chart.yaml and returns its parsed metadata.
// A missing Chart.yaml is not an error — non-Helm path sources legitimately
// have none, and BuildChartDependencies must remain a no-op for them.
func readChartMetadata(chartDir string) (*chartMetadata, error) {
	raw, err := os.ReadFile(filepath.Join(chartDir, "Chart.yaml")) // #nosec G304 -- chartDir is owned by argo-compare under TmpDir
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read Chart.yaml in %q: %w", chartDir, err)
	}
	var meta chartMetadata
	if err := yaml.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("parse Chart.yaml in %q: %w", chartDir, err)
	}
	return &meta, nil
}

// writeRepoConfig writes a fresh repositories.yaml describing each unique
// HTTP(S) dependency repository in deps, with credentials supplied from the
// provider chain when available. It returns the config file path, a separate
// repository-cache directory, and a cleanup func that removes both. The
// caller MUST invoke cleanup once helm dependency build returns.
//
// scratchDir bounds both files to the run's cleanup boundary so that on hard
// termination (panic / SIGKILL) the credentials file does not leak into the
// system /tmp outside the orchestrator's deferred RemoveAll.
func (g RealHelmChartProcessor) writeRepoConfig(ctx context.Context, helmDeps ports.HelmDeps, deps []chartDependency, scratchDir string) (string, string, func(), error) {
	seen := make(map[string]struct{})
	var entries []helmRepoEntry
	entryCount := 0
	for _, dep := range deps {
		if dep.Repository == "" || strings.HasPrefix(dep.Repository, "file://") {
			continue
		}
		if strings.HasPrefix(dep.Repository, "oci://") {
			// helm handles OCI auth via registry config, not repositories.yaml.
			// Skip silently so helm surfaces its own error if creds are needed.
			continue
		}
		if strings.HasPrefix(dep.Repository, "@") {
			// Alias-prefixed repos refer to entries the user has previously
			// registered with `helm repo add`. We run with an isolated repo
			// config so the alias is not resolvable here; let helm surface
			// its own clearer error rather than silently writing a bogus
			// `url: "@alias"` entry into our config.
			g.Log.Debugf("Skipping alias-based dependency %q; argo-compare uses an isolated repo config", dep.Repository)
			continue
		}
		if _, ok := seen[dep.Repository]; ok {
			continue
		}
		seen[dep.Repository] = struct{}{}

		creds := resolveCredentials(ctx, g.Log, helmDeps.CredentialProviders, dep.Repository)
		entries = append(entries, helmRepoEntry{
			Name:     fmt.Sprintf("argo-compare-dep-%d", entryCount),
			URL:      dep.Repository,
			Username: creds.Username,
			Password: creds.Password,
		})
		entryCount++
	}

	return writeRepoEntriesConfig(scratchDir, entries)
}

// writeRepoEntriesConfig writes the given repository entries to a fresh
// repositories.yaml under scratchDir (the OS default temp directory when
// empty) and creates a matching repository-cache directory. The file is
// created by os.CreateTemp with owner-only (0600) permissions, which is the
// access control for the credentials it may contain. It returns the config
// file path, the cache directory path, and a cleanup func that removes both.
// The caller MUST invoke cleanup once the helm invocation that consumes the
// config returns.
func writeRepoEntriesConfig(scratchDir string, entries []helmRepoEntry) (string, string, func(), error) {
	repoCfgFile, err := os.CreateTemp(scratchDir, "argo-compare-helm-repos-*.yaml")
	if err != nil {
		return "", "", noopCleanup, fmt.Errorf("create repositories.yaml tempfile: %w", err)
	}
	repoCfgPath := repoCfgFile.Name()

	cleanup := func() {
		_ = os.Remove(repoCfgPath)
	}

	encoded, err := yaml.Marshal(helmRepoFile{APIVersion: "v1", Repositories: entries})
	if err != nil {
		_ = repoCfgFile.Close()
		cleanup()
		return "", "", noopCleanup, fmt.Errorf("marshal repositories.yaml: %w", err)
	}
	if _, err := repoCfgFile.Write(encoded); err != nil {
		_ = repoCfgFile.Close()
		cleanup()
		return "", "", noopCleanup, fmt.Errorf("write repositories.yaml: %w", err)
	}
	if err := repoCfgFile.Close(); err != nil {
		cleanup()
		return "", "", noopCleanup, fmt.Errorf("close repositories.yaml: %w", err)
	}

	repoCachePath, err := os.MkdirTemp(scratchDir, "argo-compare-helm-cache-")
	if err != nil {
		cleanup()
		return "", "", noopCleanup, fmt.Errorf("create repository cache tempdir: %w", err)
	}

	combinedCleanup := func() {
		cleanup()
		_ = os.RemoveAll(repoCachePath)
	}
	return repoCfgPath, repoCachePath, combinedCleanup, nil
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
//
// Argument ordering mirrors ArgoCD's helm renderer: the chart's own values.yaml
// is auto-loaded by `helm template`, then explicit valueFiles from the
// Application's spec.source.helm.valueFiles are applied in order, then any
// inline values from spec.source.helm.values / valuesObject, and finally
// spec.source.helm.parameters as `--set` / `--set-string`. Helm always applies
// `--set` on top of `--values`, which matches ArgoCD's documented precedence
// (parameters > valuesObject > values > valueFiles). The inline values file is
// only added when it exists on disk so that Applications without inline values
// do not crash on a missing path.
//
// The context can be used to cancel the rendering or set a timeout.
func (g RealHelmChartProcessor) RenderAppSource(ctx context.Context, cmdRunner ports.CmdRunner, req ports.ChartRenderRequest) error {
	g.Log.Debugf("Rendering [%s] chart's version [%s] templates using release name [%s]",
		ui.Cyan(req.ChartName),
		ui.Cyan(req.ChartVersion),
		ui.Cyan(req.ReleaseName))

	chartDir := fmt.Sprintf("%s/charts/%s/%s", req.TmpDir, req.TargetType, req.ChartName)

	args := []string{
		"template",
		"--release-name", req.ReleaseName,
		chartDir,
		"--output-dir", fmt.Sprintf("%s/templates/%s", req.TmpDir, req.TargetType),
	}

	for _, vf := range req.ValueFiles {
		if err := validateValueFile(vf); err != nil {
			return err
		}
		args = append(args, "--values", filepath.Join(chartDir, vf))
	}

	inlineValuesPath := fmt.Sprintf("%s/%s-values-%s.yaml", req.TmpDir, req.ChartName, req.TargetType)
	if _, err := os.Stat(inlineValuesPath); err == nil || !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return fmt.Errorf("check inline values file %q: %w", inlineValuesPath, err)
		}
		args = append(args, "--values", inlineValuesPath)
	}

	for _, p := range req.Parameters {
		if err := validateHelmParamName(p.Name); err != nil {
			return err
		}
		flag := "--set"
		if p.ForceString {
			flag = "--set-string"
		}
		args = append(args, flag, fmt.Sprintf("%s=%s", p.Name, escapeHelmSetValue(p.Value)))
	}

	args = append(args, "--namespace", req.Namespace)

	_, stderr, err := cmdRunner.Run(ctx, "helm", args...)

	if len(stderr) > 0 {
		// Helm may emit warnings via stderr even on success; log them for visibility.
		g.Log.Error(stderr)
	}

	return err
}
