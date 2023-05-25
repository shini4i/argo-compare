package models

import (
	"errors"
	"fmt"
)

var (
	NotApplicationError              = errors.New("file is not an Application")
	UnsupportedAppConfigurationError = errors.New("unsupported Application configuration")
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

func (app *Application) Validate() error {
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
