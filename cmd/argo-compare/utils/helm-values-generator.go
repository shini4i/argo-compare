package utils

import (
	"fmt"
	"os"
)

type RealHelmValuesGenerator struct{}

// GenerateValuesFile creates a Helm values file for a given chart in a specified directory.
// It takes a chart name, a temporary directory for storing the file, the target type categorizing the application,
// and the content of the values file in string format.
// The function first attempts to create the file. If an error occurs, it terminates the program.
// Next, it writes the values string to the file. If an error occurs during this process, the program is also terminated.
func (g RealHelmValuesGenerator) GenerateValuesFile(chartName, tmpDir, targetType, values string) error {
	yamlFile, err := os.Create(fmt.Sprintf("%s/%s-values-%s.yaml", tmpDir, chartName, targetType))
	if err != nil {
		return err
	}

	if _, err := yamlFile.WriteString(values); err != nil {
		return err
	}

	return nil
}
