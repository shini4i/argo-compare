package appset

import (
	"fmt"

	"github.com/shini4i/argo-compare/internal/models"
	"gopkg.in/yaml.v3"
)

// Expand renders every Application an ApplicationSet generates, in generator
// and element order. Only manifests accepted by ApplicationSet.Validate can be
// expanded; anything else is returned as an error rather than a partial result.
func Expand(appSet *models.ApplicationSet) ([]models.Application, error) {
	if err := appSet.Validate(); err != nil {
		return nil, err
	}

	text, err := yaml.Marshal(&appSet.Spec.Template)
	if err != nil {
		return nil, fmt.Errorf("marshal spec.template: %w", err)
	}

	var apps []models.Application
	seen := make(map[string]bool)

	for i, params := range collectParameters(appSet) {
		app, err := renderApplication(string(text), params, appSet.Spec.GoTemplateOptions)
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
func collectParameters(appSet *models.ApplicationSet) []map[string]any {
	var params []map[string]any

	for _, generator := range appSet.Spec.Generators {
		if generator.List != nil {
			params = append(params, generator.List.Elements...)
		}
	}

	return params
}

// renderApplication turns one parameter set into a validated Application.
// Kind is set here because spec.template carries only metadata and spec.
func renderApplication(text string, params map[string]any, options []string) (models.Application, error) {
	rendered, err := render(text, params, options)
	if err != nil {
		return models.Application{}, err
	}

	var app models.Application
	if err := yaml.Unmarshal([]byte(rendered), &app); err != nil {
		return models.Application{}, fmt.Errorf("decode generated Application: %w", err)
	}

	app.Kind = models.KindApplication
	if err := app.Validate(); err != nil {
		return models.Application{}, fmt.Errorf("generated Application %q: %w", app.Metadata.Name, err)
	}

	return app, nil
}
