// Package models defines the data structures representing ArgoCD Application
// manifests and related configuration parsed from YAML files.
package models

import (
	"errors"
	"fmt"
)

var (
	// ErrNotApplication signals that the provided manifest is not an ArgoCD Application.
	ErrNotApplication = errors.New("file is not an Application")
	// ErrUnsupportedAppConfiguration identifies manifests that use unsupported configuration.
	ErrUnsupportedAppConfiguration = errors.New("unsupported Application configuration")
	// ErrEmptyFile indicates that the manifest file contained no data.
	ErrEmptyFile = errors.New("file is empty")
)

// Application models the subset of ArgoCD Application fields used by the tool.
type Application struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		Source      *Source      `yaml:"source"`
		Sources     []*Source    `yaml:"sources"`
		MultiSource bool         `yaml:"-"`
		Destination *Destination `yaml:"destination"`
	} `yaml:"spec"`
}

// Destination describes where an Application should be deployed.
type Destination struct {
	Server    string `yaml:"server"`
	Namespace string `yaml:"namespace"`
}

// Source holds the chart or path information for a single Application source.
type Source struct {
	RepoURL        string `yaml:"repoURL"`
	Chart          string `yaml:"chart,omitempty"`
	TargetRevision string `yaml:"targetRevision"`
	Path           string `yaml:"path,omitempty"`
	Helm           struct {
		ReleaseName  string                 `yaml:"releaseName,omitempty"`
		Values       string                 `yaml:"values,omitempty"`
		ValueFiles   []string               `yaml:"valueFiles,omitempty"`
		ValuesObject map[string]interface{} `yaml:"valuesObject,omitempty"`
	} `yaml:"helm"`
}

// validateHelmSources checks that all sources have a non-empty chart field.
// Currently we support only helm repository based charts as a source.
func (app *Application) validateHelmSources() error {
	if len(app.Spec.Sources) > 0 {
		for _, source := range app.Spec.Sources {
			if len(source.Chart) == 0 {
				return ErrUnsupportedAppConfiguration
			}
		}
		return nil
	}

	if app.Spec.Source == nil || len(app.Spec.Source.Chart) == 0 {
		return ErrUnsupportedAppConfiguration
	}
	return nil
}

// Validate performs validation checks on the Application struct.
// It checks for the following:
// - If the Application struct is empty, returns ErrEmptyFile.
// - If both the 'source' and 'sources' fields are set at the same time, returns an error.
// - If the kind of the application is not "Application", returns ErrNotApplication.
// - If the application specifies sources, ensures that each source has a non-empty 'chart' field.
// - Sets the 'MultiSource' field to true if sources are specified.
// - Returns nil if all validation checks pass.
func (app *Application) Validate() error {
	if app == nil {
		return ErrEmptyFile
	}

	// Check if the required fields 'Kind', 'Metadata.Name', and 'Metadata.Namespace' are set.
	if app.Kind == "" && app.Metadata.Name == "" && app.Metadata.Namespace == "" {
		return ErrEmptyFile
	}

	if app.Spec.Source != nil && len(app.Spec.Sources) > 0 {
		return fmt.Errorf("both 'source' and 'sources' fields cannot be set at the same time")
	}

	if app.Kind != "Application" {
		return ErrNotApplication
	}

	if err := app.validateHelmSources(); err != nil {
		return err
	}

	if app.Spec.Sources != nil {
		app.Spec.MultiSource = true
	}

	return nil
}
