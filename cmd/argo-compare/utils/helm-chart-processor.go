package utils

import (
	"errors"
	"fmt"
	"github.com/fatih/color"
	"github.com/shini4i/argo-compare/internal/helpers"
	"os"

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
func (g RealHelmChartProcessor) GenerateValuesFile(chartName, tmpDir, targetType, values string) error {
	yamlFile, err := os.Create(fmt.Sprintf("%s/%s-values-%s.yaml", tmpDir, chartName, targetType))
	if err != nil {
		return err
	}

	if _, err := yamlFile.WriteString(values); err != nil {
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

	if err := os.MkdirAll(chartLocation, os.ModePerm); err != nil {
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
