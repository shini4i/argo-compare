package utils

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/shini4i/argo-compare/internal/helpers"
	"gopkg.in/yaml.v3"

	"github.com/op/go-logging"

	interfaces "github.com/shini4i/argo-compare/cmd/argo-compare/interfaces"
	"github.com/shini4i/argo-compare/internal/models"
)

var (
	cyan                  = color.New(color.FgCyan, color.Bold).SprintFunc()
	FailedToDownloadChart = errors.New("failed to download chart")
)

type RealHelmChartProcessor struct {
	Log *logging.Logger
}

// GenerateValuesFile creates a Helm values file for a given chart in a specified directory.
// It takes a chart name, a temporary directory for storing the file, the target type categorizing the application,
// and the content of the values file in string format.
// The function first attempts to create the file. If an error occurs, it terminates the program.
// Next, it writes the values string to the file. If an error occurs during this process, the program is also terminated.
func (g RealHelmChartProcessor) GenerateValuesFile(chartName, tmpDir, targetType, values string, valuesObject map[string]interface{}) error {
	yamlFile, err := os.Create(fmt.Sprintf("%s/%s-values-%s.yaml", tmpDir, chartName, targetType))
	if err != nil {
		return err
	}

	defer func(yamlFile *os.File) {
		err := yamlFile.Close()
		if err != nil {
			g.Log.Fatal(err)
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
// The function is designed to handle potential errors during directory creation, globbing, and Helm chart downloading.
// Any critical error during these operations terminates the program.
func (g RealHelmChartProcessor) DownloadHelmChart(cmdRunner interfaces.CmdRunner, globber interfaces.Globber, cacheDir, repoUrl, chartName, targetRevision string, repoCredentials []models.RepoCredentials) error {
	chartLocation := fmt.Sprintf("%s/%s", cacheDir, repoUrl)

	if err := os.MkdirAll(chartLocation, 0750); err != nil {
		g.Log.Fatal(err)
	}

	// A bit hacky, but we need to support cases when helm chart tgz filename does not follow the standard naming convention
	// For example, sonarqube-4.0.0+315.tgz
	chartFileName, err := globber.Glob(fmt.Sprintf("%s/%s-%s*.tgz", chartLocation, chartName, targetRevision))
	if err != nil {
		g.Log.Fatal(err)
	}

	if len(chartFileName) == 0 {
		username, password := helpers.FindHelmRepoCredentials(repoUrl, repoCredentials)

		g.Log.Debugf("Downloading version [%s] of [%s] chart...",
			cyan(targetRevision),
			cyan(chartName))

		// we assume that if repoUrl does not have protocol, it is an OCI helm registry
		// hence we mutate the content on chartName and remove content of repoUrl
		if !strings.Contains(repoUrl, "http") {
			chartName = fmt.Sprintf("oci://%s/%s", repoUrl, chartName)
			repoUrl = ""
		}

		stdout, stderr, err := cmdRunner.Run("helm",
			"pull",
			"--destination", chartLocation,
			"--username", username,
			"--password", password,
			"--repo", repoUrl,
			chartName,
			"--version", targetRevision)

		if len(stdout) > 0 {
			g.Log.Info(stdout)
		}

		if len(stderr) > 0 {
			g.Log.Error(stderr)
		}

		if err != nil {
			return FailedToDownloadChart
		}
	} else {
		g.Log.Debugf("Version [%s] of [%s] chart is present in the cache...",
			cyan(targetRevision),
			cyan(chartName))
	}

	return nil
}

// ExtractHelmChart extracts a specific version of a Helm chart from a cache directory
// and stores it in a temporary directory. The function uses the provided CmdRunner to
// execute the tar command and Globber to match the chart file in the cache.
// If multiple files matching the pattern are found, an error is returned.
// The function logs any output (standard or error) from the tar command.
// Any critical error during these operations, like directory creation or extraction failure, terminates the program.
func (g RealHelmChartProcessor) ExtractHelmChart(cmdRunner interfaces.CmdRunner, globber interfaces.Globber, chartName, chartVersion, chartLocation, tmpDir, targetType string) error {
	g.Log.Debugf("Extracting [%s] chart version [%s] to %s/charts/%s...",
		cyan(chartName),
		cyan(chartVersion),
		tmpDir, targetType)

	path := fmt.Sprintf("%s/charts/%s/%s", tmpDir, targetType, chartName)
	if err := os.MkdirAll(path, 0750); err != nil {
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
		g.Log.Info(stdout)
	}

	if len(stderr) > 0 {
		g.Log.Error(stderr)
	}

	if err != nil {
		return err
	}

	return nil
}

// RenderAppSource uses the Helm CLI to render the templates of a given chart.
// It takes a cmdRunner to run the Helm command, a release name for the Helm release,
// the chart name and version, a temporary directory for storing intermediate files,
// and the target type which categorizes the application.
// The function constructs the Helm command with the provided arguments, runs it, and checks for any errors.
// If there are any errors, it returns them. Otherwise, it returns nil.
func (g RealHelmChartProcessor) RenderAppSource(cmdRunner interfaces.CmdRunner, releaseName, chartName, chartVersion, tmpDir, targetType string) error {
	g.Log.Debugf("Rendering [%s] chart's version [%s] templates using release name [%s]",
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
		g.Log.Error(stderr)
		return err
	}

	return nil
}
