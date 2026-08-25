package appset

import (
	"fmt"

	"github.com/shini4i/argo-compare/internal/models"
	"gopkg.in/yaml.v3"
)

// Expand renders every Application an ApplicationSet generates, in generator
// and element order. Manifests Validate rejects are returned as an error, never
// as a partial result. lister supplies the directories a git generator matches
// against, and is called only when the manifest declares one.
func Expand(appSet *models.ApplicationSet, lister DirectoryLister) ([]models.Application, error) {
	if err := appSet.Validate(); err != nil {
		return nil, err
	}

	parameters, err := collectParameters(appSet, lister)
	if err != nil {
		return nil, err
	}

	var apps []models.Application
	seen := make(map[string]bool)

	for i, params := range parameters {
		app, err := renderApplication(&appSet.Spec.Template, params, appSet.Spec.GoTemplateOptions)
		if err != nil {
			return nil, fmt.Errorf("generator element %d: %w", i, err)
		}

		if seen[app.Metadata.Name] {
			return nil, fmt.Errorf("ApplicationSet %q generates duplicate Application name %q",
				appSet.Metadata.Name, app.Metadata.Name)
		}
		seen[app.Metadata.Name] = true

		apps = append(apps, app)
	}

	return apps, nil
}

// collectParameters flattens every generator into the parameter sets that each
// produce one Application. Directories are listed once and shared by every git
// generator, since they all read the same tree.
func collectParameters(appSet *models.ApplicationSet, lister DirectoryLister) ([]map[string]any, error) {
	var (
		params []map[string]any
		dirs   []string
		listed bool
	)

	for _, generator := range appSet.Spec.Generators {
		if generator.List != nil {
			params = append(params, generator.List.Elements...)
		}

		if generator.Git == nil {
			continue
		}

		if lister == nil {
			return nil, fmt.Errorf("%w: git generators need a repository to list", models.ErrUnsupportedAppConfiguration)
		}

		if !listed {
			var err error
			if dirs, err = lister(); err != nil {
				return nil, fmt.Errorf("list directories for git generator: %w", err)
			}
			listed = true
		}

		for _, dir := range matchDirectories(dirs, generator.Git.Directories) {
			params = append(params, directoryParams(dir))
		}
	}

	return params, nil
}

// renderApplication turns one parameter set into a validated Application. The
// template is decoded first and each field rendered afterwards, so a value's
// quotes, newlines or indentation cannot alter the manifest's own structure.
// Kind is set here because spec.template carries only metadata and spec.
func renderApplication(template *yaml.Node, params map[string]any, options []string) (models.Application, error) {
	var app models.Application
	if err := template.Decode(&app); err != nil {
		return models.Application{}, fmt.Errorf("decode spec.template: %w", err)
	}

	fields := renderer{params: params, options: options}
	if err := fields.application(&app); err != nil {
		return models.Application{}, err
	}

	app.Kind = models.KindApplication
	if err := app.Validate(); err != nil {
		return models.Application{}, fmt.Errorf("generated Application %q: %w", app.Metadata.Name, err)
	}

	return app, nil
}
