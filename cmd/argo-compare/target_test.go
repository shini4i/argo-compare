package main

import (
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"os"
	"testing"
)

func TestGenerateValuesFile(t *testing.T) {
	// Create a temporary directory
	tmpDir, err := ioutil.TempDir("../../testdata/dynamic", "test-")
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

	// Call the function to test
	generateValuesFile(chartName, tmpDir, targetType, values)

	// Read the generated file
	generatedValues, err := os.ReadFile(tmpDir + "/" + chartName + "-values-" + targetType + ".yaml")
	if err != nil {
		t.Fatal(err)
	}

	// Verify the contents
	assert.Equal(t, values, string(generatedValues))
}
