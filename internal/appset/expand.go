package appset

import (
	"fmt"

	"github.com/shini4i/argo-compare/internal/models"
	"gopkg.in/yaml.v3"
)

// Expand renders every Application an ApplicationSet generates, in generator
// and element order. Manifests Validate rejects are returned as an error, never
// as a partial result. tree supplies what a git generator reads, and is
// consulted only when the manifest declares one.
func Expand(appSet *models.ApplicationSet, tree Tree) ([]models.Application, error) {
	if err := appSet.Validate(); err != nil {
		return nil, err
	}

	parameters, err := collectParameters(appSet, tree)
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
// produce one Application.
func collectParameters(appSet *models.ApplicationSet, tree Tree) ([]map[string]any, error) {
	collector := parameterCollector{tree: tree, options: appSet.Spec.GoTemplateOptions}

	for _, generator := range appSet.Spec.Generators {
		if err := collector.collect(generator); err != nil {
			return nil, err
		}
	}

	return collector.params, nil
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
