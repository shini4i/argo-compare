package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// anchoredAppSetYAML is a list-generator ApplicationSet whose template renders
// path-based Applications out of charts/demo, the directory the anchor sits in.
const anchoredAppSetYAML = `apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: demo
  namespace: argocd
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=error"]
  generators:
    - list:
        elements:
          - cluster: dev
          - cluster: prod
  template:
    metadata:
      name: '{{ .cluster }}-demo'
      namespace: argocd
    spec:
      destination:
        server: https://kubernetes.default.svc
        namespace: '{{ .cluster }}'
      source:
        repoURL: ORIGIN_URL
        path: charts/demo
        targetRevision: HEAD
        helm:
          releaseName: '{{ .cluster }}-demo'
          values: 'cluster: {{ .cluster }}'
`

// anchoredChartFiles is the chart the anchored ApplicationSet renders, with the
// anchor file that points changes under it back at the manifest.
func anchoredChartFiles(replicas string) map[string]string {
	return map[string]string{
		"charts/demo/Chart.yaml":         "apiVersion: v2\nname: demo\nversion: 0.0.1\n",
		"charts/demo/values.yaml":        "replicaCount: " + replicas + "\n",
		"charts/demo/templates/dep.yaml": "kind: Deployment\n",
		"charts/demo/.argo-compare.yml":  "application:\n  path: " + appSetPath + "\n",
	}
}

// TestAppRunAnchoredApplicationSet is the gap this flow closes: the change is a
// chart value, the manifest it feeds is an ApplicationSet, and the anchor is
// the only thing tying the two together.
func TestAppRunAnchoredApplicationSet(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepoState(t,
		branchState{manifest: anchoredAppSetYAML, files: anchoredChartFiles("1")},
		branchState{manifest: anchoredAppSetYAML, files: anchoredChartFiles("7")})

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	output := runner.log.String()

	assert.Contains(t, output, "Processing anchored chart")
	assert.Contains(t, output, "===> Comparing generated Application: [dev-demo]")
	assert.Contains(t, output, "===> Comparing generated Application: [prod-demo]")
	// The chart differs only in a value the Helm stub does not read, so the
	// comparison is expected to report no diff — what is under test is that
	// each generated Application reaches it at all.
	assert.Contains(t, output, "No diff was found in rendered manifests!")

	// Both generated Applications exist on both branches, so each renders twice.
	assert.Equal(t, 4, runner.helm.callCount("RenderAppSource"))
	assert.Equal(t, 0, runner.helm.callCount("DownloadHelmChart"), "a path-based source must not trigger helm pull")

	runner.assertTempDirsRemoved(t)
}

// foreignGeneratorAppSetYAML anchors an ApplicationSet whose git generator
// reads a repository argo-compare has no tree for.
const foreignGeneratorAppSetYAML = `apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: demo
  namespace: argocd
spec:
  goTemplate: true
  generators:
    - git:
        repoURL: https://elsewhere.example.com/group/other.git
        revision: HEAD
        directories:
          - path: clusters/*
  template:
    metadata:
      name: '{{ .path.basename }}-demo'
      namespace: argocd
    spec:
      destination:
        server: https://kubernetes.default.svc
        namespace: demo
      source:
        repoURL: ORIGIN_URL
        path: charts/demo
        targetRevision: HEAD
`

// TestAppRunAnchoredApplicationSet_ForeignGenerator proves an anchor naming an
// ApplicationSet this repository cannot expand fails instead of comparing
// nothing: the user asked for that manifest explicitly.
func TestAppRunAnchoredApplicationSet_ForeignGenerator(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepoState(t,
		branchState{manifest: foreignGeneratorAppSetYAML, files: anchoredChartFiles("1")},
		branchState{manifest: foreignGeneratorAppSetYAML, files: anchoredChartFiles("7")})

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName}, nil)
	err := runner.app.Run(context.Background())

	require.ErrorIs(t, err, models.ErrUnsupportedAppConfiguration)
	assert.Contains(t, err.Error(), "anchored ApplicationSet")
	assert.Contains(t, err.Error(), appSetPath)
	// The changed-manifest flow skips this same manifest with a warning; the
	// anchor flow must not, or the change it names goes uncompared.
	assert.Zero(t, runner.helm.callCount("RenderAppSource"))
	assert.NotContains(t, runner.log.String(), "Skipping")
}

// crossRepoAppSetYAML lives in another repository and generates one path-based
// Application per cluster directory of the repository being compared.
func crossRepoAppSetYAML(localOrigin string) string {
	return `apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: cluster-addons
  namespace: argocd
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=error"]
  generators:
    - git:
        repoURL: ` + localOrigin + `
        revision: HEAD
        directories:
          - path: clusters/*
  template:
    metadata:
      name: '{{ .path.basename }}-addons'
      namespace: argocd
    spec:
      destination:
        server: https://kubernetes.default.svc
        namespace: '{{ .path.basename }}'
      source:
        repoURL: ` + localOrigin + `
        path: '{{ .path.path }}'
        targetRevision: HEAD
        helm:
          releaseName: '{{ .path.basename }}-addons'
`
}

// clusterChart is the per-directory chart a cross-repo generator generates for.
func clusterChart(cluster string) map[string]string {
	prefix := "clusters/" + cluster + "/"

	return map[string]string{
		prefix + "Chart.yaml":         "apiVersion: v2\nname: " + cluster + "\nversion: 0.0.1\n",
		prefix + "values.yaml":        "replicaCount: 1\n",
		prefix + "templates/dep.yaml": "kind: Deployment\n",
	}
}

// TestAppRunAnchoredApplicationSet_CrossRepo covers what only an anchor can
// reach: the ApplicationSet lives in another repository, so the local scan
// never sees it, and adding a cluster directory generates an Application the
// target branch does not.
func TestAppRunAnchoredApplicationSet_CrossRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	tempDir := t.TempDir()
	localOrigin := filepath.Join(tempDir, "origin.git")
	appSetRepo := filepath.Join(tempDir, "appsets.git")
	require.NoError(t, seedBareRepoWithApplication(t, appSetRepo, "main", "apps/addons.yaml", crossRepoAppSetYAML(localOrigin)))

	anchorFile := map[string]string{
		"clusters/.argo-compare.yml": "application:\n  repo: " + appSetRepo + "\n  path: apps/addons.yaml\n  branch: main\n",
	}

	mainFiles := mergeFileMaps(anchorFile, clusterChart("dev"))
	featureFiles := mergeFileMaps(mainFiles, clusterChart("staging"))
	seedLocalRepo(t, tempDir, localOrigin, mainFiles, featureFiles)

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName, PrintAddedManifests: true}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	output := runner.log.String()

	assert.Contains(t, output, "Processing anchored chart")
	assert.Contains(t, output, "generates 2 Application(s) on this branch and 1 on main; comparing 2")
	assert.Contains(t, output, "===> Comparing generated Application: [dev-addons]")
	assert.Contains(t, output, "===> Comparing generated Application: [staging-addons]")
	assert.Contains(t, output, "would be added")

	// dev exists on both branches, staging only on this one.
	assert.Equal(t, 3, runner.helm.callCount("RenderAppSource"))
	assert.Equal(t, 0, runner.helm.callCount("DownloadHelmChart"), "a path-based source must not trigger helm pull")

	runner.assertTempDirsRemoved(t)
}

// mergeFileMaps returns the union of two file maps, with later entries winning.
func mergeFileMaps(maps ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, m := range maps {
		for name, content := range m {
			merged[name] = content
		}
	}

	return merged
}

// seedLocalRepo builds the repository under comparison: main holds mainFiles,
// and the checked-out feature branch holds featureFiles. It chdirs into the
// work tree, so tests using it can never run in parallel.
func seedLocalRepo(t *testing.T, tempDir, originDir string, mainFiles, featureFiles map[string]string) {
	t.Helper()

	_, err := git.PlainInit(originDir, true)
	require.NoError(t, err)

	workDir := filepath.Join(tempDir, "work")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	repo, err := git.PlainInit(workDir, false)
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))))

	worktree, err := repo.Worktree()
	require.NoError(t, err)
	writeBranchFiles(t, worktree, workDir, mainFiles)

	initialHash, err := worktree.Commit("initial commit", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{originDir}})
	require.NoError(t, err)
	require.NoError(t, repo.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{"refs/heads/main:refs/heads/main"}}))
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), initialHash)))

	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("feature/anchored-appset"), Create: true}))
	writeBranchFiles(t, worktree, workDir, featureFiles)
	_, err = worktree.Commit("add a cluster", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldWD))
	})
}

// TestAppRunAnchoredApplicationSet_ValidationFailure proves a schema failure in
// a generated Application still fails the run through the anchored route.
func TestAppRunAnchoredApplicationSet_ValidationFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepoState(t,
		branchState{manifest: anchoredAppSetYAML, files: anchoredChartFiles("1")},
		branchState{manifest: anchoredAppSetYAML, files: anchoredChartFiles("7")})

	validator := &stubValidator{result: ports.ValidationResult{Valid: false, ResourceCount: 1, ErrorCount: 1}}
	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName}, validator)

	require.ErrorIs(t, runner.app.Run(context.Background()), ErrManifestValidationFailed)

	// Only the source leg is validated, so one call per generated Application.
	assert.Equal(t, 2, validator.calls)
	assert.Contains(t, runner.log.String(), "Validation errors found")
}

// TestAppRunAnchoredApplicationSet_ManifestAlsoChanged proves the changed-file
// flow owns an ApplicationSet the diff already carries: comparing it again via
// its anchor would double every generated Application's diff and comment.
func TestAppRunAnchoredApplicationSet_ManifestAlsoChanged(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	featureManifest := strings.Replace(anchoredAppSetYAML,
		"          - cluster: prod\n",
		"          - cluster: prod\n          - cluster: staging\n", 1)

	seedAppSetRepoState(t,
		branchState{manifest: anchoredAppSetYAML, files: anchoredChartFiles("1")},
		branchState{manifest: featureManifest, files: anchoredChartFiles("7")})

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName, PrintAddedManifests: true}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	output := runner.log.String()

	assert.Contains(t, output, "Processing changed ApplicationSet: ["+appSetPath+"]")
	assert.NotContains(t, output, "Processing anchored chart")
	assert.NotContains(t, output, "Anchored ApplicationSet")

	// dev and prod on both branches, staging only on this one — five renders,
	// which is one per leg per Application and no repeat from the anchor.
	assert.Equal(t, 5, runner.helm.callCount("RenderAppSource"))

	runner.assertTempDirsRemoved(t)
}

// gitDirAnchoredAppSetYAML is anchored *and* reachable by the repository scan,
// because its generator covers the directory the anchor sits in.
const gitDirAnchoredAppSetYAML = `apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: demo
  namespace: argocd
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=error"]
  generators:
    - git:
        repoURL: ORIGIN_URL
        revision: HEAD
        directories:
          - path: charts/*
  template:
    metadata:
      name: '{{ .path.basename }}-demo'
      namespace: argocd
    spec:
      destination:
        server: https://kubernetes.default.svc
        namespace: demo
      source:
        repoURL: ORIGIN_URL
        path: '{{ .path.path }}'
        targetRevision: HEAD
        helm:
          releaseName: '{{ .path.basename }}-demo'
`

// TestAppRunAnchoredApplicationSet_DiscoveryWins is the second dedup route: the
// repository scan enqueues the manifest because a generator covers the changed
// directory, so the anchor group must drop out rather than compare it twice.
func TestAppRunAnchoredApplicationSet_DiscoveryWins(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepoState(t,
		branchState{manifest: gitDirAnchoredAppSetYAML, files: anchoredChartFiles("1")},
		branchState{manifest: gitDirAnchoredAppSetYAML, files: anchoredChartFiles("7")})

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	output := runner.log.String()

	assert.Contains(t, output, "Processing changed ApplicationSet: ["+appSetPath+"]")
	assert.NotContains(t, output, "Processing anchored chart")
	assert.Equal(t, 2, runner.helm.callCount("RenderAppSource"), "one Application, one render per leg")

	runner.assertTempDirsRemoved(t)
}

// TestAppRunAnchoredApplicationSet_SharedByTwoAnchors proves two chart
// directories anchored to one manifest describe a single comparison. Without
// deduplication every generated Application would be diffed once per anchor.
func TestAppRunAnchoredApplicationSet_SharedByTwoAnchors(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	withSecondAnchor := func(replicas string) map[string]string {
		files := anchoredChartFiles(replicas)
		files["charts/other/Chart.yaml"] = "apiVersion: v2\nname: other\nversion: 0.0.1\n"
		files["charts/other/values.yaml"] = "replicaCount: " + replicas + "\n"
		files["charts/other/.argo-compare.yml"] = "application:\n  path: ./" + appSetPath + "\n"

		return files
	}

	seedAppSetRepoState(t,
		branchState{manifest: anchoredAppSetYAML, files: withSecondAnchor("1")},
		branchState{manifest: anchoredAppSetYAML, files: withSecondAnchor("7")})

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	// Two Applications, two legs each — not four renders per Application.
	assert.Equal(t, 4, runner.helm.callCount("RenderAppSource"))
	assert.Equal(t, 1, strings.Count(runner.log.String(), "Anchored ApplicationSet"))

	runner.assertTempDirsRemoved(t)
}

// runNewAnchoredAppSetChart seeds an anchored ApplicationSet whose chart
// directory is added for the first time on this branch: both branches generate
// the same Applications, but the baseline tree holds no chart to render.
func runNewAnchoredAppSetChart(t *testing.T, printAdded bool) *appSetRunner {
	t.Helper()

	seedAppSetRepoState(t,
		branchState{manifest: anchoredAppSetYAML},
		branchState{manifest: anchoredAppSetYAML, files: anchoredChartFiles("1")})

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName, PrintAddedManifests: printAdded}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	return runner
}

// TestAppRunAnchoredApplicationSet_NewChart_PrintAdded renders the source leg
// alone rather than failing on a baseline tree that has no chart.
func TestAppRunAnchoredApplicationSet_NewChart_PrintAdded(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	runner := runNewAnchoredAppSetChart(t, true)

	assert.Contains(t, runner.log.String(), "does not exist in target branch main, assuming it is new")
	assert.Equal(t, 2, runner.helm.callCount("RenderAppSource"), "source leg only, for each of the two Applications")

	runner.assertTempDirsRemoved(t)
}

// TestAppRunAnchoredApplicationSet_NewChart_Default skips a comparison that has
// no baseline, matching how a newly added Application manifest is handled.
func TestAppRunAnchoredApplicationSet_NewChart_Default(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	runner := runNewAnchoredAppSetChart(t, false)

	output := runner.log.String()
	assert.Contains(t, output, "does not exist in target branch main, assuming it is new")
	assert.NotContains(t, output, "No diff was found")

	runner.assertTempDirsRemoved(t)
}

// TestAppRunAnchoredApplicationSet_AddedSkippedByDefault proves an Application
// only this branch generates is skipped, not rendered, without the print flag.
func TestAppRunAnchoredApplicationSet_AddedSkippedByDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	tempDir := t.TempDir()
	localOrigin := filepath.Join(tempDir, "origin.git")
	appSetRepo := filepath.Join(tempDir, "appsets.git")
	require.NoError(t, seedBareRepoWithApplication(t, appSetRepo, "main", "apps/addons.yaml", crossRepoAppSetYAML(localOrigin)))

	anchorFile := map[string]string{
		"clusters/.argo-compare.yml": "application:\n  repo: " + appSetRepo + "\n  path: apps/addons.yaml\n  branch: main\n",
	}
	mainFiles := mergeFileMaps(anchorFile, clusterChart("dev"))
	seedLocalRepo(t, tempDir, localOrigin, mainFiles, mergeFileMaps(mainFiles, clusterChart("staging")))

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	output := runner.log.String()
	assert.Contains(t, output, "Skipping added Application [staging-addons]")
	assert.Equal(t, 2, runner.helm.callCount("RenderAppSource"), "only dev-addons renders, on both legs")

	runner.assertTempDirsRemoved(t)
}

// registryAnchoredAppSetYAML generates registry-chart Applications, which no
// change to this repository's chart directories can affect.
const registryAnchoredAppSetYAML = `apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: demo
  namespace: argocd
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=error"]
  generators:
    - list:
        elements:
          - cluster: dev
  template:
    metadata:
      name: '{{ .cluster }}-demo'
      namespace: argocd
    spec:
      destination:
        server: https://kubernetes.default.svc
        namespace: demo
      source:
        repoURL: fake.repo/charts
        chart: demo-chart
        targetRevision: 1.0.0
`

// TestAppRunAnchoredApplicationSet_RegistryOnly proves an anchor whose
// ApplicationSet renders nothing from this repository fails rather than pulling
// two identical charts and reporting an empty diff.
func TestAppRunAnchoredApplicationSet_RegistryOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepoState(t,
		branchState{manifest: registryAnchoredAppSetYAML, files: anchoredChartFiles("1")},
		branchState{manifest: registryAnchoredAppSetYAML, files: anchoredChartFiles("7")})

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName}, nil)
	err := runner.app.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "generates no Application rendering a chart from this repository")
	assert.Contains(t, runner.log.String(), "its source is a registry chart")
	assert.Zero(t, runner.helm.callCount("DownloadHelmChart"), "a chart outside this repository must not be pulled")
}

// foreignSourceAppSetYAML generates a path-based Application belonging to a
// repository other than the one being compared.
func foreignSourceAppSetYAML() string {
	return strings.Replace(anchoredAppSetYAML,
		"        repoURL: ORIGIN_URL\n",
		"        repoURL: https://elsewhere.example.com/group/other.git\n", 1)
}

// TestAppRunAnchoredApplicationSet_ForeignSource proves a generated Application
// whose source is another repository is not rendered from this repository's
// tree, which would produce a confident diff of the wrong content.
func TestAppRunAnchoredApplicationSet_ForeignSource(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	manifest := foreignSourceAppSetYAML()
	seedAppSetRepoState(t,
		branchState{manifest: manifest, files: anchoredChartFiles("1")},
		branchState{manifest: manifest, files: anchoredChartFiles("7")})

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName}, nil)
	err := runner.app.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "generates no Application rendering a chart from this repository")
	assert.Contains(t, runner.log.String(), "is not this one, so this repository's tree cannot render it")
	assert.Zero(t, runner.helm.callCount("RenderAppSource"))
}
