package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/shini4i/argo-compare/internal/anchor"
	"github.com/shini4i/argo-compare/internal/models"
)

// noLstatFs hides the underlying filesystem's Lstater implementation: embedding the afero.Fs
// interface promotes no LstatIfPossible, which is the shape the preflight must refuse.
type noLstatFs struct {
	afero.Fs
}

// lstatErrFs fails LstatIfPossible for one exact path with a non-NotExist error and delegates
// every other path, so a test can choose which component of the walk breaks.
type lstatErrFs struct {
	afero.Fs
	failPath string
	err      error
}

func (f lstatErrFs) LstatIfPossible(name string) (os.FileInfo, bool, error) {
	if name == f.failPath {
		return nil, false, f.err
	}
	return f.Fs.(afero.Lstater).LstatIfPossible(name)
}

// anchorErrRef is the cross-repo anchor the preflight error messages are attributed to.
var anchorErrRef = anchor.ApplicationRef{
	Repo:   "https://example.com/group/apps.git",
	Path:   "apps/demo.yaml",
	Branch: "main",
}

// singleSourceTarget builds a path-based source leg naming valueFiles under charts/demo.
func singleSourceTarget(valueFiles []string) *Target {
	app := models.Application{}
	app.Spec.Source = &models.Source{
		RepoURL: "https://example.com/group/charts.git",
		Path:    "charts/demo",
		Helm:    models.HelmSource{ValueFiles: valueFiles},
	}
	return &Target{Type: TargetTypeSource, App: app}
}

// TestPreflightRefusesFilesystemThatCannotLstat pins that the preflight fails loudly on a
// filesystem it cannot inspect without following links, rather than falling back to a
// following stat and leaking whether a symlink's target exists.
func TestPreflightRefusesFilesystemThatCannotLstat(t *testing.T) {
	err := singleSourceTarget([]string{"values.yaml"}).
		checkSourceValueFilesPresent(noLstatFs{Fs: afero.NewMemMapFs()}, "/repo", anchorErrRef)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot lstat")
}

// TestPreflightReportsChartDirLstatFailure pins that an unreadable chart directory is named as
// such. It must not be reported as a missing values file, which would send the author editing
// valueFiles that are in fact present.
func TestPreflightReportsChartDirLstatFailure(t *testing.T) {
	const repoRoot = "/repo"
	chartDir := filepath.Join(repoRoot, "charts", "demo")
	sentinel := errors.New("permission denied")
	base := afero.NewMemMapFs()
	require.NoError(t, base.MkdirAll(chartDir, 0o755))

	fs := lstatErrFs{Fs: base, failPath: chartDir, err: sentinel}
	err := singleSourceTarget([]string{"values.yaml"}).
		checkSourceValueFilesPresent(fs, repoRoot, anchorErrRef)

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "check anchored chart dir")
	assert.Contains(t, err.Error(), "charts/demo")
}

// TestPreflightReportsValueFileLstatFailure pins that an unreadable values file names the entry
// that could not be inspected, and is not silently treated as absent.
func TestPreflightReportsValueFileLstatFailure(t *testing.T) {
	const repoRoot = "/repo"
	chartDir := filepath.Join(repoRoot, "charts", "demo")
	sentinel := errors.New("permission denied")
	base := afero.NewMemMapFs()
	require.NoError(t, base.MkdirAll(chartDir, 0o755))

	fs := lstatErrFs{Fs: base, failPath: filepath.Join(chartDir, "values-prod.yaml"), err: sentinel}
	err := singleSourceTarget([]string{"values-prod.yaml"}).
		checkSourceValueFilesPresent(fs, repoRoot, anchorErrRef)

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "check anchored values file")
	assert.Contains(t, err.Error(), "values-prod.yaml")
}

// TestCheckAnchoredApplicationPropagatesRefError pins that an Application naming an undeclared
// $ref fails with that cause. Reporting it as "not path-based" would blame the anchor layout
// for what is a typo in valueFiles.
func TestCheckAnchoredApplicationPropagatesRefError(t *testing.T) {
	app := models.Application{}
	app.Spec.MultiSource = true
	app.Spec.Sources = []*models.Source{
		{
			RepoURL:        "https://charts.example.com",
			Chart:          "prometheus",
			TargetRevision: "1.0.0",
			Helm:           models.HelmSource{ValueFiles: []string{"$undeclared/values.yaml"}},
		},
	}

	appInstance := &App{logger: logger.New("anchored-ref-error-test")}
	group := AnchorGroup{Anchor: anchor.Anchor{Application: anchorErrRef}}

	err := appInstance.checkAnchoredApplication(group, app, "/repo", testOriginURL)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownValueFileRef)
}
