package models

import (
	"errors"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestApplication_Validate(t *testing.T) {
	// Test case 1: Empty application
	app := &Application{}
	err := app.Validate()
	assert.True(t, errors.Is(err, EmptyFileError), "Expected validation error: %v, but got: %v", EmptyFileError, err)

	// Test case 2: Application with invalid kind
	app = &Application{
		Kind: "InvalidKind",
	}
	err = app.Validate()
	assert.True(t, errors.Is(err, NotApplicationError), "Expected validation error: %v, but got: %v", NotApplicationError, err)
}

func TestIsEmpty(t *testing.T) {
	// Test case 1: Empty application
	app := &Application{}
	assert.True(t, isEmpty(app), "Expected isEmpty to return true for an empty application")

	// Test case 2: Non-empty application
	app = &Application{
		Kind: "Application",
		Metadata: struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		}{
			Name:      "example",
			Namespace: "default",
		},
		Spec: struct {
			Source      *Source   `yaml:"source"`
			Sources     []*Source `yaml:"sources"`
			MultiSource bool      `yaml:"-"`
		}{
			Source: &Source{
				RepoURL:        "https://example.com/repo",
				Chart:          "example-chart",
				TargetRevision: "main",
				Path:           "/path/to/app",
				Helm: struct {
					ReleaseName string   `yaml:"releaseName,omitempty"`
					Values      string   `yaml:"values,omitempty"`
					ValueFiles  []string `yaml:"valueFiles,omitempty"`
				}{
					ReleaseName: "example-release",
					Values:      "values.yaml",
					ValueFiles:  []string{"values1.yaml", "values2.yaml"},
				},
			},
		},
	}
	assert.False(t, isEmpty(app), "Expected isEmpty to return false for a non-empty application")
}
