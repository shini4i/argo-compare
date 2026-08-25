package appset

import (
	"strings"

	"sigs.k8s.io/yaml"
)

// toYAML renders a value as YAML for embedding in a manifest, without the
// trailing newline Marshal appends. sigs.k8s.io/yaml is used rather than the
// yaml.v3 elsewhere in this package because it is what ArgoCD marshals with,
// and the two indent nested collections differently.
func toYAML(v any) (string, error) {
	data, err := yaml.Marshal(v)
	if err != nil {
		return "", err
	}

	return strings.TrimSuffix(string(data), "\n"), nil
}

// fromYAML parses a YAML (or JSON) mapping into a map a template can index.
func fromYAML(text string) (map[string]any, error) {
	m := map[string]any{}
	if err := yaml.Unmarshal([]byte(text), &m); err != nil {
		return nil, err
	}

	return m, nil
}

// fromYAMLArray parses a YAML (or JSON) sequence into a slice a template can
// range over.
func fromYAMLArray(text string) ([]any, error) {
	a := []any{}
	if err := yaml.Unmarshal([]byte(text), &a); err != nil {
		return nil, err
	}

	return a, nil
}
