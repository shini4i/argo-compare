package app

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/op/go-logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubPoster struct {
	bodies []string
	err    error
}

func (s *stubPoster) Post(body string) error {
	s.bodies = append(s.bodies, body)
	return s.err
}

func setupSilentLogger(name string, t *testing.T) *logging.Logger {
	logger := logging.MustGetLogger(name)
	logging.SetBackend(logging.NewLogBackend(io.Discard, "", 0))
	t.Cleanup(func() {
		logging.SetBackend(logging.NewLogBackend(os.Stdout, "", 0))
	})
	return logger
}

func TestCommentStrategyPresentWithDiff(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-diff", t)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ShowAdded:       true,
		ShowRemoved:     true,
		ApplicationPath: "apps/ingress.yaml",
	}

	result := ComparisonResult{
		Added: []DiffOutput{
			{File: File{Name: "/added.yaml"}, Diff: "--- /tmp/src\n+++ /tmp/dst\n+ added"},
		},
		Changed: []DiffOutput{
			{File: File{Name: "changed.yaml"}, Diff: "--- /tmp/src\n+++ /tmp/dst\n@@ diff\n- old\n+ new"},
		},
		Removed: []DiffOutput{
			{File: File{Name: "/removed.yaml"}, Diff: "--- /tmp/src\n+++ /tmp/dst\n- removed"},
		},
	}

	require.NoError(t, strategy.Present(result))
	require.Len(t, poster.bodies, 1)
	body := poster.bodies[0]
	assert.Contains(t, body, "## Argo Compare Results")
	assert.Contains(t, body, "**Application:** `apps/ingress.yaml`")
	assert.Contains(t, body, "**Summary**")
	assert.Contains(t, body, "- Added: 1")
	assert.Contains(t, body, "<summary>Added • added.yaml</summary>")
	assert.Contains(t, body, "<summary>Changed • changed.yaml</summary>")
	assert.Contains(t, body, "<summary>Removed • removed.yaml</summary>")
	assert.Contains(t, body, "```diff")
	assert.Contains(t, body, "@@ diff")
	assert.NotContains(t, body, "--- /")
	assert.NotContains(t, body, "+++ /")
	assert.Contains(t, body, "+ added")
	assert.Contains(t, body, "- old")
	assert.Contains(t, body, "+ new")
	assert.Contains(t, body, "- removed")
}

func TestCommentStrategyPresentNoDiff(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-empty", t)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ApplicationPath: "apps/foo.yaml",
	}

	require.NoError(t, strategy.Present(ComparisonResult{}))
	require.Len(t, poster.bodies, 1)
	assert.Contains(t, poster.bodies[0], "No manifest differences detected")
	assert.Contains(t, poster.bodies[0], "**Application:** `apps/foo.yaml`")
}

func TestCommentStrategyRequiresPoster(t *testing.T) {
	logger := setupSilentLogger("comment-missing", t)

	strategy := CommentStrategy{
		Log:             logger,
		ApplicationPath: "apps/foo.yaml",
	}

	err := strategy.Present(ComparisonResult{})
	require.Error(t, err)
}

func TestCommentStrategyRequiresLogger(t *testing.T) {
	strategy := CommentStrategy{
		Poster:          &stubPoster{},
		ApplicationPath: "apps/foo.yaml",
	}

	err := strategy.Present(ComparisonResult{})
	require.Error(t, err)
}

func TestCommentStrategySplitsLargeBody(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-large", t)

	largeDiff := strings.Repeat("+ oversized line\n", 160000)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ShowAdded:       true,
		ApplicationPath: "apps/big.yaml",
	}

	result := ComparisonResult{
		Added: []DiffOutput{{File: File{Name: "big.yaml"}, Diff: largeDiff}},
	}

	require.NoError(t, strategy.Present(result))
	assert.Greater(t, len(poster.bodies), 1)
	assert.Contains(t, poster.bodies[0], "Part 1 of")
	assert.Contains(t, poster.bodies[len(poster.bodies)-1], "Part "+fmt.Sprint(len(poster.bodies))+" of "+fmt.Sprint(len(poster.bodies)))
}

func TestCommentStrategyNotesHiddenSections(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-hidden", t)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ShowAdded:       false,
		ShowRemoved:     false,
		ApplicationPath: "apps/partial.yaml",
	}

	result := ComparisonResult{
		Added:   []DiffOutput{{File: File{Name: "/new.yaml"}, Diff: "+ new"}},
		Removed: []DiffOutput{{File: File{Name: "/old.yaml"}, Diff: "- old"}},
	}

	require.NoError(t, strategy.Present(result))
	require.Len(t, poster.bodies, 1)
	body := poster.bodies[0]
	assert.Contains(t, body, "(not shown)")
	assert.Contains(t, body, "Added manifests (1) are present but not shown")
	assert.Contains(t, body, "Removed manifests (1) are present but not shown")
}

func TestCommentStrategyStripsDiffHeaders(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-headers", t)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ApplicationPath: "apps/sample.yaml",
	}

	diff := "--- /tmp/argo-compare-123/src.yaml\n+++ /tmp/argo-compare-123/dst.yaml\n@@ diff"
	result := ComparisonResult{
		Changed: []DiffOutput{{File: File{Name: "/manifests/sample.yaml"}, Diff: diff}},
	}

	require.NoError(t, strategy.Present(result))
	require.Len(t, poster.bodies, 1)
	body := poster.bodies[0]
	assert.NotContains(t, body, "--- ")
	assert.NotContains(t, body, "+++ ")
	assert.Contains(t, body, "@@ diff")
}

func TestCommentStrategySkipsCRDManifests(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-crd", t)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ApplicationPath: "apps/crd.yaml",
	}

	diff := "--- a/crd.yaml\n+++ b/crd.yaml\n+ kind: CustomResourceDefinition\n+ metadata: {}"
	result := ComparisonResult{
		Changed: []DiffOutput{{File: File{Name: "/crds/crd.yaml"}, Diff: diff}},
	}

	require.NoError(t, strategy.Present(result))
	require.Len(t, poster.bodies, 1)
	body := poster.bodies[0]
	assert.Contains(t, body, "**CRD Notes**")
	assert.Contains(t, body, "CRD manifest `crds/crd.yaml`")
	assert.Contains(t, body, "Diff omitted")
	assert.NotContains(t, body, "kind: CustomResourceDefinition")
	if strings.Contains(body, "</details>") {
		assert.True(t, strings.Index(body, "**CRD Notes**") > strings.LastIndex(body, "</details>"), "CRD notes should appear after diff sections")
	}
}

func TestCommentStrategyIgnoresNonCRDManifests(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-non-crd", t)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ApplicationPath: "apps/credentials.yaml",
	}

	diff := "--- a/config/credentials.yaml\n+++ b/config/credentials.yaml\n+ username: demo"
	result := ComparisonResult{
		Changed: []DiffOutput{{File: File{Name: "/config/credentials.yaml"}, Diff: diff}},
	}

	require.NoError(t, strategy.Present(result))
	require.Len(t, poster.bodies, 1)
	body := poster.bodies[0]
	assert.Contains(t, body, "credentials.yaml")
	assert.NotContains(t, body, "CRD Notes")
}
