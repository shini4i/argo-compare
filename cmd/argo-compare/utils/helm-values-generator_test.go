package utils

import (
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

const (
	testsDir = "../../../testdata/disposable"
)

func TestGenerateValuesFile(t *testing.T) {
	generator := RealHelmValuesGenerator{}

	// Create a temporary directory
	tmpDir, err := os.MkdirTemp(testsDir, "test-")
	if err != nil {
		t.Fatal(err)
	}

	// Delete the directory after the test finishes
	defer func(path string) {
		err := os.RemoveAll(path)
		if err != nil {
			t.Fatal(err)
		}
	}(tmpDir)

	chartName := "ingress-nginx"
	targetType := "src"
	values := "fullnameOverride: ingress-nginx\ncontroller:\n  kind: DaemonSet\n  service:\n    externalTrafficPolicy: Local\n    annotations:\n      fancyAnnotation: false\n"

	// Test case 1: Everything works as expected
	err = generator.GenerateValuesFile(chartName, tmpDir, targetType, values)
	assert.NoError(t, err, "expected no error, got %v", err)

	// Read the generated file
	generatedValues, err := os.ReadFile(tmpDir + "/" + chartName + "-values-" + targetType + ".yaml")
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, values, string(generatedValues))

	// Test case 2: Error when creating the file
	err = generator.GenerateValuesFile(chartName, "/non/existing/path", targetType, values)
	assert.Error(t, err, "expected error, got nil")
}
