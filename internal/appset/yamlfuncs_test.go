package appset

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yamlv3 "gopkg.in/yaml.v3"
)

// TestToYAMLMatchesArgoCDLayout pins why this package marshals with
// sigs.k8s.io/yaml rather than the yaml.v3 it already depends on: the two
// indent nested collections differently, and toYaml output lands inside a
// manifest where indentation is structural.
func TestToYAMLMatchesArgoCDLayout(t *testing.T) {
	value := map[string]any{
		"replicaCount": 2,
		"image":        map[string]any{"repository": "demo", "tag": "1.0.0"},
		"args":         []any{"--one", "--two"},
	}

	got, err := toYAML(value)
	require.NoError(t, err)

	assert.Equal(t, `args:
- --one
- --two
image:
  repository: demo
  tag: 1.0.0
replicaCount: 2`, got)

	// The same value through yaml.v3 indents differently, so swapping the
	// library would silently change every rendered manifest that uses toYaml.
	v3, err := yamlv3.Marshal(value)
	require.NoError(t, err)
	assert.NotEqual(t, got, string(v3))
}

// TestToYAMLDropsTheTrailingNewline keeps the value usable inline, which is how
// a template embeds it.
func TestToYAMLDropsTheTrailingNewline(t *testing.T) {
	got, err := toYAML(map[string]any{"a": "b"})
	require.NoError(t, err)
	assert.Equal(t, "a: b", got)
}

func TestFromYAML(t *testing.T) {
	got, err := fromYAML("cluster:\n  name: dev\n  replicas: 3\n")
	require.NoError(t, err)

	cluster, ok := got["cluster"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "dev", cluster["name"])
	// sigs.k8s.io/yaml routes through JSON, so every number arrives as float64.
	// ArgoCD behaves identically; a switch to yaml.v3 would decode int and break parity.
	assert.Equal(t, float64(3), cluster["replicas"])

	// JSON is a subset of YAML, so a JSON document parses too.
	got, err = fromYAML(`{"cluster":{"name":"prod"}}`)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"name": "prod"}, got["cluster"])

	_, err = fromYAML("- a\n- b\n")
	assert.Error(t, err, "a sequence is not a mapping")
}

func TestFromYAMLArray(t *testing.T) {
	got, err := fromYAMLArray("- dev\n- prod\n")
	require.NoError(t, err)
	assert.Equal(t, []any{"dev", "prod"}, got)

	_, err = fromYAMLArray("a: b\n")
	assert.Error(t, err, "a mapping is not a sequence")
}

// TestToYAMLReportsUnmarshalableValues covers the error branch. yaml.v3 decodes
// a mapping with a non-string key into map[any]any, which the JSON marshalling
// underneath sigs.k8s.io/yaml refuses.
func TestToYAMLReportsUnmarshalableValues(t *testing.T) {
	got, err := toYAML(map[any]any{8080: "http"})

	require.Error(t, err)
	assert.Empty(t, got)
}

func TestToYAMLRendersNil(t *testing.T) {
	got, err := toYAML(nil)

	require.NoError(t, err)
	assert.Equal(t, "null", got)
}

// TestFromYAMLEdgeCases covers the input an absent generator parameter produces
// — the empty string — alongside genuinely malformed syntax.
func TestFromYAMLEdgeCases(t *testing.T) {
	empty, err := fromYAML("")
	require.NoError(t, err)
	assert.NotNil(t, empty)
	assert.Empty(t, empty)

	emptyList, err := fromYAMLArray("")
	require.NoError(t, err)
	assert.NotNil(t, emptyList)
	assert.Empty(t, emptyList)

	broken, err := fromYAML("a: [unclosed")
	require.Error(t, err)
	assert.Nil(t, broken)

	brokenList, err := fromYAMLArray("- [unclosed")
	require.Error(t, err)
	assert.Nil(t, brokenList)
}
