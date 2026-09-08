package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refTarget builds a Target whose Application uses a registry chart plus a
// values-only ref source, the shape from ArgoCD's multiple_sources guide.
func refTarget(t *testing.T, valueFiles []string) *Target {
	t.Helper()
	app := models.Application{Kind: models.KindApplication}
	app.Metadata.Name = "demo"
	app.Spec.Sources = []*models.Source{
		{
			RepoURL:        "https://prometheus-community.github.io/helm-charts",
			Chart:          "prometheus",
			TargetRevision: "15.7.1",
			Helm:           models.HelmSource{ValueFiles: valueFiles},
		},
		{
			RepoURL:        "https://git.example.com/org/value-files.git",
			TargetRevision: "dev",
			Ref:            "values",
		},
	}
	require.NoError(t, app.Validate())
	return &Target{TmpDir: "/tmp/run", Type: TargetTypeSource, App: app}
}

func TestSplitRefValueFile(t *testing.T) {
	tests := []struct {
		name    string
		entry   string
		refName string
		rel     string
		ok      bool
	}{
		{name: "ref prefixed", entry: "$values/envs/prod/values.yaml", refName: "values", rel: "envs/prod/values.yaml", ok: true},
		{name: "single segment", entry: "$values/values.yaml", refName: "values", rel: "values.yaml", ok: true},
		{name: "plain path", entry: "values.yaml", ok: false},
		{name: "nested plain path", entry: "envs/prod/values.yaml", ok: false},
		{name: "dollar without slash", entry: "$values", ok: false},
		{name: "dollar only", entry: "$", ok: false},
		{name: "empty", entry: "", ok: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			refName, rel, ok := splitRefValueFile(tc.entry)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.refName, refName)
			assert.Equal(t, tc.rel, rel)
		})
	}
}

func TestTargetRefSources(t *testing.T) {
	target := refTarget(t, []string{"$values/envs/prod/values.yaml"})

	refs, err := target.refSources()

	require.NoError(t, err)
	require.Contains(t, refs, "values")
	assert.Equal(t, "https://git.example.com/org/value-files.git", refs["values"].RepoURL)
	assert.Len(t, refs, 1, "the chart source declares no ref")
}

func TestResolveValueFilesMixesChartAndRefEntriesInOrder(t *testing.T) {
	target := refTarget(t, []string{"values.yaml", "$values/envs/prod/values.yaml", "overrides.yaml"})

	resolved, err := target.resolveValueFiles(target.App.Spec.Sources[0])

	require.NoError(t, err)
	chartDir := filepath.Join("/tmp/run", "charts", TargetTypeSource, "prometheus")
	refDir := filepath.Join("/tmp/run", "refs", TargetTypeSource, "values")
	assert.Equal(t, []string{
		filepath.Join(chartDir, "values.yaml"),
		filepath.Join(refDir, "envs/prod/values.yaml"),
		filepath.Join(chartDir, "overrides.yaml"),
	}, resolved, "helm applies valueFiles in order, so precedence must survive resolution")
}

func TestResolveValueFilesUnknownRef(t *testing.T) {
	target := refTarget(t, []string{"$missing/values.yaml"})

	_, err := target.resolveValueFiles(target.App.Spec.Sources[0])

	require.ErrorIs(t, err, ErrUnknownValueFileRef)
	assert.Contains(t, err.Error(), "missing")
}

// TestResolveValueFilesRejectsTraversal keeps the path guard applied to the
// remainder after the $ref prefix: valueFiles are PR-author controlled, so a
// traversal there would read host files into the rendered diff.
func TestResolveValueFilesRejectsTraversal(t *testing.T) {
	tests := []struct {
		name  string
		entry string
	}{
		{name: "ref traversal", entry: "$values/../../../etc/passwd"},
		{name: "ref absolute", entry: "$values//etc/passwd"},
		{name: "chart traversal", entry: "../../etc/passwd"},
		{name: "chart absolute", entry: "/etc/passwd"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := refTarget(t, []string{tc.entry})

			_, err := target.resolveValueFiles(target.App.Spec.Sources[0])

			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidValueFilePath)
		})
	}
}

func TestUsedRefFilesDeduplicates(t *testing.T) {
	target := refTarget(t, []string{"$values/a.yaml", "$values/a.yaml", "$values/b.yaml", "local.yaml"})

	used, err := target.usedRefFiles()

	require.NoError(t, err)
	assert.Equal(t, []refFile{
		{Ref: "values", Path: "a.yaml"},
		{Ref: "values", Path: "b.yaml"},
	}, used, "declared order is kept so a materialization failure is reproducible")
}

// TestUsedRefFilesIgnoresRefSourceOwnValueFiles ensures a values-only ref
// source is never itself rendered, so its own helm block is irrelevant.
func TestUsedRefFilesIgnoresRefSourceOwnValueFiles(t *testing.T) {
	target := refTarget(t, []string{"$values/a.yaml"})
	target.App.Spec.Sources[1].Helm.ValueFiles = []string{"$values/ignored.yaml"}

	used, err := target.usedRefFiles()

	require.NoError(t, err)
	assert.Equal(t, []refFile{{Ref: "values", Path: "a.yaml"}}, used)
}

// TestSingleSourceApplicationHasNoRefs covers spec.source: there is no sibling
// to reference, so a $ref entry cannot resolve.
func TestSingleSourceApplicationHasNoRefs(t *testing.T) {
	app := models.Application{Kind: models.KindApplication}
	app.Metadata.Name = "demo"
	app.Spec.Source = &models.Source{RepoURL: "https://example.com/charts", Chart: "demo", TargetRevision: "1.0.0",
		Helm: models.HelmSource{ValueFiles: []string{"$values/a.yaml"}}}
	require.NoError(t, app.Validate())
	target := &Target{TmpDir: "/tmp/run", Type: TargetTypeSource, App: app}

	_, err := target.resolveValueFiles(app.Spec.Source)

	require.ErrorIs(t, err, ErrUnknownValueFileRef)
}

// TestResolveValueFilesRejectsUnsafeRefName guards the other half of the
// boundary: the ref name becomes a directory, so a separator or a traversal in
// it must be refused rather than relied on to be harmless after joining.
func TestResolveValueFilesRejectsUnsafeRefName(t *testing.T) {
	tests := []struct {
		name    string
		refName string
	}{
		{name: "parent traversal", refName: ".."},
		{name: "current dir", refName: "."},
		{name: "separator", refName: "a/b"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := refTarget(t, []string{"$" + tc.refName + "/values.yaml"})
			target.App.Spec.Sources[1].Ref = tc.refName

			_, err := target.resolveValueFiles(target.App.Spec.Sources[0])

			require.ErrorIs(t, err, ErrInvalidValueFilePath)
		})
	}
}

// TestRefSourcesRejectsDuplicateName guards the ambiguity that would otherwise
// resolve silently against the last declaration: a same-repo and a remote ref
// are read from different places, so the choice is not cosmetic.
func TestRefSourcesRejectsDuplicateName(t *testing.T) {
	target := refTarget(t, []string{"$values/a.yaml"})
	target.App.Spec.Sources = append(target.App.Spec.Sources, &models.Source{
		RepoURL: "https://attacker.tld/other.git", TargetRevision: "main", Ref: "values",
	})

	_, err := target.refSources()

	require.ErrorIs(t, err, ErrInvalidValueFilePath)
	assert.Contains(t, err.Error(), "more than one source")
}

// TestDuplicateRefNamePropagates keeps the ambiguity fatal on every route into
// the resolution layer, not just the direct one.
func TestDuplicateRefNamePropagates(t *testing.T) {
	build := func(t *testing.T) *Target {
		t.Helper()
		target := refTarget(t, []string{"$values/a.yaml"})
		target.App.Spec.Sources = append(target.App.Spec.Sources, &models.Source{
			RepoURL: "https://attacker.tld/other.git", TargetRevision: "main", Ref: "values",
		})
		return target
	}

	t.Run("usedRefFiles", func(t *testing.T) {
		_, err := build(t).usedRefFiles()
		require.ErrorIs(t, err, ErrInvalidValueFilePath)
	})

	t.Run("resolveValueFiles", func(t *testing.T) {
		target := build(t)
		_, err := target.resolveValueFiles(target.App.Spec.Sources[0])
		require.ErrorIs(t, err, ErrInvalidValueFilePath)
	})

	t.Run("refsThisRepo", func(t *testing.T) {
		_, err := refsThisRepo(build(t), testOriginURL)
		require.ErrorIs(t, err, ErrInvalidValueFilePath)
	})
}

// TestResolveValueFilesNormalizesRefPath pins the normalization both legs
// depend on: the working tree cleans a path on the way to disk, while a Git
// tree lookup matches the entry name exactly.
func TestResolveValueFilesNormalizesRefPath(t *testing.T) {
	for _, entry := range []string{
		"$values/./envs/prod/values.yaml",
		"$values/envs//prod/values.yaml",
		"$values/envs/staging/../prod/values.yaml",
	} {
		t.Run(entry, func(t *testing.T) {
			target := refTarget(t, []string{entry})

			used, err := target.usedRefFiles()

			require.NoError(t, err)
			assert.Equal(t, []refFile{{Ref: "values", Path: "envs/prod/values.yaml"}}, used)
		})
	}
}

// TestValidateRelValueFileAcceptsDottedNames is the false positive the
// segment-wise traversal check exists to avoid.
func TestValidateRelValueFileAcceptsDottedNames(t *testing.T) {
	for _, p := range []string{"..values.yaml", "envs/..values.yaml", "...yaml", ".values.yaml"} {
		assert.NoError(t, validateRelValueFile(p), "%q is a legal file name", p)
	}
	for _, p := range []string{"..", "../x.yaml", "a/../../x.yaml"} {
		assert.ErrorIs(t, validateRelValueFile(p), ErrInvalidValueFilePath, "%q escapes", p)
	}
}

// TestResolveValueFilesRejectsEmptyRemainder covers "$values/", which would
// otherwise resolve to the ref directory itself and reach helm as a directory.
func TestResolveValueFilesRejectsEmptyRemainder(t *testing.T) {
	for _, entry := range []string{"$values/", ""} {
		t.Run(entry, func(t *testing.T) {
			target := refTarget(t, []string{entry})

			_, err := target.resolveValueFiles(target.App.Spec.Sources[0])

			require.ErrorIs(t, err, ErrInvalidValueFilePath)
			assert.Contains(t, err.Error(), "must not be empty")
		})
	}
}

// TestResolveValueFilesStayInsideTmpDir is the producer half of the contract
// the renderer enforces: every resolved path must sit under the render root, or
// helm is handed something validateValueFile will refuse.
func TestResolveValueFilesStayInsideTmpDir(t *testing.T) {
	target := refTarget(t, []string{"values.yaml", "$values/envs/prod/values.yaml", "nested/extra.yaml"})

	resolved, err := target.resolveValueFiles(target.App.Spec.Sources[0])

	require.NoError(t, err)
	require.Len(t, resolved, 3)
	for _, p := range resolved {
		assert.True(t, filepath.IsAbs(p), "%q must be absolute", p)
		assert.True(t, strings.HasPrefix(p, target.TmpDir+string(filepath.Separator)),
			"%q must stay inside the render directory %q", p, target.TmpDir)
	}
}

// TestUsedRefFilesKeepsDeclaredOrder pins the stable ordering the function
// documents, which is what makes a multi-ref failure reproducible.
func TestUsedRefFilesKeepsDeclaredOrder(t *testing.T) {
	target := refTarget(t, []string{"$values/z.yaml", "$values/a.yaml", "$values/m.yaml"})

	used, err := target.usedRefFiles()

	require.NoError(t, err)
	assert.Equal(t, []refFile{
		{Ref: "values", Path: "z.yaml"},
		{Ref: "values", Path: "a.yaml"},
		{Ref: "values", Path: "m.yaml"},
	}, used)
}

// TestUsedRefFilesDedupesAcrossSources covers two renderable sources naming the
// same ref file: it is materialized once.
func TestUsedRefFilesDedupesAcrossSources(t *testing.T) {
	target := refTarget(t, []string{"$values/shared.yaml"})
	target.App.Spec.Sources = append(target.App.Spec.Sources, &models.Source{
		RepoURL:        "https://prometheus-community.github.io/helm-charts",
		Chart:          "alertmanager",
		TargetRevision: "1.0.0",
		Helm:           models.HelmSource{ValueFiles: []string{"$values/shared.yaml"}},
	})

	used, err := target.usedRefFiles()

	require.NoError(t, err)
	assert.Equal(t, []refFile{{Ref: "values", Path: "shared.yaml"}}, used)
}
