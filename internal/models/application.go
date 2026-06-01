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
	RepoURL        string     `yaml:"repoURL"`
	Chart          string     `yaml:"chart,omitempty"`
	TargetRevision string     `yaml:"targetRevision"`
	Path           string     `yaml:"path,omitempty"`
	Helm           HelmSource `yaml:"helm"`
}

// HelmSource mirrors the subset of ArgoCD's spec.source.helm we render with.
//
// Parameters carry spec.source.helm.parameters (the `--set` / `--set-string`
// equivalents). ArgoCD also lets these be overridden by .argocd-source[-<app>].yaml
// files committed next to the chart, which is how argo-watcher / Argo CD Image
// Updater record image bumps; see source_overrides.go for that merge.
type HelmSource struct {
	ReleaseName  string                 `yaml:"releaseName,omitempty"`
	Values       string                 `yaml:"values,omitempty"`
	ValueFiles   []string               `yaml:"valueFiles,omitempty"`
	ValuesObject map[string]interface{} `yaml:"valuesObject,omitempty"`
	Parameters   []HelmParameter        `yaml:"parameters,omitempty"`
}

// HelmParameter is a single spec.source.helm.parameters entry. ForceString
// selects `helm template --set-string` over `--set`, matching ArgoCD: it
// preserves ambiguously-typed values (e.g. a numeric image tag) as strings.
// ForceString defaults to false when omitted.
type HelmParameter struct {
	Name        string `yaml:"name"`
	Value       string `yaml:"value"`
	ForceString bool   `yaml:"forceString,omitempty"`
}

// validateHelmSources checks that every source declares exactly one chart kind:
// either a Helm-registry chart (Source.Chart) or a Git path (Source.Path).
// Sources with neither set, or with both set, are rejected with
// ErrUnsupportedAppConfiguration. A nil Source is also rejected.
func (app *Application) validateHelmSources() error {
	if len(app.Spec.Sources) > 0 {
		for _, source := range app.Spec.Sources {
			if err := validateSourceShape(source); err != nil {
				return err
			}
		}
		return nil
	}

	if app.Spec.Source == nil {
		return ErrUnsupportedAppConfiguration
	}
	return validateSourceShape(app.Spec.Source)
}

// validateSourceShape ensures the supplied Source declares exactly one of
// Chart or Path. Each failure mode wraps ErrUnsupportedAppConfiguration with
// a specific message so users see *why* their manifest was rejected without
// losing the sentinel for errors.Is checks.
func validateSourceShape(source *Source) error {
	if source == nil {
		return fmt.Errorf("%w: source is nil", ErrUnsupportedAppConfiguration)
	}
	hasChart := len(source.Chart) > 0
	hasPath := len(source.Path) > 0
	switch {
	case hasChart && hasPath:
		return fmt.Errorf("%w: source has both chart=%q and path=%q set; only one is allowed", ErrUnsupportedAppConfiguration, source.Chart, source.Path)
	case !hasChart && !hasPath:
		return fmt.Errorf("%w: source has neither chart nor path set", ErrUnsupportedAppConfiguration)
	}
	return nil
}

// Validate performs validation checks on the Application struct.
// It checks for the following:
// - If the Application struct is empty, returns ErrEmptyFile.
// - If both the 'source' and 'sources' fields are set at the same time, returns an error.
// - If the kind of the application is not "Application", returns ErrNotApplication.
// - For each source, ensures it declares exactly one of 'chart' (Helm-registry)
//   or 'path' (Git path); both empty or both set yields ErrUnsupportedAppConfiguration.
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
