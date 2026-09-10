package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"gopkg.in/yaml.v3"
)

// Target type constants identify the source and destination manifests for comparison.
const (
	TargetTypeSource      = "src"
	TargetTypeDestination = "dst"
)

// Target encapsulates the chart rendering workflow for a single application source.
type Target struct {
	CmdRunner           ports.CmdRunner
	FileReader          ports.FileReader
	HelmProcessor       ports.HelmChartsProcessor
	Globber             ports.Globber
	CacheDir            string
	TmpDir              string
	CredentialProviders []ports.CredentialProvider
	Log                 *logger.Logger

	File string
	Type string
	App  models.Application
}

// parse loads the target application's manifest into memory and validates its structure.
func (t *Target) parse() error {
	yamlContent, err := readManifest(t.FileReader, t.File)
	if err != nil {
		return err
	}

	t.Log.Debugf("Parsing %s...", t.File)

	app := models.Application{}
	if err := yaml.Unmarshal(yamlContent, &app); err != nil {
		return err
	}

	if err := app.Validate(); err != nil {
		return err
	}

	t.App = app

	return nil
}

// readManifest reads a manifest file, resolving a relative path against the
// repository root so callers can pass either form.
func readManifest(fileReader ports.FileReader, path string) ([]byte, error) {
	file := path

	// Use filepath.IsAbs to check if the path is absolute rather than checking for /tmp/
	if !filepath.IsAbs(path) {
		gitRepoRoot, err := GetGitRepoRoot()
		if err != nil {
			return nil, err
		}
		file = filepath.Join(gitRepoRoot, path)
	}

	content, err := fileReader.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read application file %q: %w", file, err)
	}

	return content, nil
}

// parseApplicationSet loads and validates an ApplicationSet manifest from path.
func parseApplicationSet(fileReader ports.FileReader, path string) (*models.ApplicationSet, error) {
	yamlContent, err := readManifest(fileReader, path)
	if err != nil {
		return nil, err
	}

	return parseApplicationSetContent(yamlContent)
}

// parseApplicationSetContent validates an ApplicationSet manifest already held
// in memory, such as one read out of a Git tree.
func parseApplicationSetContent(yamlContent []byte) (*models.ApplicationSet, error) {
	appSet := &models.ApplicationSet{}
	if err := yaml.Unmarshal(yamlContent, appSet); err != nil {
		return nil, err
	}

	if err := appSet.Validate(); err != nil {
		return nil, err
	}

	return appSet, nil
}

// generateValuesFiles materializes Helm values files so templates can be rendered.
// The chart name used for the values file derives from effectiveChartName so
// registry-based (chart) and Git-path-based (path) sources both produce
// stably-named values files for the downstream render step to find.
//
// Sources that declare no inline values (no helm.values and no helm.valuesObject)
// skip the generation entirely — the renderer detects the missing file and
// omits the corresponding --values flag. This supports Applications that rely
// solely on helm.valueFiles or on the chart's own defaults.
func (t *Target) generateValuesFiles() error {
	for _, source := range t.renderableSources() {
		if !hasInlineValues(source) {
			continue
		}
		if err := t.HelmProcessor.GenerateValuesFile(effectiveChartName(source), t.TmpDir, t.Type, source.Helm.Values, source.Helm.ValuesObject); err != nil {
			return err
		}
	}
	return nil
}

// hasInlineValues reports whether the source carries inline values that must
// be rendered into a temporary file (helm.values raw YAML, or helm.valuesObject
// structured form). helm.valueFiles are handled separately as paths into the
// chart directory and do not count as inline.
//
// The nil check on ValuesObject mirrors GenerateValuesFile's guard; both treat
// a non-nil but empty map as "has values" to produce consistent behaviour with
// the existing YAML marshalling path.
func hasInlineValues(source *models.Source) bool {
	if source == nil {
		return false
	}
	return source.Helm.Values != "" || source.Helm.ValuesObject != nil
}

// ensureHelmCharts downloads required Helm charts into the configured cache.
// The context can be used to cancel downloads or set a timeout.
func (t *Target) ensureHelmCharts(ctx context.Context) error {
	deps := ports.HelmDeps{
		CmdRunner:           t.CmdRunner,
		Globber:             t.Globber,
		CredentialProviders: t.CredentialProviders,
	}

	for _, source := range t.renderableSources() {
		req := ports.ChartDownloadRequest{
			CacheDir:       t.CacheDir,
			RepoURL:        source.RepoURL,
			ChartName:      source.Chart,
			TargetRevision: source.TargetRevision,
		}
		if err := t.HelmProcessor.DownloadHelmChart(ctx, deps, req); err != nil {
			return err
		}
	}
	return nil
}

// extractCharts unpacks cached Helm charts into the working directories.
// The context can be used to cancel extraction or set a timeout.
func (t *Target) extractCharts(ctx context.Context) error {
	deps := ports.HelmDeps{CmdRunner: t.CmdRunner, Globber: t.Globber, CredentialProviders: t.CredentialProviders}

	for _, source := range t.renderableSources() {
		repoURL := strings.TrimPrefix(source.RepoURL, "oci://")
		req := ports.ChartExtractRequest{
			ChartName:     effectiveChartName(source),
			ChartVersion:  source.TargetRevision,
			ChartLocation: fmt.Sprintf("%s/%s", t.CacheDir, repoURL),
			TmpDir:        t.TmpDir,
			TargetType:    t.Type,
		}
		if err := t.HelmProcessor.ExtractHelmChart(ctx, deps, req); err != nil {
			return err
		}
	}
	return nil
}

// renderAppSources runs Helm template rendering for each renderable source.
// helm.valueFiles are resolved to absolute paths first, so a "$ref/path" entry
// reaches Helm as the file materialized for this leg.
func (t *Target) renderAppSources(ctx context.Context) error {
	for _, source := range t.renderableSources() {
		releaseName := t.App.Metadata.Name
		if source.Helm.ReleaseName != "" {
			releaseName = source.Helm.ReleaseName
		}
		parameters, err := t.resolveSourceParameters(source)
		if err != nil {
			return err
		}
		valueFiles, err := t.resolveValueFiles(source)
		if err != nil {
			return err
		}
		req := ports.ChartRenderRequest{
			ReleaseName:  releaseName,
			ChartName:    effectiveChartName(source),
			ChartVersion: source.TargetRevision,
			TmpDir:       t.TmpDir,
			TargetType:   t.Type,
			Namespace:    t.App.Spec.Destination.Namespace,
			ValueFiles:   valueFiles,
			Parameters:   parameters,
		}
		if err := t.HelmProcessor.RenderAppSource(ctx, t.CmdRunner, req); err != nil {
			return err
		}
	}
	return nil
}

// resolveSourceParameters merges a source's inline helm.parameters with any
// .argocd-source override files materialized alongside its chart, so image
// bumps written by argo-watcher / Argo CD Image Updater are reflected in the
// rendered diff. The chart directory mirrors the layout produced by chart
// materialization and extraction (TmpDir/charts/<Type>/<ChartName>).
func (t *Target) resolveSourceParameters(source *models.Source) ([]models.HelmParameter, error) {
	chartDir := filepath.Join(t.TmpDir, "charts", t.Type, effectiveChartName(source))
	return resolveHelmParameters(t.FileReader, source, chartDir, t.App.Metadata.Name)
}
