package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/shini4i/argo-compare/internal/ports/portstest"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const appSetPath = "apps/guestbook.yaml"

// originPlaceholder is replaced with the seeded origin path, so a git generator
// manifest can name the repository before that path exists.
const originPlaceholder = "ORIGIN_URL"

// legacyAppSet uses fasttemplate substitution, which argo-compare rejects.
const legacyAppSet = `apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: guestbook
  namespace: argocd
spec:
  generators:
    - list:
        elements:
          - cluster: dev
  template:
    metadata:
      name: '{{cluster}}-guestbook'
`

// applicationSetYAML renders a list-generator ApplicationSet whose elements are
// the supplied cluster/revision pairs.
func applicationSetYAML(elements [][2]string) string {
	content := `apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: guestbook
  namespace: argocd
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=error"]
  generators:
    - list:
        elements:
`
	for _, element := range elements {
		content += fmt.Sprintf("          - cluster: %s\n            revision: %s\n", element[0], element[1])
	}

	return content + `  template:
    metadata:
      name: '{{ .cluster }}-guestbook'
      namespace: argocd
    spec:
      destination:
        server: https://kubernetes.default.svc
        namespace: guestbook
      source:
        repoURL: fake.repo/charts
        chart: demo-chart
        targetRevision: '{{ .revision }}'
        helm:
          releaseName: '{{ .cluster }}-guestbook'
`
}

// branchState is what one branch holds: the ApplicationSet manifest at
// appSetPath, plus any extra files whose directories a git generator matches.
type branchState struct {
	manifest string
	files    map[string]string
}

// seedAppSetRepo builds a repository whose main branch holds mainManifest and
// whose checked-out feature branch holds featureManifest, both at appSetPath.
// An empty mainManifest leaves the file absent there.
func seedAppSetRepo(t *testing.T, mainManifest, featureManifest string) {
	t.Helper()

	seedAppSetRepoState(t, branchState{manifest: mainManifest}, branchState{manifest: featureManifest})
}

// seedAppSetRepoState is seedAppSetRepo with extra per-branch files. It chdirs
// into the repository, so tests using either helper can never run in parallel.
func seedAppSetRepoState(t *testing.T, main, feature branchState) {
	t.Helper()

	tempDir := t.TempDir()
	remoteDir := filepath.Join(tempDir, "origin.git")

	// A git generator names the repository by URL, which is only known once the
	// origin exists, so manifests carry a placeholder for it.
	mainManifest := strings.ReplaceAll(main.manifest, originPlaceholder, remoteDir)
	featureManifest := strings.ReplaceAll(feature.manifest, originPlaceholder, remoteDir)

	workDir := filepath.Join(tempDir, "work")
	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "apps"), 0o755))

	repo, err := git.PlainInit(workDir, false)
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))))

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	// Guarantees main has a commit even when the feature branch adds the manifest.
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "README.md"), []byte("seed\n"), 0o644))
	_, err = worktree.Add("README.md")
	require.NoError(t, err)

	if mainManifest != "" {
		require.NoError(t, os.WriteFile(filepath.Join(workDir, appSetPath), []byte(mainManifest), 0o644))
		_, err = worktree.Add(appSetPath)
		require.NoError(t, err)
	}
	writeBranchFiles(t, worktree, workDir, main.files)

	initialHash, err := worktree.Commit("initial commit", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	_, err = git.PlainInit(remoteDir, true)
	require.NoError(t, err)
	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remoteDir}})
	require.NoError(t, err)
	require.NoError(t, repo.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{"refs/heads/main:refs/heads/main"}}))
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), initialHash)))

	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("feature/appset"), Create: true}))

	require.NoError(t, os.WriteFile(filepath.Join(workDir, appSetPath), []byte(featureManifest), 0o644))
	_, err = worktree.Add(appSetPath)
	require.NoError(t, err)

	// Files the branch drops must be removed, not merely left out, or the diff
	// never records the deletion.
	for _, name := range sortedKeys(main.files) {
		if _, kept := feature.files[name]; !kept {
			_, removeErr := worktree.Remove(name)
			require.NoError(t, removeErr)
		}
	}
	writeBranchFiles(t, worktree, workDir, feature.files)

	_, err = worktree.Commit("update application set", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldWD))
	})
}

// appSetRunner wires an App over the seeded repository and captures its log.
type appSetRunner struct {
	app       *App
	helm      *stubHelmProcessor
	validator *stubValidator
	log       *bytes.Buffer
}

// newAppSetRunner builds an App for the current working directory. cfg supplies
// only the flags a test cares about; paths and target branch are filled in here.
func newAppSetRunner(t *testing.T, cfg Config, validator *stubValidator) *appSetRunner {
	t.Helper()

	tempDir := t.TempDir()
	cfg.TargetBranch = "main"
	cfg.CacheDir = filepath.Join(tempDir, "cache")
	cfg.TempDirBase = filepath.Join(tempDir, "tmp")
	cfg.Version = "test"
	require.NoError(t, os.MkdirAll(cfg.TempDirBase, 0o755))

	logBuffer := &bytes.Buffer{}
	logger.RedirectForTest(t, logBuffer)

	helmStub := newStubHelmProcessor(t)
	deps := Dependencies{
		FS:            afero.NewOsFs(),
		CmdRunner:     portstest.NoopCmdRunner{},
		FileReader:    utils.OsFileReader{},
		HelmProcessor: helmStub,
		Globber:       utils.CustomGlobber{},
		Logger:        logger.New("appset-test"),
	}
	if validator != nil {
		deps.ManifestValidator = validator
	}

	appInstance, err := New(cfg, deps)
	require.NoError(t, err)

	return &appSetRunner{app: appInstance, helm: helmStub, validator: validator, log: logBuffer}
}

// assertTempDirsRemoved proves every render workspace was cleaned up, which one
// comparison per generated Application makes easy to miss.
func (r *appSetRunner) assertTempDirsRemoved(t *testing.T) {
	t.Helper()

	for dir := range r.helm.tmpDirs {
		_, statErr := os.Stat(dir)
		assert.True(t, os.IsNotExist(statErr), "temporary directory %s must be cleaned up", dir)
	}
}

// TestAppRunExpandsApplicationSet drives Run() end to end over an
// ApplicationSet whose branch changes one generated Application's revision and
// adds a second Application that the target branch does not generate.
func TestAppRunExpandsApplicationSet(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepo(t,
		applicationSetYAML([][2]string{{"dev", "1.0.0"}, {"prod", "1.0.0"}}),
		applicationSetYAML([][2]string{{"dev", "1.1.0"}, {"prod", "1.0.0"}, {"staging", "1.0.0"}}))

	runner := newAppSetRunner(t, Config{PrintAddedManifests: true, PrintRemovedManifests: true}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	// dev and prod render on both branches, staging only on the source branch.
	assert.Equal(t, 5, runner.helm.callCount("RenderAppSource"))

	output := runner.log.String()
	assert.Contains(t, output, "Processing changed ApplicationSet")
	assert.Contains(t, output, "Generates 3 Application(s) on this branch and 2 on main; comparing 3")
	for _, name := range []string{"dev-guestbook", "prod-guestbook", "staging-guestbook"} {
		assert.Contains(t, output, name)
	}
	assert.Contains(t, output, "1 file would be changed")
	assert.Contains(t, output, "1 file would be added")

	runner.assertTempDirsRemoved(t)
}

// TestAppRunSkipsAddedApplicationWithoutFlag pins the default behaviour: an
// Application the branch starts generating is announced but not rendered
// unless --print-added-manifests is set.
func TestAppRunSkipsAddedApplicationWithoutFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepo(t,
		applicationSetYAML([][2]string{{"dev", "1.0.0"}}),
		applicationSetYAML([][2]string{{"dev", "1.0.0"}, {"staging", "1.0.0"}}))

	runner := newAppSetRunner(t, Config{}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	// Only the unchanged dev Application renders, on both branches.
	assert.Equal(t, 2, runner.helm.callCount("RenderAppSource"))
	assert.Contains(t, runner.log.String(), "Skipping added Application [staging-guestbook]")
}

// TestAppRunSkipsRemovedApplicationWithoutFlag covers the half of the contract
// a plain Application manifest cannot express: the branch stops generating an
// Application the target branch still generates.
func TestAppRunSkipsRemovedApplicationWithoutFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepo(t,
		applicationSetYAML([][2]string{{"dev", "1.0.0"}, {"legacy", "1.0.0"}}),
		applicationSetYAML([][2]string{{"dev", "1.0.0"}}))

	runner := newAppSetRunner(t, Config{}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	assert.Equal(t, 2, runner.helm.callCount("RenderAppSource"))
	assert.Contains(t, runner.log.String(), "Skipping removed Application [legacy-guestbook]")
}

// TestAppRunRendersRemovedApplicationWithFlag proves --print-removed-manifests
// renders the target-branch leg alone, so the Application reports as wholly
// removed rather than being silently dropped.
func TestAppRunRendersRemovedApplicationWithFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepo(t,
		applicationSetYAML([][2]string{{"dev", "1.0.0"}, {"legacy", "1.0.0"}}),
		applicationSetYAML([][2]string{{"dev", "1.0.0"}}))

	runner := newAppSetRunner(t, Config{PrintRemovedManifests: true}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	// dev on both legs, legacy on the destination leg only.
	assert.Equal(t, 3, runner.helm.callCount("RenderAppSource"))

	output := runner.log.String()
	assert.Contains(t, output, "legacy-guestbook")
	assert.Contains(t, output, "1 file would be removed")

	runner.assertTempDirsRemoved(t)
}

// TestAppRunTreatsNewApplicationSetAsAdded covers the first-use path: the
// manifest does not exist on the target branch at all.
func TestAppRunTreatsNewApplicationSetAsAdded(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepo(t, "", applicationSetYAML([][2]string{{"dev", "1.0.0"}, {"prod", "1.0.0"}}))

	runner := newAppSetRunner(t, Config{PrintAddedManifests: true}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	// Source leg only, once per generated Application.
	assert.Equal(t, 2, runner.helm.callCount("RenderAppSource"))

	output := runner.log.String()
	assert.Contains(t, output, "does not exist in target branch main, assuming it is a new ApplicationSet")
	assert.Contains(t, output, "dev-guestbook")
	assert.Contains(t, output, "prod-guestbook")
}

// TestAppRunTreatsUnexpandableBaselineAsAdded pins the migration path the docs
// recommend: adding `goTemplate: true` to a legacy manifest must not fail the
// run just because the target branch cannot be expanded.
func TestAppRunTreatsUnexpandableBaselineAsAdded(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepo(t, legacyAppSet, applicationSetYAML([][2]string{{"dev", "1.0.0"}}))

	runner := newAppSetRunner(t, Config{PrintAddedManifests: true}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	assert.Equal(t, 1, runner.helm.callCount("RenderAppSource"))

	output := runner.log.String()
	assert.Contains(t, output, "cannot be expanded on target branch main")
	assert.Contains(t, output, "goTemplate: true")
}

// TestAppRunReportsValidationFailureForGeneratedApplications proves the
// merge-gate contract holds per generated Application, not per manifest file.
func TestAppRunReportsValidationFailureForGeneratedApplications(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepo(t,
		applicationSetYAML([][2]string{{"dev", "1.0.0"}, {"prod", "1.0.0"}}),
		applicationSetYAML([][2]string{{"dev", "1.1.0"}, {"prod", "1.0.0"}}))

	validator := &stubValidator{result: ports.ValidationResult{Valid: false, ErrorCount: 1}}
	runner := newAppSetRunner(t, Config{ValidateManifests: true}, validator)

	require.ErrorIs(t, runner.app.Run(context.Background()), ErrManifestValidationFailed)

	// Validation runs on the source leg of each generated Application.
	assert.Equal(t, 2, validator.calls)
}

// TestAppRunFileToCompareRejectsUnsupportedApplicationSet pins the deliberate
// asymmetry with discovery: an explicitly named file is an error when it cannot
// be expanded, rather than a skip the user might not notice.
func TestAppRunFileToCompareRejectsUnsupportedApplicationSet(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepo(t, applicationSetYAML([][2]string{{"dev", "1.0.0"}}), legacyAppSet)

	runner := newAppSetRunner(t, Config{FileToCompare: appSetPath}, nil)

	err := runner.app.Run(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, models.ErrUnsupportedAppConfiguration)
	assert.Zero(t, runner.helm.callCount("RenderAppSource"))
}

// TestAppRunFileToCompareExpandsApplicationSet covers the supported half of the
// --file path, which bypasses discovery entirely.
func TestAppRunFileToCompareExpandsApplicationSet(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepo(t,
		applicationSetYAML([][2]string{{"dev", "1.0.0"}}),
		applicationSetYAML([][2]string{{"dev", "1.1.0"}}))

	runner := newAppSetRunner(t, Config{FileToCompare: appSetPath}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	assert.Equal(t, 2, runner.helm.callCount("RenderAppSource"))
	assert.Contains(t, runner.log.String(), "1 file would be changed")
}

// TestGetChangedFileRaw covers both outcomes the ApplicationSet flow depends on:
// the exact bytes committed on the target branch, and the sentinel for a file
// that exists only on the source branch.
func TestGetChangedFileRaw(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	manifest := applicationSetYAML([][2]string{{"dev", "1.0.0"}})
	seedAppSetRepo(t, manifest, applicationSetYAML([][2]string{{"dev", "1.1.0"}}))

	repoInstance, err := NewGitRepo(afero.NewOsFs(), portstest.NoopCmdRunner{}, utils.OsFileReader{}, logger.New("raw-test"))
	require.NoError(t, err)

	content, err := repoInstance.GetChangedFileRaw("main", appSetPath)
	require.NoError(t, err)
	assert.Equal(t, manifest, string(content))

	_, err = repoInstance.GetChangedFileRaw("main", "apps/absent.yaml")
	assert.True(t, errors.Is(err, errGitFileDoesNotExist))
}

// writeBranchFiles commits the extra files a branch holds, creating any parent
// directories the git generator will later match.
func writeBranchFiles(t *testing.T, worktree *git.Worktree, workDir string, files map[string]string) {
	t.Helper()

	for _, name := range sortedKeys(files) {
		full := filepath.Join(workDir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(files[name]), 0o644))
		_, err := worktree.Add(name)
		require.NoError(t, err)
	}
}

func sortedKeys(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
