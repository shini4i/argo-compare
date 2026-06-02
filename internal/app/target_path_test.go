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

func TestResolveRepoPath(t *testing.T) {
	repoRoot := "/repo/root"
	cases := []struct {
		name    string
		rel     string
		wantAbs string
		wantErr bool
	}{
		{"plain subdir", "charts/foo", "/repo/root/charts/foo", false},
		{"current dir", "", "/repo/root", false},
		{"dot prefix", "./charts/foo", "/repo/root/charts/foo", false},
		{"parent escape", "../escape", "", true},
		{"deep parent escape", "charts/../../../etc", "", true},
		// Absolute paths are rebased under repoRoot by filepath.Join, not an escape.
		{"absolute rebased", "/etc/passwd", "/repo/root/etc/passwd", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			abs, err := resolveRepoPath(repoRoot, c.rel)
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.wantAbs, abs)
		})
	}
}

func TestMaterializeChartFromWorkingTree_PathEscape(t *testing.T) {
	// Threat: a malicious or misconfigured Application sets spec.source.path to
	// a value that resolves outside the local repo. The guard must reject the
	// path before any I/O lands content from outside the repo in tmpDir.
	repoRoot := t.TempDir()
	tmpDir := t.TempDir()

	cases := []struct {
		name string
		path string
	}{
		{"parent escape", "../../etc"},
		{"deep parent escape", "charts/../../etc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tgt := Target{
				TmpDir: tmpDir,
				Type:   TargetTypeSource,
				Log:    logger.New("target-path-escape-test"),
				App: models.Application{Spec: struct {
					Source      *models.Source      `yaml:"source"`
					Sources     []*models.Source    `yaml:"sources"`
					MultiSource bool                `yaml:"-"`
					Destination *models.Destination `yaml:"destination"`
				}{Source: &models.Source{Path: c.path}}},
			}

			err := tgt.MaterializeChartFromWorkingTree(context.Background(), afero.NewOsFs(), repoRoot)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "escapes repository root")

			// Nothing should have been written to tmpDir/charts.
			_, statErr := os.Stat(filepath.Join(tmpDir, "charts"))
			assert.True(t, os.IsNotExist(statErr), "no chart dir must be materialized for %q", c.path)
		})
	}
}

func TestMaterializeChartFromWorkingTree_RejectsSymlinks(t *testing.T) {
	// Threat: an attacker plants a symlink inside the chart directory pointing
	// at /etc/passwd (or any file outside the chart). os.Open would silently
	// dereference, copy the target into tmpDir, and helm template would surface
	// the content in rendered manifests / PR comments. The materializer must
	// refuse symlinks rather than follow them.
	repoRoot := t.TempDir()
	chartDir := filepath.Join(repoRoot, "charts", "demo")
	require.NoError(t, os.MkdirAll(chartDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte("name: demo\n"), 0o644))

	// Plant a file outside the chart that the symlink would expose.
	outsideTarget := filepath.Join(t.TempDir(), "sensitive.txt")
	require.NoError(t, os.WriteFile(outsideTarget, []byte("secret\n"), 0o644))

	// values.yaml is a symlink to the outside file.
	require.NoError(t, os.Symlink(outsideTarget, filepath.Join(chartDir, "values.yaml")))

	tmpDir := t.TempDir()
	tgt := Target{
		TmpDir: tmpDir,
		Type:   TargetTypeSource,
		Log:    logger.New("target-path-symlink-test"),
		App: models.Application{Spec: struct {
			Source      *models.Source      `yaml:"source"`
			Sources     []*models.Source    `yaml:"sources"`
			MultiSource bool                `yaml:"-"`
			Destination *models.Destination `yaml:"destination"`
		}{Source: &models.Source{Path: "charts/demo"}}},
	}

	err := tgt.MaterializeChartFromWorkingTree(context.Background(), afero.NewOsFs(), repoRoot)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported mode")

	// Confirm the sensitive file content was NOT copied into tmpDir.
	leaked, statErr := os.Stat(filepath.Join(tmpDir, "charts", "src", "demo", "values.yaml"))
	if statErr == nil {
		t.Fatalf("symlinked file leaked into tmpDir: %v bytes", leaked.Size())
	}
}

func TestMaterializeChartFromWorkingTree_RejectsSymlinkedChartDir(t *testing.T) {
	// Variant of the above where the chart directory itself is a symlink to a
	// directory outside the repo. Lstat must catch this before any I/O.
	repoRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "charts"), 0o755))

	outsideDir := filepath.Join(t.TempDir(), "outside-chart")
	require.NoError(t, os.MkdirAll(outsideDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outsideDir, "Chart.yaml"), []byte("name: outside\n"), 0o644))

	// charts/demo -> outsideDir
	require.NoError(t, os.Symlink(outsideDir, filepath.Join(repoRoot, "charts", "demo")))

	tmpDir := t.TempDir()
	tgt := Target{
		TmpDir: tmpDir,
		Type:   TargetTypeSource,
		Log:    logger.New("target-path-symlink-dir-test"),
		App: models.Application{Spec: struct {
			Source      *models.Source      `yaml:"source"`
			Sources     []*models.Source    `yaml:"sources"`
			MultiSource bool                `yaml:"-"`
			Destination *models.Destination `yaml:"destination"`
		}{Source: &models.Source{Path: "charts/demo"}}},
	}

	err := tgt.MaterializeChartFromWorkingTree(context.Background(), afero.NewOsFs(), repoRoot)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular directory")
}

func TestMaterializeChartFromTree(t *testing.T) {
	tree := commitTreeWith(t, map[string]string{
		fooChartYAML:                    "name: foo\n",
		fooValuesYAML:                   "replicaCount: 9\n",
		"charts/foo/templates/dep.yaml": "kind: Deployment\n",
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
