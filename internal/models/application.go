package models

import (
	"errors"
	"fmt"
)

var (
	NotApplicationError              = errors.New("file is not an Application")
	UnsupportedAppConfigurationError = errors.New("unsupported Application configuration")
	EmptyFileError                   = errors.New("file is empty")
)

type Application struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		Source      *Source   `yaml:"source"`
		Sources     []*Source `yaml:"sources"`
		MultiSource bool      `yaml:"-"`
	} `yaml:"spec"`
}

type Source struct {
	RepoURL        string `yaml:"repoURL"`
	Chart          string `yaml:"chart,omitempty"`
	TargetRevision string `yaml:"targetRevision"`
	Path           string `yaml:"path,omitempty"`
	Helm           struct {
		ReleaseName string   `yaml:"releaseName,omitempty"`
		Values      string   `yaml:"values,omitempty"`
		ValueFiles  []string `yaml:"valueFiles,omitempty"`
	} `yaml:"helm"`
}

// Validate performs validation checks on the Application struct.
// It checks for the following:
// - If the Application struct is empty, returns EmptyFileError.
// - If both the 'source' and 'sources' fields are set at the same time, returns an error.
// - If the kind of the application is not "Application", returns NotApplicationError.
// - If the application specifies sources, ensures that each source has a non-empty 'chart' field.
// - Sets the 'MultiSource' field to true if sources are specified.
// - Returns nil if all validation checks pass.
func (app *Application) Validate() error {
	if isEmpty(app) {
		return EmptyFileError
	}

	if app.Spec.Source != nil && len(app.Spec.Sources) > 0 {
		return fmt.Errorf("both 'source' and 'sources' fields cannot be set at the same time")
	}

	if app.Kind != "Application" {
		return NotApplicationError
	}

	// currently we support only helm repository based charts as a source
	if len(app.Spec.Sources) != 0 {
		for _, source := range app.Spec.Sources {
			if len(source.Chart) == 0 {
				return UnsupportedAppConfigurationError
			}
		}
	} else {
		if len(app.Spec.Source.Chart) == 0 {
			return UnsupportedAppConfigurationError
		}
	}

	if app.Spec.Sources != nil {
		app.Spec.MultiSource = true
	}

	return nil
}

// Check if the Application structure is empty.
func isEmpty(app *Application) bool {
	return app.Kind == "" &&
		app.Metadata.Name == "" &&
		app.Metadata.Namespace == "" &&
		app.Spec.Source == nil &&
		len(app.Spec.Sources) == 0 &&
		!app.Spec.MultiSource
}
