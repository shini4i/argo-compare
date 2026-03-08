package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/op/go-logging"
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
	Log                 *logging.Logger

	File string
	Type string
	App  models.Application
}

// parse loads the target application's manifest into memory and validates its structure.
func (t *Target) parse() error {
	app := models.Application{}

	var file string

	// Use filepath.IsAbs to check if the path is absolute rather than checking for /tmp/
	if filepath.IsAbs(t.File) {
		file = t.File
	} else {
		gitRepoRoot, err := GetGitRepoRoot()
		if err != nil {
			return err
		}
		file = filepath.Join(gitRepoRoot, t.File)
	}

	t.Log.Debugf("Parsing %s...", file)

	yamlContent := t.FileReader.ReadFile(file)
	if err := yaml.Unmarshal(yamlContent, &app); err != nil {
		return err
	}

	if err := app.Validate(); err != nil {
		return err
	}

	t.App = app

	return nil
}

// generateValuesFiles materializes Helm values files so templates can be rendered.
func (t *Target) generateValuesFiles() error {
	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			if err := t.HelmProcessor.GenerateValuesFile(source.Chart, t.TmpDir, t.Type, source.Helm.Values, source.Helm.ValuesObject); err != nil {
				return err
			}
		}
		return nil
	}

	return t.HelmProcessor.GenerateValuesFile(
		t.App.Spec.Source.Chart,
		t.TmpDir,
		t.Type,
		t.App.Spec.Source.Helm.Values,
		t.App.Spec.Source.Helm.ValuesObject,
	)
}

// ensureHelmCharts downloads required Helm charts into the configured cache.
// The context can be used to cancel downloads or set a timeout.
func (t *Target) ensureHelmCharts(ctx context.Context) error {
	deps := ports.HelmDeps{
		CmdRunner:           t.CmdRunner,
		Globber:             t.Globber,
		CredentialProviders: t.CredentialProviders,
	}

	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
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

	req := ports.ChartDownloadRequest{
		CacheDir:       t.CacheDir,
		RepoURL:        t.App.Spec.Source.RepoURL,
		ChartName:      t.App.Spec.Source.Chart,
		TargetRevision: t.App.Spec.Source.TargetRevision,
	}
	return t.HelmProcessor.DownloadHelmChart(ctx, deps, req)
}

// extractCharts unpacks cached Helm charts into the working directories.
// The context can be used to cancel extraction or set a timeout.
func (t *Target) extractCharts(ctx context.Context) error {
	deps := ports.HelmDeps{CmdRunner: t.CmdRunner, Globber: t.Globber, CredentialProviders: t.CredentialProviders}

	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			repoURL := strings.TrimPrefix(source.RepoURL, "oci://")
			req := ports.ChartExtractRequest{
				ChartName:     source.Chart,
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

	repoURL := strings.TrimPrefix(t.App.Spec.Source.RepoURL, "oci://")
	req := ports.ChartExtractRequest{
		ChartName:     t.App.Spec.Source.Chart,
		ChartVersion:  t.App.Spec.Source.TargetRevision,
		ChartLocation: fmt.Sprintf("%s/%s", t.CacheDir, repoURL),
		TmpDir:        t.TmpDir,
		TargetType:    t.Type,
	}
	return t.HelmProcessor.ExtractHelmChart(ctx, deps, req)
}

// renderAppSources runs Helm template rendering for each application source.
// The context can be used to cancel rendering or set a timeout.
func (t *Target) renderAppSources(ctx context.Context) error {
	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			releaseName := t.App.Metadata.Name
			if source.Helm.ReleaseName != "" {
				releaseName = source.Helm.ReleaseName
			}
			req := ports.ChartRenderRequest{
				ReleaseName:  releaseName,
				ChartName:    source.Chart,
				ChartVersion: source.TargetRevision,
				TmpDir:       t.TmpDir,
				TargetType:   t.Type,
				Namespace:    t.App.Spec.Destination.Namespace,
			}
			if err := t.HelmProcessor.RenderAppSource(ctx, t.CmdRunner, req); err != nil {
				return err
			}
		}
		return nil
	}

	releaseName := t.App.Metadata.Name
	if t.App.Spec.Source.Helm.ReleaseName != "" {
		releaseName = t.App.Spec.Source.Helm.ReleaseName
	}
	req := ports.ChartRenderRequest{
		ReleaseName:  releaseName,
		ChartName:    t.App.Spec.Source.Chart,
		ChartVersion: t.App.Spec.Source.TargetRevision,
		TmpDir:       t.TmpDir,
		TargetType:   t.Type,
		Namespace:    t.App.Spec.Destination.Namespace,
	}
	return t.HelmProcessor.RenderAppSource(ctx, t.CmdRunner, req)
}
