package models

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
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

	// Test case 3: Unsupported app configuration - empty chart name
	appWithEmptyChart := &Application{
		Kind: "Application",
		Spec: struct {
			Source      *Source   `yaml:"source"`
			Sources     []*Source `yaml:"sources"`
			MultiSource bool      `yaml:"-"`
		}{
			Source: &Source{
				Chart: "", // Empty chart name
			},
			Sources:     nil,
			MultiSource: false,
		},
	}
	err = appWithEmptyChart.Validate()
	assert.ErrorIs(t, err, UnsupportedAppConfigurationError, "expected UnsupportedAppConfigurationError")

	// Test case 4: Valid application with multiple sources
	appWithMultipleSources := &Application{
		Kind: "Application",
		Spec: struct {
			Source      *Source   `yaml:"source"`
			Sources     []*Source `yaml:"sources"`
			MultiSource bool      `yaml:"-"`
		}{
			Source: nil,
			Sources: []*Source{
				{
					RepoURL:        "https://chart.example.com",
					Chart:          "chart-1",
					TargetRevision: "1.0.0",
				},
				{
					RepoURL:        "https://chart.example.com",
					Chart:          "chart-2",
					TargetRevision: "2.0.0",
				},
			},
			MultiSource: false,
		},
	}
	err = appWithMultipleSources.Validate()
	assert.NoError(t, err, "expected no error")

	// Test case 5: Both 'source' and 'sources' fields are set
	appWithBothFields := &Application{
		Spec: struct {
			Source      *Source   `yaml:"source"`
			Sources     []*Source `yaml:"sources"`
			MultiSource bool      `yaml:"-"`
		}{
			Source: &Source{
				RepoURL:        "https://chart.example.com",
				Chart:          "ingress-nginx",
				TargetRevision: "3.34.0",
			},
			Sources: []*Source{
				{
					RepoURL:        "https://chart.example.com",
					Chart:          "chart-1",
					TargetRevision: "1.0.0",
				},
			},
			MultiSource: false,
		},
	}
	err = appWithBothFields.Validate()
	assert.EqualError(t, err, "both 'source' and 'sources' fields cannot be set at the same time", "expected error message")

	// Test case 6: Unsupported app configuration - empty chart name in multiple sources
	appWithMultipleSourcesUnsupported := &Application{
		Kind: "Application",
		Spec: struct {
			Source      *Source   `yaml:"source"`
			Sources     []*Source `yaml:"sources"`
			MultiSource bool      `yaml:"-"`
		}{
			Source: nil,
			Sources: []*Source{
				{
					Chart: "",
				},
				{
					Chart: "chart-2",
				},
			},
			MultiSource: false,
		},
	}
	err = appWithMultipleSourcesUnsupported.Validate()
	assert.ErrorIs(t, err, UnsupportedAppConfigurationError, "expected UnsupportedAppConfigurationError")
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
					ReleaseName  string                 `yaml:"releaseName,omitempty"`
					Values       string                 `yaml:"values,omitempty"`
					ValueFiles   []string               `yaml:"valueFiles,omitempty"`
					ValuesObject map[string]interface{} `yaml:"valuesObject,omitempty"`
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
