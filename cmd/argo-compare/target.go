package main

import (
	"fmt"
	"strings"

	interfaces "github.com/shini4i/argo-compare/cmd/argo-compare/interfaces"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"gopkg.in/yaml.v3"

	"github.com/shini4i/argo-compare/internal/models"
)

type Target struct {
	CmdRunner  interfaces.CmdRunner
	FileReader interfaces.FileReader
	File       string
	Type       string // src or dst version
	App        models.Application
}

// parse reads the YAML content from a file and unmarshals it into an Application model.
// It uses the FileReader interface to support different implementations for file reading.
// Returns an error in case of issues during reading, unmarshaling or validation.
func (t *Target) parse() error {
	app := models.Application{}

	var file string

	// if we are working with a temporary file, we don't need to prepend the repo root path
	if !strings.Contains(t.File, "/tmp/") {
		if gitRepoRoot, err := GetGitRepoRoot(); err != nil {
			return err
		} else {
			file = fmt.Sprintf("%s/%s", gitRepoRoot, t.File)
		}
	} else {
		file = t.File
	}

	log.Debugf("Parsing %s...", file)

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

// generateValuesFiles generates Helm values files for the application's sources.
// If the application uses multiple sources, a separate values file is created for each source.
// Otherwise, a single values file is generated for the application's single source.
func (t *Target) generateValuesFiles(helmChartProcessor interfaces.HelmChartsProcessor) error {
	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			if err := helmChartProcessor.GenerateValuesFile(source.Chart, tmpDir, t.Type, source.Helm.Values, source.Helm.ValuesObject); err != nil {
				return err
			}
		}
	} else {
		if err := helmChartProcessor.GenerateValuesFile(t.App.Spec.Source.Chart, tmpDir, t.Type, t.App.Spec.Source.Helm.Values, t.App.Spec.Source.Helm.ValuesObject); err != nil {
			return err
		}
	}
	return nil
}

// ensureHelmCharts downloads Helm charts for the application's sources.
// If the application uses multiple sources, each chart is downloaded separately.
// If the application has a single source, only the respective chart is downloaded.
// In case of any error during download, the error is returned immediately.
func (t *Target) ensureHelmCharts(helmChartProcessor interfaces.HelmChartsProcessor) error {
	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			if err := helmChartProcessor.DownloadHelmChart(t.CmdRunner, utils.CustomGlobber{}, cacheDir, source.RepoURL, source.Chart, source.TargetRevision, repoCredentials); err != nil {
				return err
			}
		}
	} else {
		if err := helmChartProcessor.DownloadHelmChart(t.CmdRunner, utils.CustomGlobber{}, cacheDir, t.App.Spec.Source.RepoURL, t.App.Spec.Source.Chart, t.App.Spec.Source.TargetRevision, repoCredentials); err != nil {
			return err
		}
	}

	return nil
}

// extractCharts extracts the content of the downloaded Helm charts.
// For applications with multiple sources, each chart is extracted separately.
// For single-source applications, only the corresponding chart is extracted.
// If an error occurs during extraction, the program is terminated.
func (t *Target) extractCharts(helmChartProcessor interfaces.HelmChartsProcessor) error {
	// We have a separate function for this and not using helm to extract the content of the chart
	// because we don't want to re-download the chart if the TargetRevision is the same
	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			err := helmChartProcessor.ExtractHelmChart(t.CmdRunner, utils.CustomGlobber{}, source.Chart, source.TargetRevision, fmt.Sprintf("%s/%s", cacheDir, source.RepoURL), tmpDir, t.Type)
			if err != nil {
				return err
			}
		}
	} else {
		err := helmChartProcessor.ExtractHelmChart(t.CmdRunner, utils.CustomGlobber{}, t.App.Spec.Source.Chart, t.App.Spec.Source.TargetRevision, fmt.Sprintf("%s/%s", cacheDir, t.App.Spec.Source.RepoURL), tmpDir, t.Type)
		if err != nil {
			return err
		}
	}
	return nil
}

// renderAppSources uses Helm to render chart templates for the application's sources.
// If the Helm specification provides a release name, it is used; otherwise, the application's metadata name is used.
// If the application has multiple sources, each source is rendered individually.
// If the application has only one source, the source is rendered accordingly.
// If there's any error during rendering, it will lead to a fatal error, and the program will exit.
func (t *Target) renderAppSources(helmChartProcessor interfaces.HelmChartsProcessor) error {
	var releaseName string

	// We are providing release name to the helm template command to cover some corner cases
	// when the chart is using the release name in the templates
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
			if err := helmChartProcessor.RenderAppSource(&utils.RealCmdRunner{}, releaseName, source.Chart, source.TargetRevision, tmpDir, t.Type, t.App.Spec.Destination.Namespace); err != nil {
				return err
			}
		}
		return nil
	}

	if err := helmChartProcessor.RenderAppSource(&utils.RealCmdRunner{}, releaseName, t.App.Spec.Source.Chart, t.App.Spec.Source.TargetRevision, tmpDir, t.Type, t.App.Spec.Destination.Namespace); err != nil {
		return err
	}

	return nil
}
