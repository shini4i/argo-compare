package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/shini4i/argo-compare/internal/models"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEffectiveChartName(t *testing.T) {
	cases := []struct {
		name string
		src  models.Source
		want string
	}{
		{"chart only", models.Source{Chart: "foo"}, "foo"},
		{"path only", models.Source{Path: "charts/foo"}, "foo"},
		{"trailing slash path", models.Source{Path: "charts/foo/"}, "foo"},
		{"single-component path", models.Source{Path: "foo"}, "foo"},
		{"empty source", models.Source{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, effectiveChartName(&c.src))
		})
	}
}

func TestTargetPathBased(t *testing.T) {
	cases := []struct {
		name string
		app  models.Application
		want bool
	}{
		{
			name: "single source registry",
			app: models.Application{Spec: struct {
				Source      *models.Source      `yaml:"source"`
				Sources     []*models.Source    `yaml:"sources"`
				MultiSource bool                `yaml:"-"`
				Destination *models.Destination `yaml:"destination"`
			}{Source: &models.Source{Chart: "foo"}}},
			want: false,
		},
		{
			name: "single source path",
			app: models.Application{Spec: struct {
				Source      *models.Source      `yaml:"source"`
				Sources     []*models.Source    `yaml:"sources"`
				MultiSource bool                `yaml:"-"`
				Destination *models.Destination `yaml:"destination"`
			}{Source: &models.Source{Path: "charts/foo"}}},
			want: true,
		},
		{
			name: "multi source all path",
			app: models.Application{Spec: struct {
				Source      *models.Source      `yaml:"source"`
				Sources     []*models.Source    `yaml:"sources"`
				MultiSource bool                `yaml:"-"`
				Destination *models.Destination `yaml:"destination"`
			}{Sources: []*models.Source{{Path: "a"}, {Path: "b"}}, MultiSource: true}},
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tgt := Target{App: c.app}
			assert.Equal(t, c.want, tgt.PathBased())
		})
	}
}

func TestTargetClassifySource_MixedMultiSourceRejected(t *testing.T) {
	tgt := Target{App: models.Application{Spec: struct {
		Source      *models.Source      `yaml:"source"`
		Sources     []*models.Source    `yaml:"sources"`
		MultiSource bool                `yaml:"-"`
		Destination *models.Destination `yaml:"destination"`
	}{
		Sources: []*models.Source{
			{Chart: "registry-chart"},
			{Path: "charts/foo"},
		},
		MultiSource: true,
	}}}
	err := tgt.ClassifySources()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMixedMultiSource)
}

func TestTargetClassifySource_UniformPasses(t *testing.T) {
	chartOnly := Target{App: models.Application{Spec: struct {
		Source      *models.Source      `yaml:"source"`
		Sources     []*models.Source    `yaml:"sources"`
		MultiSource bool                `yaml:"-"`
		Destination *models.Destination `yaml:"destination"`
	}{Sources: []*models.Source{{Chart: "a"}, {Chart: "b"}}, MultiSource: true}}}
	require.NoError(t, chartOnly.ClassifySources())

	pathOnly := Target{App: models.Application{Spec: struct {
		Source      *models.Source      `yaml:"source"`
		Sources     []*models.Source    `yaml:"sources"`
		MultiSource bool                `yaml:"-"`
		Destination *models.Destination `yaml:"destination"`
	}{Sources: []*models.Source{{Path: "a"}, {Path: "b"}}, MultiSource: true}}}
	require.NoError(t, pathOnly.ClassifySources())
}

func TestMaterializeChartFromWorkingTree(t *testing.T) {
	repoRoot := t.TempDir()
	chartSrc := filepath.Join(repoRoot, "charts", "foo")
	require.NoError(t, os.MkdirAll(filepath.Join(chartSrc, "templates"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(chartSrc, "Chart.yaml"), []byte("name: foo\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(chartSrc, "values.yaml"), []byte("replicaCount: 1\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(chartSrc, "templates", "dep.yaml"), []byte("kind: Deployment\n"), 0o644))

	tmpDir := t.TempDir()
	tgt := Target{
		TmpDir: tmpDir,
		Type:   TargetTypeSource,
		Log:    logger.New("target-path-test"),
		App: models.Application{Spec: struct {
			Source      *models.Source      `yaml:"source"`
			Sources     []*models.Source    `yaml:"sources"`
			MultiSource bool                `yaml:"-"`
			Destination *models.Destination `yaml:"destination"`
		}{Source: &models.Source{Path: "charts/foo"}}},
	}

	require.NoError(t, tgt.MaterializeChartFromWorkingTree(context.Background(), afero.NewOsFs(), repoRoot))

	destChart := filepath.Join(tmpDir, "charts", "src", "foo")
	for _, rel := range []string{"Chart.yaml", "values.yaml", "templates/dep.yaml"} {
		_, err := os.Stat(filepath.Join(destChart, rel))
		require.NoError(t, err, "expected %s under %s", rel, destChart)
	}
}

func TestMaterializeChartFromTree(t *testing.T) {
	tree := commitTreeWith(t, map[string]string{
		"charts/foo/Chart.yaml":          "name: foo\n",
		"charts/foo/values.yaml":         "replicaCount: 9\n",
		"charts/foo/templates/dep.yaml":  "kind: Deployment\n",
	})

	tmpDir := t.TempDir()
	tgt := Target{
		TmpDir: tmpDir,
		Type:   TargetTypeDestination,
		Log:    logger.New("target-path-tree-test"),
		App: models.Application{Spec: struct {
			Source      *models.Source      `yaml:"source"`
			Sources     []*models.Source    `yaml:"sources"`
			MultiSource bool                `yaml:"-"`
			Destination *models.Destination `yaml:"destination"`
		}{Source: &models.Source{Path: "charts/foo"}}},
	}

	require.NoError(t, tgt.MaterializeChartFromTree(context.Background(), afero.NewOsFs(), tree))

	values, err := os.ReadFile(filepath.Join(tmpDir, "charts", "dst", "foo", "values.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "replicaCount: 9\n", string(values))
}

