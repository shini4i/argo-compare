package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shini4i/argo-compare/internal/anchor"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeRepoIdentity(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"https with .git", "https://host.example.com/group/repo.git", "host.example.com/group/repo"},
		{"https without .git", "https://host.example.com/group/repo", "host.example.com/group/repo"},
		{"ssh standard", "ssh://git@host.example.com/group/repo.git", "host.example.com/group/repo"},
		{"ssh non-standard port drops port", "ssh://git@host.example.com:1022/group/repo.git", "host.example.com/group/repo"},
		{"ipv6 with port drops port and brackets", "ssh://git@[2001:db8::1]:1022/group/repo.git", "2001:db8::1/group/repo"},
		{"scp style", "git@host.example.com:group/repo.git", "host.example.com/group/repo"},
		{"scp style no .git", "git@host.example.com:group/repo", "host.example.com/group/repo"},
		{"trailing slash", "https://host.example.com/group/repo/", "host.example.com/group/repo"},
		{"oci prefix", "oci://host.example.com/group/repo", "host.example.com/group/repo"},
		{"lowercase host", "HTTPS://Host.Example.Com/group/repo.git", "host.example.com/group/repo"},
		{"local path", "/tmp/foo.git", "/tmp/foo"},
		{"file scheme", "file:///tmp/foo.git", "/tmp/foo"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, normalizeRepoIdentity(c.in))
		})
	}
}

func TestRepoIdentityMatches(t *testing.T) {
	cases := []struct {
		name      string
		a, b      string
		wantMatch bool
	}{
		{"https vs ssh same repo", "https://host.example.com/group/repo.git", "ssh://git@host.example.com/group/repo.git", true},
		{"https vs ssh-with-port same repo", "https://host.example.com/group/repo.git", "ssh://git@host.example.com:1022/group/repo.git", true},
		{"ipv6 https vs ssh-with-port same repo", "https://[2001:db8::1]/group/repo.git", "ssh://git@[2001:db8::1]:1022/group/repo.git", true},
		{"scp vs https same repo", "git@host.example.com:group/repo.git", "https://host.example.com/group/repo.git", true},
		{"different repo", "https://host.example.com/group/repo.git", "https://host.example.com/group/other.git", false},
		{"different host", "https://a.example.com/group/repo.git", "https://b.example.com/group/repo.git", false},
		{"file vs bare path same repo", "file:///tmp/foo.git", "/tmp/foo.git", true},
		{"one empty", "", "https://host.example.com/group/repo.git", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.wantMatch, repoIdentityMatches(c.a, c.b))
		})
	}
}

func TestAssertSameRepo(t *testing.T) {
	const httpsOrigin = "https://host.example.com/group/repo.git"

	t.Run("single source: ssh-with-port matches portless https origin", func(t *testing.T) {
		src := &models.Source{RepoURL: "ssh://git@host.example.com:1022/group/repo.git", Path: "charts/app"}
		assert.NoError(t, assertSameRepo(src, nil, httpsOrigin))
	})

	t.Run("single source: different host is rejected", func(t *testing.T) {
		src := &models.Source{RepoURL: "https://other.example.com/group/repo.git", Path: "charts/app"}
		assert.Error(t, assertSameRepo(src, nil, httpsOrigin))
	})

	t.Run("empty origin is a hard fail", func(t *testing.T) {
		src := &models.Source{RepoURL: httpsOrigin, Path: "charts/app"}
		assert.Error(t, assertSameRepo(src, nil, ""))
	})

	t.Run("multi-source: all must match", func(t *testing.T) {
		sources := []*models.Source{
			{RepoURL: "ssh://git@host.example.com:1022/group/repo.git", Path: "charts/a"},
			{RepoURL: "https://host.example.com/group/repo.git", Path: "charts/b"},
		}
		assert.NoError(t, assertSameRepo(nil, sources, httpsOrigin))
	})

	t.Run("multi-source: one mismatch rejects", func(t *testing.T) {
		sources := []*models.Source{
			{RepoURL: "ssh://git@host.example.com:1022/group/repo.git", Path: "charts/a"},
			{RepoURL: "https://other.example.com/group/repo.git", Path: "charts/b"},
		}
		assert.Error(t, assertSameRepo(nil, sources, httpsOrigin))
	})

	t.Run("source without path is skipped", func(t *testing.T) {
		// A source with no Path is not a path-based source; its repoURL is not checked.
		src := &models.Source{RepoURL: "https://other.example.com/group/repo.git"}
		assert.NoError(t, assertSameRepo(src, nil, httpsOrigin))
	})
}

func TestCheckSourceValueFilesPresent(t *testing.T) {
	const repoRoot = "/repo"
	crossRepoRef := anchor.ApplicationRef{
		Repo:   "https://example.com/group/apps.git",
		Path:   "apps/demo.yaml",
		Branch: "main",
	}
	chartDir := filepath.Join(repoRoot, "charts", "demo")

	newTarget := func(valueFiles []string) *Target {
		app := models.Application{}
		app.Spec.Source = &models.Source{
			RepoURL: "https://example.com/group/charts.git",
			Path:    "charts/demo",
			Helm:    models.HelmSource{ValueFiles: valueFiles},
		}
		return &Target{Type: TargetTypeSource, App: app}
	}

	newFs := func(t *testing.T) afero.Fs {
		t.Helper()
		fs := afero.NewMemMapFs()
		require.NoError(t, fs.MkdirAll(chartDir, 0o755))
		return fs
	}

	t.Run("all referenced value files present", func(t *testing.T) {
		fs := newFs(t)
		require.NoError(t, afero.WriteFile(fs, filepath.Join(chartDir, "values.yaml"), []byte("x: 1"), 0o644))
		assert.NoError(t, newTarget([]string{"values.yaml"}).checkSourceValueFilesPresent(fs, repoRoot, crossRepoRef))
	})

	t.Run("missing value file yields actionable error", func(t *testing.T) {
		fs := newFs(t)
		err := newTarget([]string{"values-prod.yaml"}).checkSourceValueFilesPresent(fs, repoRoot, crossRepoRef)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrValueFileMissingFromSource)
		assert.Contains(t, err.Error(), "values-prod.yaml")
		assert.Contains(t, err.Error(), "main")
		// The message must stay actionable: name the offending source path and
		// point at the docs that explain the workaround.
		assert.Contains(t, err.Error(), "charts/demo")
		assert.Contains(t, err.Error(), "docs/anchored-repositories.md")
	})

	t.Run("inner loop keeps checking after a present value file", func(t *testing.T) {
		fs := newFs(t)
		require.NoError(t, afero.WriteFile(fs, filepath.Join(chartDir, "values.yaml"), []byte("x: 1"), 0o644))
		err := newTarget([]string{"values.yaml", "missing.yaml"}).checkSourceValueFilesPresent(fs, repoRoot, crossRepoRef)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrValueFileMissingFromSource)
		assert.Contains(t, err.Error(), "missing.yaml")
	})

	// A source opting out cannot be out of sync with the chart, so the preflight
	// must not report drift ArgoCD would render through.
	t.Run("missing value file is fine with ignoreMissingValueFiles", func(t *testing.T) {
		fs := newFs(t)
		target := newTarget([]string{"values-prod.yaml"})
		target.App.Spec.Source.Helm.IgnoreMissingValueFiles = true

		assert.NoError(t, target.checkSourceValueFilesPresent(fs, repoRoot, crossRepoRef))
	})

	t.Run("multi-source Application with a nil entry is checked without panicking", func(t *testing.T) {
		fs := newFs(t)
		require.NoError(t, afero.WriteFile(fs, filepath.Join(chartDir, "values.yaml"), []byte("x: 1"), 0o644))

		app := models.Application{}
		app.Spec.MultiSource = true
		app.Spec.Sources = []*models.Source{
			nil,
			{Path: "charts/demo", Helm: models.HelmSource{ValueFiles: []string{"values.yaml"}}},
		}
		tgt := &Target{Type: TargetTypeSource, App: app}

		assert.NoError(t, tgt.checkSourceValueFilesPresent(fs, repoRoot, crossRepoRef))
	})

	t.Run("no referenced value files is fine", func(t *testing.T) {
		assert.NoError(t, newTarget(nil).checkSourceValueFilesPresent(newFs(t), repoRoot, crossRepoRef))
	})

	t.Run("malformed entries deferred to renderer validation", func(t *testing.T) {
		// Absolute paths, "..", and empty entries are rejected later by
		// validateValueFile with a specific error; the preflight must not mask it.
		fs := newFs(t)
		tgt := newTarget([]string{"../escape.yaml", "/abs.yaml", ""})
		assert.NoError(t, tgt.checkSourceValueFilesPresent(fs, repoRoot, crossRepoRef))
	})

	t.Run("ArgoCD $ref multi-source entries are not treated as local paths", func(t *testing.T) {
		// "$values/env/prod.yaml" points into another multi-source `ref:` source,
		// not this chart, so the preflight must skip it rather than raise a
		// spurious ErrValueFileMissingFromSource.
		fs := newFs(t)
		tgt := newTarget([]string{"$values/env/prod.yaml"})
		assert.NoError(t, tgt.checkSourceValueFilesPresent(fs, repoRoot, crossRepoRef))
	})

	t.Run("multi-source Application checks every source", func(t *testing.T) {
		fs := newFs(t)
		otherChartDir := filepath.Join(repoRoot, "charts", "other")
		require.NoError(t, fs.MkdirAll(otherChartDir, 0o755))
		require.NoError(t, afero.WriteFile(fs, filepath.Join(chartDir, "values.yaml"), []byte("x: 1"), 0o644))

		app := models.Application{}
		app.Spec.MultiSource = true
		app.Spec.Sources = []*models.Source{
			{Path: "charts/demo", Helm: models.HelmSource{ValueFiles: []string{"values.yaml"}}},
			{Path: "charts/other", Helm: models.HelmSource{ValueFiles: []string{"missing.yaml"}}},
		}
		tgt := &Target{Type: TargetTypeSource, App: app}

		err := tgt.checkSourceValueFilesPresent(fs, repoRoot, crossRepoRef)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrValueFileMissingFromSource)
		assert.Contains(t, err.Error(), "missing.yaml")
	})

	t.Run("flagged source does not excuse an unflagged one in the same Application", func(t *testing.T) {
		// The skip is per-source: a source setting the flag must not end the scan and
		// let a later source's genuinely missing values file reach helm unreported.
		fs := newFs(t)
		otherChartDir := filepath.Join(repoRoot, "charts", "other")
		require.NoError(t, fs.MkdirAll(otherChartDir, 0o755))

		app := models.Application{}
		app.Spec.MultiSource = true
		app.Spec.Sources = []*models.Source{
			{Path: "charts/demo", Helm: models.HelmSource{
				ValueFiles:              []string{"values-prod.yaml"},
				IgnoreMissingValueFiles: true,
			}},
			{Path: "charts/other", Helm: models.HelmSource{ValueFiles: []string{"missing.yaml"}}},
		}
		tgt := &Target{Type: TargetTypeSource, App: app}

		err := tgt.checkSourceValueFilesPresent(fs, repoRoot, crossRepoRef)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrValueFileMissingFromSource)
		assert.Contains(t, err.Error(), "missing.yaml")
	})

	t.Run("path escaping the repo root is rejected even when ignoreMissingValueFiles is set", func(t *testing.T) {
		// The flag drops missing values files, not path validation: spec.source.path is
		// pull-request-author input and is resolved before the source is skipped.
		fs := newFs(t)
		app := models.Application{}
		app.Spec.Source = &models.Source{
			Path: "../outside",
			Helm: models.HelmSource{
				ValueFiles:              []string{"values.yaml"},
				IgnoreMissingValueFiles: true,
			},
		}
		tgt := &Target{Type: TargetTypeSource, App: app}

		err := tgt.checkSourceValueFilesPresent(fs, repoRoot, crossRepoRef)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "escapes repository root")
	})
}

// TestCheckSourceValueFilesPresentSkipsMissingChartDir pins that a chart directory absent
// from the working tree is left for materialization to report, not blamed on values files.
func TestCheckSourceValueFilesPresentSkipsMissingChartDir(t *testing.T) {
	app := models.Application{}
	app.Spec.Source = &models.Source{Path: "charts/gone", Helm: models.HelmSource{ValueFiles: []string{"values.yaml"}}}
	tgt := &Target{Type: TargetTypeSource, App: app}

	assert.NoError(t, tgt.checkSourceValueFilesPresent(afero.NewMemMapFs(), "/repo", anchor.ApplicationRef{Repo: "https://example.com/apps.git", Path: "apps/demo.yaml"}))
}

// TestCheckSourceValueFilesPresentLeavesSymlinksToMaterialization pins that no committed symlink
// is followed, wherever it sits on the path: whether its target holds the file must not change
// the error a PR author sees, so the preflight defers to materialization's symlink rejection.
func TestCheckSourceValueFilesPresentLeavesSymlinksToMaterialization(t *testing.T) {
	ref := anchor.ApplicationRef{Repo: "https://example.com/apps.git", Path: "apps/demo.yaml"}
	newTarget := func(valueFile string) *Target {
		app := models.Application{}
		app.Spec.Source = &models.Source{Path: "charts/demo", Helm: models.HelmSource{ValueFiles: []string{valueFile}}}
		return &Target{Type: TargetTypeSource, App: app}
	}

	t.Run("dangling symlink as the values file", func(t *testing.T) {
		repoRoot := t.TempDir()
		chartDir := filepath.Join(repoRoot, "charts", "demo")
		require.NoError(t, os.MkdirAll(chartDir, 0o755))
		require.NoError(t, os.Symlink("/nonexistent/target", filepath.Join(chartDir, "values.yaml")))

		assert.NoError(t, newTarget("values.yaml").checkSourceValueFilesPresent(afero.NewOsFs(), repoRoot, ref))
	})

	t.Run("chart directory is a symlink to a directory lacking the file", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "charts"), 0o755))
		require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(repoRoot, "charts", "demo")))

		assert.NoError(t, newTarget("values.yaml").checkSourceValueFilesPresent(afero.NewOsFs(), repoRoot, ref))
	})

	t.Run("intermediate directory of a nested values file is a symlink", func(t *testing.T) {
		repoRoot := t.TempDir()
		chartDir := filepath.Join(repoRoot, "charts", "demo")
		require.NoError(t, os.MkdirAll(chartDir, 0o755))
		require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(chartDir, "env")))

		assert.NoError(t, newTarget("env/prod.yaml").checkSourceValueFilesPresent(afero.NewOsFs(), repoRoot, ref))
	})

	t.Run("nested values file missing along real directories is still reported", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "charts", "demo", "env"), 0o755))

		err := newTarget("env/prod.yaml").checkSourceValueFilesPresent(afero.NewOsFs(), repoRoot, ref)
		assert.ErrorIs(t, err, ErrValueFileMissingFromSource)
	})
}
