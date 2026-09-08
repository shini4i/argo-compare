package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refAppSetYAML is the shape reported in issue #157: an ApplicationSet whose
// generated Applications render an external chart but take their values from a
// ref source, addressed as $values in valueFiles.
func refAppSetYAML(chartRevision string) string {
	return fmt.Sprintf(`apiVersion: argoproj.io/v1alpha1
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
          - cluster: dev
  template:
    metadata:
      name: '{{ .cluster }}-guestbook'
      namespace: argocd
    spec:
      destination:
        server: https://kubernetes.default.svc
        namespace: guestbook
      sources:
        - repoURL: fake.repo/charts
          chart: demo-chart
          targetRevision: %s
          helm:
            releaseName: '{{ .cluster }}-guestbook'
            valueFiles:
              - $values/envs/{{ .cluster }}/values.yaml
        - repoURL: %s
          targetRevision: master
          ref: values
`, chartRevision, originPlaceholder)
}

// TestAppRunAppSetWithLocalRefSource renders both legs of a generated
// Application whose values come from a ref source pointing at this repository,
// proving each leg reads its own revision of the values file.
func TestAppRunAppSetWithLocalRefSource(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	valuesPath := "envs/dev/values.yaml"
	seedAppSetRepoState(t,
		branchState{manifest: refAppSetYAML("1.0.0"), files: map[string]string{valuesPath: "replicaCount: 1\n"}},
		branchState{manifest: refAppSetYAML("2.0.0"), files: map[string]string{valuesPath: "replicaCount: 7\n"}},
	)

	runner := newAppSetRunner(t, Config{}, nil)
	require.NoError(t, runner.app.Run(context.Background()))

	src, ok := runner.helm.renderFor(TargetTypeSource, "dev-guestbook")
	require.True(t, ok, "the source leg must render")
	dst, ok := runner.helm.renderFor(TargetTypeDestination, "dev-guestbook")
	require.True(t, ok, "the destination leg must render")

	assert.Equal(t, "replicaCount: 7\n", onlyRefValues(t, src, TargetTypeSource),
		"the source leg must see the working tree's values")
	assert.Equal(t, "replicaCount: 1\n", onlyRefValues(t, dst, TargetTypeDestination),
		"the destination leg must see the merge-base values")

	runner.assertTempDirsRemoved(t)
}

// onlyRefValues returns the content of a render's single values file, having
// asserted that it resolved into that leg's ref directory rather than the chart.
func onlyRefValues(t *testing.T, render stubRender, targetType string) string {
	t.Helper()
	require.Len(t, render.ValueFiles, 1)
	for path, content := range render.ValueFiles {
		assert.Contains(t, path, filepath.Join("refs", targetType, "values", "envs", "dev", "values.yaml"))
		return content
	}
	return ""
}

// TestAppRunAppSetWithUnknownRefFails stops the run rather than rendering a
// diff that silently omits the values file the Application asked for.
func TestAppRunAppSetWithUnknownRefFails(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	// The generated Application references $values, but the ref source names
	// something else, so nothing declares $values.
	manifest := refAppSetYAML("1.0.0")
	broken := strings.Replace(refAppSetYAML("2.0.0"), "ref: values", "ref: other", 1)

	seedAppSetRepoState(t,
		branchState{manifest: manifest, files: map[string]string{"envs/dev/values.yaml": "replicaCount: 1\n"}},
		branchState{manifest: broken, files: map[string]string{"envs/dev/values.yaml": "replicaCount: 1\n"}},
	)

	runner := newAppSetRunner(t, Config{}, nil)
	err := runner.app.Run(context.Background())

	require.ErrorIs(t, err, ErrUnknownValueFileRef)
}

// TestAppRunAppSetWithMissingRefValuesFile names the absent file rather than
// letting Helm fail on a path the user never wrote.
func TestAppRunAppSetWithMissingRefValuesFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepoState(t,
		branchState{manifest: refAppSetYAML("1.0.0"), files: map[string]string{"envs/dev/values.yaml": "replicaCount: 1\n"}},
		branchState{manifest: refAppSetYAML("2.0.0"), files: map[string]string{"envs/staging/values.yaml": "replicaCount: 1\n"}},
	)

	runner := newAppSetRunner(t, Config{}, nil)
	err := runner.app.Run(context.Background())

	require.ErrorIs(t, err, ErrRefValueFileMissing)
	assert.Contains(t, err.Error(), "envs/dev/values.yaml")
}
