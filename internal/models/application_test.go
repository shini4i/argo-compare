package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplication_Validate(t *testing.T) {
	// Test case 1: Empty application
	app := &Application{}
	err := app.Validate()
	assert.ErrorIs(t, err, ErrEmptyFile, "expected ErrEmptyFile")

	// Test case 2: Application with invalid kind
	app = &Application{
		Kind: "InvalidKind",
	}
	err = app.Validate()
	assert.ErrorIs(t, err, ErrNotApplication, "expected ErrNotApplication")

	// Test case 3: Unsupported app configuration - empty chart name
	appWithEmptyChart := &Application{
		Kind: "Application",
		Spec: struct {
			Source      *Source      `yaml:"source"`
			Sources     []*Source    `yaml:"sources"`
			MultiSource bool         `yaml:"-"`
			Destination *Destination `yaml:"destination"`
		}{
			Source: &Source{
				Chart: "", // Empty chart name
			},
			Sources:     nil,
			MultiSource: false,
		},
	}
	err = appWithEmptyChart.Validate()
	assert.ErrorIs(t, err, ErrUnsupportedAppConfiguration, "expected ErrUnsupportedAppConfiguration")

	// Test case 4: Valid application with multiple sources
	appWithMultipleSources := &Application{
		Kind: "Application",
		Spec: struct {
			Source      *Source      `yaml:"source"`
			Sources     []*Source    `yaml:"sources"`
			MultiSource bool         `yaml:"-"`
			Destination *Destination `yaml:"destination"`
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
		Kind: "Application",
		Spec: struct {
			Source      *Source      `yaml:"source"`
			Sources     []*Source    `yaml:"sources"`
			MultiSource bool         `yaml:"-"`
			Destination *Destination `yaml:"destination"`
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
			Source      *Source      `yaml:"source"`
			Sources     []*Source    `yaml:"sources"`
			MultiSource bool         `yaml:"-"`
			Destination *Destination `yaml:"destination"`
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
	assert.ErrorIs(t, err, ErrUnsupportedAppConfiguration, "expected ErrUnsupportedAppConfiguration")

	// Test case 7: Nil Source and empty Sources - should not panic
	appWithNilSource := &Application{
		Kind: "Application",
		Spec: struct {
			Source      *Source      `yaml:"source"`
			Sources     []*Source    `yaml:"sources"`
			MultiSource bool         `yaml:"-"`
			Destination *Destination `yaml:"destination"`
		}{
			Source:      nil,
			Sources:     nil,
			MultiSource: false,
		},
	}
	err = appWithNilSource.Validate()
	assert.ErrorIs(t, err, ErrUnsupportedAppConfiguration, "expected ErrUnsupportedAppConfiguration for nil source")

	// Test case 8: Nil receiver - should not panic
	var nilApp *Application
	err = nilApp.Validate()
	assert.ErrorIs(t, err, ErrEmptyFile, "expected ErrEmptyFile for nil receiver")
}
