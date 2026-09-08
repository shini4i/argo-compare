package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shini4i/argo-compare/internal/anchor"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitOpsRepoPlaceholder is replaced with the anchored repository's URL, which
// the manifest names before the test knows it.
const gitOpsRepoPlaceholder = "GITOPS_URL"

// remoteGeneratorAppSetYAML lives in the gitops repository and lists that same
// repository's directories, while the values its Applications render come from
// the repository holding the anchor.
const remoteGeneratorAppSetYAML = `apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: demo
  namespace: argocd
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=error"]
  generators:
    - git:
        repoURL: GITOPS_URL
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
        namespace: '{{ .path.basename }}'
      sources:
        - repoURL: fake.repo/charts
          chart: demo-chart
          targetRevision: 1.0.0
          helm:
            releaseName: '{{ .path.basename }}-demo'
            valueFiles:
              - $values/envs/{{ .path.basename }}/values.yaml
        - repoURL: ORIGIN_URL
          targetRevision: main
          ref: values
`

// stubAnchorFetcher resolves every anchor to one manifest and clone, standing
// in for a cross-repo fetch without a reachable remote.
type stubAnchorFetcher struct {
	manifest ports.AnchoredManifest
	refs     []anchor.ApplicationRef
	err      error
}

func (s *stubAnchorFetcher) Fetch(_ context.Context, ref anchor.ApplicationRef, _ string) (ports.AnchoredManifest, error) {
	s.refs = append(s.refs, ref)
	if s.err != nil {
		return ports.AnchoredManifest{}, s.err
	}
	return s.manifest, nil
}

// remoteGeneratorValuesFiles is the values tree the anchoring repository holds,
// with the anchor pointing changes under it at the remote ApplicationSet.
func remoteGeneratorValuesFiles(replicas string) map[string]string {
	return map[string]string{
		"envs/dev/values.yaml":  "replicaCount: " + replicas + "\n",
		"envs/prod/values.yaml": "replicaCount: " + replicas + "\n",
		"envs/.argo-compare.yml": "application:\n  repo: " + testGitOpsRepo +
			"\n  path: apps/demo.yaml\n",
	}
}

// newRemoteGeneratorFetcher builds the fetcher stub for the anchored
// ApplicationSet, with a clone holding the directories its generator lists.
func newRemoteGeneratorFetcher(t *testing.T, originURL string) *stubAnchorFetcher {
	t.Helper()

	manifestYAML := strings.ReplaceAll(remoteGeneratorAppSetYAML, gitOpsRepoPlaceholder, testGitOpsRepo)
	manifestYAML = strings.ReplaceAll(manifestYAML, originPlaceholder, originURL)

	appSet, err := parseApplicationSetContent([]byte(manifestYAML))
	require.NoError(t, err)

	return &stubAnchorFetcher{manifest: ports.AnchoredManifest{
		ApplicationSet: appSet,
		Tree: stubRepoTree{files: map[string]string{
			"apps/demo.yaml":              manifestYAML,
			"clusters/dev/kustomization":  "dev\n",
			"clusters/prod/kustomization": "prod\n",
		}},
		TreeRevision: "main",
	}}
}

// TestAppRunAnchoredApplicationSetWithRemoteGitGenerator compares a values
// change in this repository against an ApplicationSet that lives elsewhere and
// generates its Applications from that other repository's directories.
func TestAppRunAnchoredApplicationSetWithRemoteGitGenerator(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	origin := seedAppSetRepoState(t,
		branchState{files: remoteGeneratorValuesFiles("1")},
		branchState{files: remoteGeneratorValuesFiles("7")})

	fetcher := newRemoteGeneratorFetcher(t, origin)
	runner := newAppSetRunnerWithFetcher(t, Config{AnchorFileName: DefaultAnchorFileName}, fetcher)
	require.NoError(t, runner.app.Run(context.Background()))

	output := runner.log.String()
	assert.Contains(t, output, "===> Comparing generated Application: [dev-demo]")
	assert.Contains(t, output, "===> Comparing generated Application: [prod-demo]")

	assertNoOneSidedApplication(t, output)
	assert.Equal(t, 4, runner.helm.callCount("RenderAppSource"), "two Applications on two legs")

	require.NotEmpty(t, fetcher.refs)
	assert.Equal(t, testGitOpsRepo, fetcher.refs[0].Repo, "the anchor must resolve against the gitops repository")

	src, ok := runner.helm.renderFor(TargetTypeSource, "dev-demo")
	require.True(t, ok, "the source leg must render")
	dst, ok := runner.helm.renderFor(TargetTypeDestination, "dev-demo")
	require.True(t, ok, "the destination leg must render")

	assert.Equal(t, "replicaCount: 7\n", onlyRefValues(t, src, TargetTypeSource))
	assert.Equal(t, "replicaCount: 1\n", onlyRefValues(t, dst, TargetTypeDestination))

	runner.assertTempDirsRemoved(t)
}

// TestAppRunAnchoredApplicationSetRemoteGeneratorWithoutTree fails loudly when
// the fetcher supplies no clone, rather than expanding the generator against a
// local tree holding none of its directories.
func TestAppRunAnchoredApplicationSetRemoteGeneratorWithoutTree(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	origin := seedAppSetRepoState(t,
		branchState{files: remoteGeneratorValuesFiles("1")},
		branchState{files: remoteGeneratorValuesFiles("7")})

	fetcher := newRemoteGeneratorFetcher(t, origin)
	fetcher.manifest.Tree = nil
	fetcher.manifest.TreeRevision = ""

	runner := newAppSetRunnerWithFetcher(t, Config{AnchorFileName: DefaultAnchorFileName}, fetcher)
	err := runner.app.Run(context.Background())

	require.ErrorIs(t, err, models.ErrUnsupportedAppConfiguration)
	assert.Contains(t, err.Error(), "no tree")
}

// anchoredRepoGeneratorAppSetYAML lists the repository holding it by URL, and
// pins revision to the anchor's branch rather than HEAD so a wrong
// TreeRevision fails the run instead of passing silently. param names the
// generator parameter carrying the cluster, which differs by generator kind.
func anchoredRepoGeneratorAppSetYAML(appSetRepo, localOrigin, generator, param string) string {
	return `apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: addons
  namespace: argocd
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=error"]
  generators:
    - git:
        repoURL: ` + appSetRepo + `
        revision: main
` + generator + `
  template:
    metadata:
      name: '{{ ` + param + ` }}-addons'
      namespace: argocd
    spec:
      destination:
        server: https://kubernetes.default.svc
        namespace: '{{ ` + param + ` }}'
      sources:
        - repoURL: fake.repo/charts
          chart: demo-chart
          targetRevision: 1.0.0
          helm:
            releaseName: '{{ ` + param + ` }}-addons'
            valueFiles:
              - $values/envs/{{ ` + param + ` }}/values.yaml
        - repoURL: ` + localOrigin + `
          targetRevision: main
          ref: values
`
}

// anchoredRepoValuesFiles is the local repository under comparison: the values
// the generated Applications render, plus the anchor reaching the manifest.
func anchoredRepoValuesFiles(replicas, appSetRepo string) map[string]string {
	return map[string]string{
		"envs/dev/values.yaml":  "replicaCount: " + replicas + "\n",
		"envs/prod/values.yaml": "replicaCount: " + replicas + "\n",
		"envs/.argo-compare.yml": "application:\n  repo: " + appSetRepo +
			"\n  path: apps/addons.yaml\n  branch: main\n",
	}
}

// assertNoOneSidedApplication proves both legs generated the same Applications,
// which serving them one shared tree is what guarantees.
func assertNoOneSidedApplication(t *testing.T, output string) {
	t.Helper()

	for _, phrase := range []string{"would be added", "would be removed", "Skipping added", "Skipping removed"} {
		assert.NotContains(t, output, phrase, "both legs must generate the same Applications")
	}
}

// TestAppRunAnchoredAppSetRealFetcherDirectoriesGenerator drives Run() with the
// real fetcher, so the clone it makes is the tree the generator is expanded
// against — the handoff a stubbed tree cannot exercise.
func TestAppRunAnchoredAppSetRealFetcherDirectoriesGenerator(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	tempDir := t.TempDir()
	localOrigin := filepath.Join(tempDir, "origin.git")
	appSetRepo := filepath.Join(tempDir, "appsets.git")

	manifest := anchoredRepoGeneratorAppSetYAML(appSetRepo, localOrigin,
		"        directories:\n          - path: clusters/*", ".path.basename")
	require.NoError(t, seedBareRepoWithFiles(t, appSetRepo, "main", map[string]string{
		"apps/addons.yaml":          manifest,
		"clusters/dev/config.yaml":  "cluster: dev\n",
		"clusters/prod/config.yaml": "cluster: prod\n",
	}))

	seedLocalRepo(t, tempDir, localOrigin,
		anchoredRepoValuesFiles("1", appSetRepo),
		anchoredRepoValuesFiles("7", appSetRepo))

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName, PrintAddedManifests: true}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	output := runner.log.String()
	assert.Contains(t, output, "===> Comparing generated Application: [dev-addons]")
	assert.Contains(t, output, "===> Comparing generated Application: [prod-addons]")
	assert.Contains(t, output, "generates 2 Application(s) on this branch and 2 on main; comparing 2",
		"one shared tree must leave both legs generating the same set")
	assertNoOneSidedApplication(t, output)

	assert.Equal(t, 4, runner.helm.callCount("RenderAppSource"), "two Applications on two legs")

	src, ok := runner.helm.renderFor(TargetTypeSource, "dev-addons")
	require.True(t, ok)
	dst, ok := runner.helm.renderFor(TargetTypeDestination, "dev-addons")
	require.True(t, ok)
	assert.Equal(t, "replicaCount: 7\n", onlyRefValues(t, src, TargetTypeSource))
	assert.Equal(t, "replicaCount: 1\n", onlyRefValues(t, dst, TargetTypeDestination))

	runner.assertTempDirsRemoved(t)
}

// TestAppRunAnchoredAppSetRealFetcherFilesGenerator covers the files generator,
// which takes its parameters by reading each matched blob out of the anchored
// clone rather than only listing directory names.
func TestAppRunAnchoredAppSetRealFetcherFilesGenerator(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	tempDir := t.TempDir()
	localOrigin := filepath.Join(tempDir, "origin.git")
	appSetRepo := filepath.Join(tempDir, "appsets.git")

	manifest := anchoredRepoGeneratorAppSetYAML(appSetRepo, localOrigin,
		"        files:\n          - path: clusters/**/config.yaml", ".cluster")
	require.NoError(t, seedBareRepoWithFiles(t, appSetRepo, "main", map[string]string{
		"apps/addons.yaml":          manifest,
		"clusters/dev/config.yaml":  "cluster: dev\n",
		"clusters/prod/config.yaml": "cluster: prod\n",
	}))

	seedLocalRepo(t, tempDir, localOrigin,
		anchoredRepoValuesFiles("1", appSetRepo),
		anchoredRepoValuesFiles("7", appSetRepo))

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName, PrintAddedManifests: true}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	output := runner.log.String()
	assert.Contains(t, output, "===> Comparing generated Application: [dev-addons]")
	assert.Contains(t, output, "===> Comparing generated Application: [prod-addons]")
	assert.Contains(t, output, "generates 2 Application(s) on this branch and 2 on main; comparing 2",
		"one shared tree must leave both legs generating the same set")
	assertNoOneSidedApplication(t, output)

	assert.Equal(t, 4, runner.helm.callCount("RenderAppSource"), "two Applications on two legs")

	src, ok := runner.helm.renderFor(TargetTypeSource, "dev-addons")
	require.True(t, ok, "the parameters must come from the anchored repo's committed config files")
	dst, ok := runner.helm.renderFor(TargetTypeDestination, "dev-addons")
	require.True(t, ok)
	assert.Equal(t, "replicaCount: 7\n", onlyRefValues(t, src, TargetTypeSource))
	assert.Equal(t, "replicaCount: 1\n", onlyRefValues(t, dst, TargetTypeDestination))

	runner.assertTempDirsRemoved(t)
}
