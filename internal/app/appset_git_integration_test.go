package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitAppSetYAML is a git directory generator over clusters/*, excluding one
// directory. repoURL is rewritten per test to point at the seeded origin.
func gitAppSetYAML(repoURL string) string {
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
        repoURL: ` + repoURL + `
        revision: HEAD
        directories:
          - path: clusters/*
          - path: clusters/donotdeploy
            exclude: true
  template:
    metadata:
      name: '{{ .path.basenameNormalized }}-addons'
      namespace: argocd
    spec:
      destination:
        server: https://kubernetes.default.svc
        namespace: '{{ .path.basename }}'
      source:
        repoURL: fake.repo/charts
        chart: demo-chart
        targetRevision: 1.0.0
        helm:
          releaseName: '{{ .path.basename }}-addons'
`
}

func clusterFile(cluster string) string {
	return "clusters/" + cluster + "/values.yaml"
}

// TestAppRunGitGeneratorReportsAddedDirectory is the case the git generator
// exists for: the branch adds a directory, so the ApplicationSet generates an
// Application the target branch does not.
func TestAppRunGitGeneratorReportsAddedDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	manifest := gitAppSetYAML(originPlaceholder)

	seedAppSetRepoState(t,
		branchState{manifest: manifest, files: map[string]string{
			clusterFile("dev"):         "replicaCount: 1\n",
			clusterFile("donotdeploy"): "replicaCount: 1\n",
		}},
		branchState{manifest: manifest, files: map[string]string{
			clusterFile("dev"):         "replicaCount: 1\n",
			clusterFile("donotdeploy"): "replicaCount: 1\n",
			clusterFile("staging"):     "replicaCount: 2\n",
		}})

	runner := newAppSetRunner(t, Config{PrintAddedManifests: true}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	output := runner.log.String()

	// dev exists on both branches; staging only on this one; donotdeploy is excluded.
	assert.Contains(t, output, "Generates 2 Application(s) on this branch and 1 on main; comparing 2")
	assert.Contains(t, output, "dev-addons")
	assert.Contains(t, output, "staging-addons")
	assert.NotContains(t, output, "donotdeploy")

	// dev renders on both legs, staging only on the source leg.
	assert.Equal(t, 3, runner.helm.callCount("RenderAppSource"))

	runner.assertTempDirsRemoved(t)
}

// TestAppRunGitGeneratorReportsRemovedDirectory is the mirror case: deleting a
// directory stops generating its Application.
func TestAppRunGitGeneratorReportsRemovedDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	manifest := gitAppSetYAML(originPlaceholder)

	seedAppSetRepoState(t,
		branchState{manifest: manifest, files: map[string]string{
			clusterFile("dev"):    "replicaCount: 1\n",
			clusterFile("legacy"): "replicaCount: 1\n",
		}},
		branchState{manifest: manifest, files: map[string]string{
			clusterFile("dev"): "replicaCount: 1\n",
		}})

	runner := newAppSetRunner(t, Config{}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	output := runner.log.String()
	assert.Contains(t, output, "Skipping removed Application [legacy-addons]")
	assert.Equal(t, 2, runner.helm.callCount("RenderAppSource"))
}

// TestAppRunGitGeneratorSkipsForeignRepo pins the deferred scope: a generator
// reading another repository would list the same tree for both legs, so it is
// skipped with the reason named rather than matched against the local one.
func TestAppRunGitGeneratorSkipsForeignRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	local := gitAppSetYAML(originPlaceholder)
	foreign := gitAppSetYAML("https://github.com/someone/elsewhere.git")

	seedAppSetRepoState(t,
		branchState{manifest: local, files: map[string]string{clusterFile("dev"): "replicaCount: 1\n"}},
		branchState{manifest: foreign, files: map[string]string{clusterFile("dev"): "replicaCount: 1\n"}})

	runner := newAppSetRunner(t, Config{}, nil)

	require.NoError(t, runner.app.Run(context.Background()))
	assert.Contains(t, runner.log.String(), "elsewhere.git")
	assert.Zero(t, runner.helm.callCount("RenderAppSource"))
}

// TestAppRunDiscoveryIgnoresForeignRepoApplicationSet is the blast radius the
// repository scan introduces: an ApplicationSet nobody edited must not fail a
// run just because a directory change happens to match its patterns.
func TestAppRunDiscoveryIgnoresForeignRepoApplicationSet(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	foreign := gitAppSetYAML("https://github.com/someone/elsewhere.git")

	seedAppSetRepoState(t,
		branchState{manifest: foreign, files: map[string]string{clusterFile("dev"): "replicaCount: 1\n"}},
		branchState{manifest: foreign, files: map[string]string{
			clusterFile("dev"):     "replicaCount: 1\n",
			clusterFile("staging"): "replicaCount: 2\n",
		}})

	runner := newAppSetRunner(t, Config{PrintAddedManifests: true}, nil)

	require.NoError(t, runner.app.Run(context.Background()))
	assert.Zero(t, runner.helm.callCount("RenderAppSource"))
	assert.NotContains(t, runner.log.String(), "Processing changed ApplicationSet")
}
