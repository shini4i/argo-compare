// Package models defines the data structures representing ArgoCD Application
// manifests and related configuration parsed from YAML files.
package models

import (
	"errors"
	"fmt"
)

// KindApplication is the manifest kind argo-compare operates on.
const KindApplication = "Application"

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
// Ref names the source so a sibling can address its files as "$<Ref>/path" in
// helm.valueFiles; a Ref without Path contributes no resources of its own.
type Source struct {
	RepoURL        string     `yaml:"repoURL"`
	Chart          string     `yaml:"chart,omitempty"`
	TargetRevision string     `yaml:"targetRevision"`
	Path           string     `yaml:"path,omitempty"`
	Ref            string     `yaml:"ref,omitempty"`
	Helm           HelmSource `yaml:"helm"`
}

// IsRef reports whether the source is addressable by siblings as "$<Ref>/...".
func (s *Source) IsRef() bool {
	return s != nil && s.Ref != ""
}

// Renderable reports whether the source produces manifests of its own, which
// requires either a registry chart or a Git path.
func (s *Source) Renderable() bool {
	return s != nil && (s.Chart != "" || s.Path != "")
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

// validateHelmSources checks the shape of every source and requires that at
// least one of them renders. An Application whose sources are all values-only
// refs would produce an empty diff that reads as "no changes" rather than as
// the misconfiguration it is.
func (app *Application) validateHelmSources() error {
	if len(app.Spec.Sources) > 0 {
		for _, source := range app.Spec.Sources {
			if err := validateSourceShape(source); err != nil {
				return err
			}
		}
		for _, source := range app.Spec.Sources {
			if source.Renderable() {
				return nil
			}
		}
		return fmt.Errorf("%w: no source renders a chart; every source is a values-only ref", ErrUnsupportedAppConfiguration)
	}

	if app.Spec.Source == nil {
		return ErrUnsupportedAppConfiguration
	}
	if err := validateSourceShape(app.Spec.Source); err != nil {
		return err
	}
	if !app.Spec.Source.Renderable() {
		return fmt.Errorf("%w: no source renders a chart; every source is a values-only ref", ErrUnsupportedAppConfiguration)
	}
	return nil
}

// validateSourceShape ensures the supplied Source declares at most one chart
// kind, and that a values-only source names a ref. Each failure mode wraps
// ErrUnsupportedAppConfiguration with a specific message so users see *why*
// their manifest was rejected without losing the sentinel for errors.Is.
func validateSourceShape(source *Source) error {
	if source == nil {
		return fmt.Errorf("%w: source is nil", ErrUnsupportedAppConfiguration)
	}
	hasChart := len(source.Chart) > 0
	hasPath := len(source.Path) > 0
	switch {
	case hasChart && hasPath:
		return fmt.Errorf("%w: source has both chart=%q and path=%q set; only one is allowed", ErrUnsupportedAppConfiguration, source.Chart, source.Path)
	// ArgoCD forbids the pairing: Helm charts are not supported as value file sources.
	case hasChart && source.IsRef():
		return fmt.Errorf("%w: source has both chart=%q and ref=%q set; a ref source must be a Git repository", ErrUnsupportedAppConfiguration, source.Chart, source.Ref)
	case !hasChart && !hasPath && !source.IsRef():
		return fmt.Errorf("%w: source has neither chart nor path set", ErrUnsupportedAppConfiguration)
	}
	return nil
}

// Validate performs validation checks on the Application struct.
// It checks for the following:
//   - If the Application struct is empty, returns ErrEmptyFile.
//   - If both the 'source' and 'sources' fields are set at the same time, returns an error.
//   - If the kind of the application is not "Application", returns ErrNotApplication.
//   - For each source, ensures it declares at most one of 'chart' (Helm-registry)
//     or 'path' (Git path), and that at least one source renders.
//   - Sets the 'MultiSource' field to true if sources are specified.
//   - Returns nil if all validation checks pass.
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

	if app.Kind != KindApplication {
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
