package app

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mapFileReader serves file contents from an in-memory map keyed by absolute
// path. A missing key returns (nil, nil), mirroring the ports.FileReader
// contract for an absent file.
type mapFileReader struct {
	files map[string][]byte
	err   error
}

func (m mapFileReader) ReadFile(path string) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.files[path], nil
}

const chartDir = "/tmp/run/charts/src/my-app"

func appSpecificOverride(t *testing.T, yaml string) map[string][]byte {
	t.Helper()
	return map[string][]byte{
		filepath.Join(chartDir, ".argocd-source-my-app.yaml"): []byte(yaml),
	}
}

func TestResolveHelmParameters(t *testing.T) {
	t.Run("no inline params and no override files yields nil", func(t *testing.T) {
		reader := mapFileReader{}
		got, err := resolveHelmParameters(reader, &models.Source{}, chartDir, "my-app")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("inline parameters pass through", func(t *testing.T) {
		reader := mapFileReader{}
		source := &models.Source{Helm: models.HelmSource{Parameters: []models.HelmParameter{
			{Name: "image.tag", Value: "1.0.0", ForceString: true},
		}}}
		got, err := resolveHelmParameters(reader, source, chartDir, "my-app")
		require.NoError(t, err)
		assert.Equal(t, []models.HelmParameter{{Name: "image.tag", Value: "1.0.0", ForceString: true}}, got)
	})

	t.Run("app-specific override file replaces inline value by name", func(t *testing.T) {
		// This is the reported gap: argo-watcher bumps the image tag in the
		// .argocd-source file; the rendered diff must reflect the new tag.
		reader := mapFileReader{files: appSpecificOverride(t, `
helm:
  parameters:
    - name: image.tag
      value: "2.0.0"
      forceString: true
`)}
		source := &models.Source{Helm: models.HelmSource{Parameters: []models.HelmParameter{
			{Name: "image.tag", Value: "1.0.0", ForceString: true},
		}}}
		got, err := resolveHelmParameters(reader, source, chartDir, "my-app")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "image.tag", got[0].Name)
		assert.Equal(t, "2.0.0", got[0].Value, "override file must win over inline value")
		assert.True(t, got[0].ForceString)
	})

	t.Run("generic and app-specific files merge, app-specific wins", func(t *testing.T) {
		reader := mapFileReader{files: map[string][]byte{
			filepath.Join(chartDir, ".argocd-source.yaml"): []byte(`
helm:
  parameters:
    - name: image.repository
      value: registry.example.com/app
    - name: image.tag
      value: generic-tag
`),
			filepath.Join(chartDir, ".argocd-source-my-app.yaml"): []byte(`
helm:
  parameters:
    - name: image.tag
      value: app-specific-tag
      forceString: true
`),
		}}
		got, err := resolveHelmParameters(reader, &models.Source{}, chartDir, "my-app")
		require.NoError(t, err)
		require.Len(t, got, 2)
		// First-seen order is preserved: repository (generic), then tag (generic, overridden).
		assert.Equal(t, "image.repository", got[0].Name)
		assert.Equal(t, "registry.example.com/app", got[0].Value)
		assert.Equal(t, "image.tag", got[1].Name)
		assert.Equal(t, "app-specific-tag", got[1].Value, "app-specific file must override generic")
		assert.True(t, got[1].ForceString)
	})

	t.Run("empty appName consults only the generic file", func(t *testing.T) {
		reader := mapFileReader{files: map[string][]byte{
			filepath.Join(chartDir, ".argocd-source.yaml"): []byte(`
helm:
  parameters:
    - name: image.tag
      value: from-generic
`),
		}}
		got, err := resolveHelmParameters(reader, &models.Source{}, chartDir, "")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "from-generic", got[0].Value)
	})

	t.Run("path traversal in appName is rejected, leaving inline params intact", func(t *testing.T) {
		// A malicious metadata.name must not redirect the override read outside
		// chartDir; the read is skipped and only inline params survive.
		reader := mapFileReader{files: map[string][]byte{
			"/tmp/run/charts/secret.yaml": []byte(`
helm:
  parameters:
    - name: leaked
      value: secret
`),
		}}
		source := &models.Source{Helm: models.HelmSource{Parameters: []models.HelmParameter{
			{Name: "image.tag", Value: "1.0.0"},
		}}}
		got, err := resolveHelmParameters(reader, source, chartDir, "../secret")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "image.tag", got[0].Name, "traversal filename must not be read")
	})

	t.Run("reader error is surfaced", func(t *testing.T) {
		sentinel := errors.New("permission denied")
		reader := mapFileReader{err: sentinel}
		_, err := resolveHelmParameters(reader, &models.Source{}, chartDir, "my-app")
		require.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
	})

	t.Run("malformed override YAML surfaces an error", func(t *testing.T) {
		reader := mapFileReader{files: appSpecificOverride(t, "helm: [not-a-map")}
		_, err := resolveHelmParameters(reader, &models.Source{}, chartDir, "my-app")
		require.Error(t, err)
	})
}
