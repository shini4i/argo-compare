package app

import (
	"fmt"
	"strings"

	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"gopkg.in/yaml.v3"
)

// Target encapsulates the chart rendering workflow for a single application source.
type Target struct {
	CmdRunner       ports.CmdRunner
	FileReader      ports.FileReader
	HelmProcessor   ports.HelmChartsProcessor
	CacheDir        string
	TmpDir          string
	RepoCredentials []models.RepoCredentials
	Log             *logging.Logger

	File string
	Type string
	App  models.Application
}

// parse loads the target application's manifest into memory and validates its structure.
func (t *Target) parse() error {
	app := models.Application{}

	var file string

	if !strings.Contains(t.File, "/tmp/") {
		gitRepoRoot, err := GetGitRepoRoot()
		if err != nil {
			return err
		}
		file = fmt.Sprintf("%s/%s", gitRepoRoot, t.File)
	} else {
		file = t.File
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
func (t *Target) ensureHelmCharts() error {
	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			if err := t.HelmProcessor.DownloadHelmChart(
				t.CmdRunner,
				utils.CustomGlobber{},
				t.CacheDir,
				source.RepoURL,
				source.Chart,
				source.TargetRevision,
				t.RepoCredentials,
			); err != nil {
				return err
			}
		}
		return nil
	}

	return t.HelmProcessor.DownloadHelmChart(
		t.CmdRunner,
		utils.CustomGlobber{},
		t.CacheDir,
		t.App.Spec.Source.RepoURL,
		t.App.Spec.Source.Chart,
		t.App.Spec.Source.TargetRevision,
		t.RepoCredentials,
	)
}

// extractCharts unpacks cached Helm charts into the working directories.
func (t *Target) extractCharts() error {
	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			if err := t.HelmProcessor.ExtractHelmChart(
				t.CmdRunner,
				utils.CustomGlobber{},
				source.Chart,
				source.TargetRevision,
				fmt.Sprintf("%s/%s", t.CacheDir, source.RepoURL),
				t.TmpDir,
				t.Type,
			); err != nil {
				return err
			}
		}
		return nil
	}

	return t.HelmProcessor.ExtractHelmChart(
		t.CmdRunner,
		utils.CustomGlobber{},
		t.App.Spec.Source.Chart,
		t.App.Spec.Source.TargetRevision,
		fmt.Sprintf("%s/%s", t.CacheDir, t.App.Spec.Source.RepoURL),
		t.TmpDir,
		t.Type,
	)
}

// renderAppSources runs Helm template rendering for each application source.
func (t *Target) renderAppSources() error {
	var releaseName string

	if !t.App.Spec.MultiSource {
		if t.App.Spec.Source.Helm.ReleaseName != "" {
			releaseName = t.App.Spec.Source.Helm.ReleaseName
		} else {
			releaseName = t.App.Metadata.Name
		}
	}

	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			if source.Helm.ReleaseName != "" {
				releaseName = source.Helm.ReleaseName
			} else {
				releaseName = t.App.Metadata.Name
			}
			if err := t.HelmProcessor.RenderAppSource(
				t.CmdRunner,
				releaseName,
				source.Chart,
				source.TargetRevision,
				t.TmpDir,
				t.Type,
				t.App.Spec.Destination.Namespace,
			); err != nil {
				return err
			}
		}
		return nil
	}

	return t.HelmProcessor.RenderAppSource(
		t.CmdRunner,
		releaseName,
		t.App.Spec.Source.Chart,
		t.App.Spec.Source.TargetRevision,
		t.TmpDir,
		t.Type,
		t.App.Spec.Destination.Namespace,
	)
}
