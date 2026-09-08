package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// anchoredRefAppYAML is a plain Application — not an ApplicationSet — that
// renders a registry chart and takes its values from this repository through a
// ref source. The anchor sits in the values directory it reads.
const anchoredRefAppYAML = `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: demo
  namespace: argocd
spec:
  destination:
    server: https://kubernetes.default.svc
    namespace: demo
  sources:
    - repoURL: fake.repo/charts
      chart: demo-chart
      targetRevision: 1.0.0
      helm:
        releaseName: demo
        valueFiles:
          - $values/envs/dev/values.yaml
    - repoURL: ORIGIN_URL
      targetRevision: main
      ref: values
`

// anchoredRefAppFiles is the values tree plus the anchor pointing at the
// Application manifest.
func anchoredRefAppFiles(replicas string) map[string]string {
	return map[string]string{
		"envs/dev/values.yaml":   "replicaCount: " + replicas + "\n",
		"envs/.argo-compare.yml": "application:\n  path: " + appSetPath + "\n",
	}
}

// TestAppRunAnchoredApplicationWithRefSource is issue #157 for a plain
// Application: the manifest is unchanged, only a values file moved, and the
// chart is external. Before ref support this failed as "not path-based".
func TestAppRunAnchoredApplicationWithRefSource(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepoState(t,
		branchState{manifest: anchoredRefAppYAML, files: anchoredRefAppFiles("1")},
		branchState{manifest: anchoredRefAppYAML, files: anchoredRefAppFiles("7")})

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	assert.Contains(t, runner.log.String(), "Processing anchored chart")

	// One Application, two legs, and the registry chart pulled for each: no tree
	// in this repository holds it.
	assert.Equal(t, 2, runner.helm.callCount("RenderAppSource"))
	assert.Equal(t, 2, runner.helm.callCount("DownloadHelmChart"),
		"a registry chart reached through an anchor must still be pulled")

	src, ok := runner.helm.renderFor(TargetTypeSource, "demo")
	require.True(t, ok, "the source leg must render")
	dst, ok := runner.helm.renderFor(TargetTypeDestination, "demo")
	require.True(t, ok, "the destination leg must render")
	assert.Equal(t, "replicaCount: 7\n", onlyRefValues(t, src, TargetTypeSource))
	assert.Equal(t, "replicaCount: 1\n", onlyRefValues(t, dst, TargetTypeDestination))

	runner.assertTempDirsRemoved(t)
}

// TestAppRunAnchoredApplicationRegistryChartWithoutRef keeps the gate closed
// where it should be: nothing in this repository feeds the render, so the
// anchor cannot be reporting on it.
func TestAppRunAnchoredApplicationRegistryChartWithoutRef(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	const manifest = `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: demo
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

	seedAppSetRepoState(t,
		branchState{manifest: manifest, files: anchoredRefAppFiles("1")},
		branchState{manifest: manifest, files: anchoredRefAppFiles("7")})

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName}, nil)
	err := runner.app.Run(context.Background())

	require.ErrorIs(t, err, ErrAnchorNotPathBased)
}
