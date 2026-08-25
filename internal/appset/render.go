package appset

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shini4i/argo-compare/internal/models"
)

// actionMarker opens a Go template action. A field without one is returned
// untouched, so an ordinary value never reaches the template engine.
const actionMarker = "{{"

// renderer substitutes parameters into every string an Application carries.
// Rendering field by field rather than over the whole manifest keeps a value's
// content out of the document's structure: indentation, quotes and newlines in
// a rendered value cannot change how the manifest itself parses.
type renderer struct {
	params  map[string]any
	options []string
	err     error
}

// text renders one field, keeping the first failure and leaving later fields
// untouched so the reported error is the one that actually happened.
func (r *renderer) text(value string) string {
	if r.err != nil || !strings.Contains(value, actionMarker) {
		return value
	}

	rendered, err := render(value, r.params, r.options)
	if err != nil {
		r.err = err
		return value
	}

	return rendered
}

// application renders every templatable field of a generated Application.
func (r *renderer) application(app *models.Application) error {
	app.Metadata.Name = r.text(app.Metadata.Name)
	app.Metadata.Namespace = r.text(app.Metadata.Namespace)

	if app.Spec.Destination != nil {
		app.Spec.Destination.Server = r.text(app.Spec.Destination.Server)
		app.Spec.Destination.Namespace = r.text(app.Spec.Destination.Namespace)
	}

	r.source(app.Spec.Source)
	for _, source := range app.Spec.Sources {
		r.source(source)
	}

	return r.err
}

// source renders one Application source, including its Helm inputs.
func (r *renderer) source(source *models.Source) {
	if source == nil {
		return
	}

	source.RepoURL = r.text(source.RepoURL)
	source.Chart = r.text(source.Chart)
	source.TargetRevision = r.text(source.TargetRevision)
	source.Path = r.text(source.Path)

	source.Helm.ReleaseName = r.text(source.Helm.ReleaseName)
	source.Helm.Values = r.text(source.Helm.Values)

	for i := range source.Helm.ValueFiles {
		source.Helm.ValueFiles[i] = r.text(source.Helm.ValueFiles[i])
	}

	for i := range source.Helm.Parameters {
		source.Helm.Parameters[i].Name = r.text(source.Helm.Parameters[i].Name)
		source.Helm.Parameters[i].Value = r.text(source.Helm.Parameters[i].Value)
	}

	source.Helm.ValuesObject = r.mapping(source.Helm.ValuesObject)
}

// mapping renders a map's keys as well as its values, which ArgoCD does so a
// template can name the key it is setting. Keys are visited in sorted order so
// a collision is reported the same way on every run.
func (r *renderer) mapping(original map[string]any) map[string]any {
	if original == nil || r.err != nil {
		return original
	}

	keys := make([]string, 0, len(original))
	for key := range original {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	rendered := make(map[string]any, len(original))
	for _, key := range keys {
		renderedKey := r.text(key)
		if r.err != nil {
			return original
		}

		if _, taken := rendered[renderedKey]; taken {
			r.err = fmt.Errorf("key %q renders to %q, which another key in the same map already produced", key, renderedKey)
			return original
		}

		rendered[renderedKey] = r.nested(original[key])
	}

	return rendered
}

// nested renders the strings inside helm.valuesObject, which is free-form YAML
// and so may nest maps and sequences to any depth.
func (r *renderer) nested(value any) any {
	switch typed := value.(type) {
	case string:
		return r.text(typed)
	case map[string]any:
		return r.mapping(typed)
	case []any:
		for i, item := range typed {
			typed[i] = r.nested(item)
		}
		return typed
	default:
		return value
	}
}
