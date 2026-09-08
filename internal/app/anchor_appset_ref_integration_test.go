package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// anchoredRefAppSetYAML anchors an ApplicationSet whose generated Applications
// render a registry chart but read their values from this repository through a
// ref source. The anchor sits in the values directory, which is the only thing
// tying a values-only change to the manifest.
const anchoredRefAppSetYAML = `apiVersion: argoproj.io/v1alpha1
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
      sources:
        - repoURL: fake.repo/charts
          chart: demo-chart
          targetRevision: 1.0.0
          helm:
            releaseName: '{{ .cluster }}-demo'
            valueFiles:
              - $values/envs/{{ .cluster }}/values.yaml
        - repoURL: ORIGIN_URL
          targetRevision: main
          ref: values
`

// anchoredRefValuesFiles is the values tree the ref source supplies, with the
// anchor file that points changes under it back at the manifest.
func anchoredRefValuesFiles(replicas string) map[string]string {
	return map[string]string{
		"envs/dev/values.yaml":   "replicaCount: " + replicas + "\n",
		"envs/prod/values.yaml":  "replicaCount: " + replicas + "\n",
		"envs/.argo-compare.yml": "application:\n  path: " + appSetPath + "\n",
	}
}

// TestAppRunAnchoredApplicationSetWithRefSource is the combination issue #157
// asks for: the change is a values file, the chart is external, and the anchor
// is what reaches the ApplicationSet. The manifest is identical on both
// branches, so nothing but the anchor can trigger the comparison.
func TestAppRunAnchoredApplicationSetWithRefSource(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepoState(t,
		branchState{manifest: anchoredRefAppSetYAML, files: anchoredRefValuesFiles("1")},
		branchState{manifest: anchoredRefAppSetYAML, files: anchoredRefValuesFiles("7")})

	runner := newAppSetRunner(t, Config{AnchorFileName: DefaultAnchorFileName}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	output := runner.log.String()
	assert.Contains(t, output, "===> Comparing generated Application: [dev-demo]")
	assert.Contains(t, output, "===> Comparing generated Application: [prod-demo]")

	// Two Applications on both branches, so four renders; each pulls the
	// registry chart, which no tree in this repository holds.
	assert.Equal(t, 4, runner.helm.callCount("RenderAppSource"))
	assert.Equal(t, 4, runner.helm.callCount("DownloadHelmChart"),
		"a registry chart reached through an anchor must still be pulled")

	src, ok := runner.helm.renderFor(TargetTypeSource, "dev-demo")
	require.True(t, ok)
	dst, ok := runner.helm.renderFor(TargetTypeDestination, "dev-demo")
	require.True(t, ok)
	assert.Equal(t, "replicaCount: 7\n", onlyRefValues(t, src, TargetTypeSource))
	assert.Equal(t, "replicaCount: 1\n", onlyRefValues(t, dst, TargetTypeDestination))

	runner.assertTempDirsRemoved(t)
}
