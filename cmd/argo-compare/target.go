package main

import (
	"errors"
	"fmt"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"gopkg.in/yaml.v3"
	"os"
	"strings"

	"github.com/shini4i/argo-compare/internal/helpers"
	"github.com/shini4i/argo-compare/internal/models"
)

type Target struct {
	CmdRunner  utils.CmdRunner
	FileReader utils.FileReader
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
		if gitRepoRoot, err := helpers.GetGitRepoRoot(&utils.RealCmdRunner{}); err != nil {
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
func (t *Target) generateValuesFiles(vg utils.HelmValuesGenerator) error {
	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			if err := vg.GenerateValuesFile(source.Chart, tmpDir, t.Type, source.Helm.Values); err != nil {
				return err
			}
		}
	} else {
		if err := vg.GenerateValuesFile(t.App.Spec.Source.Chart, tmpDir, t.Type, t.App.Spec.Source.Helm.Values); err != nil {
			return err
		}
	}
	return nil
}

// ensureHelmCharts downloads Helm charts for the application's sources.
// If the application uses multiple sources, each chart is downloaded separately.
// If the application has a single source, only the respective chart is downloaded.
// In case of any error during download, the error is returned immediately.
func (t *Target) ensureHelmCharts() error {
	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			if err := downloadHelmChart(t.CmdRunner, utils.CustomGlobber{}, cacheDir, source.RepoURL, source.Chart, source.TargetRevision); err != nil {
				return err
			}
		}
	} else {
		if err := downloadHelmChart(t.CmdRunner, utils.CustomGlobber{}, cacheDir, t.App.Spec.Source.RepoURL, t.App.Spec.Source.Chart, t.App.Spec.Source.TargetRevision); err != nil {
			return err
		}
	}

	return nil
}

// extractCharts extracts the content of the downloaded Helm charts.
// For applications with multiple sources, each chart is extracted separately.
// For single-source applications, only the corresponding chart is extracted.
// If an error occurs during extraction, the program is terminated.
func (t *Target) extractCharts() {
	// We have a separate function for this and not using helm to extract the content of the chart
	// because we don't want to re-download the chart if the TargetRevision is the same
	if t.App.Spec.MultiSource {
		for _, source := range t.App.Spec.Sources {
			err := extractHelmChart(t.CmdRunner, utils.CustomGlobber{}, source.Chart, source.TargetRevision, fmt.Sprintf("%s/%s", cacheDir, source.RepoURL), tmpDir, t.Type)
			if err != nil {
				log.Fatal(err)
			}
		}
	} else {
		err := extractHelmChart(t.CmdRunner, utils.CustomGlobber{}, t.App.Spec.Source.Chart, t.App.Spec.Source.TargetRevision, fmt.Sprintf("%s/%s", cacheDir, t.App.Spec.Source.RepoURL), tmpDir, t.Type)
		if err != nil {
			log.Fatal(err)
		}
	}
}

// renderAppSources uses Helm to render chart templates for the application's sources.
// If the Helm specification provides a release name, it is used; otherwise, the application's metadata name is used.
// If the application has multiple sources, each source is rendered individually.
// If the application has only one source, the source is rendered accordingly.
// If there's any error during rendering, it will lead to a fatal error, and the program will exit.
func (t *Target) renderAppSources() {
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
			if err := renderAppSource(&utils.RealCmdRunner{}, releaseName, source.Chart, source.TargetRevision, tmpDir, t.Type); err != nil {
				log.Fatal(err)
			}
		}
		return
	}

	if err := renderAppSource(&utils.RealCmdRunner{}, releaseName, t.App.Spec.Source.Chart, t.App.Spec.Source.TargetRevision, tmpDir, t.Type); err != nil {
		log.Fatal(err)
	}
}

// renderAppSource uses the Helm CLI to render the templates of a given chart.
// It takes a cmdRunner to run the Helm command, a release name for the Helm release,
// the chart name and version, a temporary directory for storing intermediate files,
// and the target type which categorizes the application.
// The function constructs the Helm command with the provided arguments, runs it, and checks for any errors.
// If there are any errors, it returns them. Otherwise, it returns nil.
func renderAppSource(cmdRunner utils.CmdRunner, releaseName, chartName, chartVersion, tmpDir, targetType string) error {
	log.Debugf("Rendering [%s] chart's version [%s] templates using release name [%s]",
		cyan(chartName),
		cyan(chartVersion),
		cyan(releaseName))

	_, stderr, err := cmdRunner.Run(
		"helm",
		"template",
		"--release-name", releaseName,
		fmt.Sprintf("%s/charts/%s/%s", tmpDir, targetType, chartName),
		"--output-dir", fmt.Sprintf("%s/templates/%s", tmpDir, targetType),
		"--values", fmt.Sprintf("%s/charts/%s/%s/values.yaml", tmpDir, targetType, chartName),
		"--values", fmt.Sprintf("%s/%s-values-%s.yaml", tmpDir, chartName, targetType),
	)

	if err != nil {
		log.Error(stderr)
		return err
	}

	return nil
}

// downloadHelmChart fetches a specified version of a Helm chart from a given repository URL and
// stores it in a cache directory. The function leverages the provided CmdRunner to execute
// the helm pull command and Globber to deal with possible non-standard chart naming.
// If the chart is already present in the cache, the function just logs the information and doesn't download it again.
// The function is designed to handle potential errors during directory creation, globbing, and Helm chart downloading.
// Any critical error during these operations terminates the program.
func downloadHelmChart(cmdRunner utils.CmdRunner, globber utils.Globber, cacheDir, repoUrl, chartName, targetRevision string) error {
	chartLocation := fmt.Sprintf("%s/%s", cacheDir, repoUrl)

	if err := os.MkdirAll(chartLocation, os.ModePerm); err != nil {
		log.Fatal(err)
	}

	// A bit hacky, but we need to support cases when helm chart tgz filename does not follow the standard naming convention
	// For example, sonarqube-4.0.0+315.tgz
	chartFileName, err := globber.Glob(fmt.Sprintf("%s/%s-%s*.tgz", chartLocation, chartName, targetRevision))
	if err != nil {
		log.Fatal(err)
	}

	if len(chartFileName) == 0 {
		username, password := findHelmRepoCredentials(repoUrl, repoCredentials)

		log.Debugf("Downloading version [%s] of [%s] chart...",
			cyan(targetRevision),
			cyan(chartName))

		stdout, stderr, err := cmdRunner.Run("helm",
			"pull",
			"--destination", chartLocation,
			"--username", username,
			"--password", password,
			"--repo", repoUrl,
			chartName,
			"--version", targetRevision)

		if len(stdout) > 0 {
			log.Info(stdout)
		}

		if len(stderr) > 0 {
			log.Error(stderr)
		}

		if err != nil {
			return failedToDownloadChart
		}
	} else {
		log.Debugf("Version [%s] of [%s] chart is present in the cache...",
			cyan(targetRevision),
			cyan(chartName))
	}

	return nil
}

// extractHelmChart extracts a specific version of a Helm chart from a cache directory
// and stores it in a temporary directory. The function uses the provided CmdRunner to
// execute the tar command and Globber to match the chart file in the cache.
// If multiple files matching the pattern are found, an error is returned.
// The function logs any output (standard or error) from the tar command.
// Any critical error during these operations, like directory creation or extraction failure, terminates the program.
func extractHelmChart(cmdRunner utils.CmdRunner, globber utils.Globber, chartName, chartVersion, chartLocation, tmpDir, targetType string) error {
	log.Debugf("Extracting [%s] chart version [%s] to %s/charts/%s...",
		cyan(chartName),
		cyan(chartVersion),
		tmpDir, targetType)

	path := fmt.Sprintf("%s/charts/%s/%s", tmpDir, targetType, chartName)
	if err := os.MkdirAll(path, os.ModePerm); err != nil {
		return err
	}

	searchPattern := fmt.Sprintf("%s/%s-%s*.tgz",
		chartLocation,
		chartName,
		chartVersion)

	chartFileName, err := globber.Glob(searchPattern)
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

	stdout, stderr, err := cmdRunner.Run("tar",
		"xf",
		chartFileName[0],
		"-C", fmt.Sprintf("%s/charts/%s", tmpDir, targetType),
	)

	if len(stdout) > 0 {
		log.Info(stdout)
	}

	if len(stderr) > 0 {
		log.Error(stderr)
	}

	if err != nil {
		return err
	}

	return nil
}

// findHelmRepoCredentials scans the provided array of RepoCredentials for a match to the
// provided repository URL, and returns the associated username and password.
// If no matching credentials are found, it returns two empty strings.
func findHelmRepoCredentials(url string, credentials []RepoCredentials) (string, string) {
	for _, repoCred := range credentials {
		if repoCred.Url == url {
			return repoCred.Username, repoCred.Password
		}
	}
	return "", ""
}
