package app

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// crdSection is one Application whose only change is a CRD, which the comment
// reports as a notice instead of a diff.
func crdSection(label, file string) commentSection {
	return commentSection{
		label: label,
		result: ComparisonResult{
			Changed: []DiffOutput{{
				File: File{Name: file},
				Diff: "--- a\n+++ b\n@@ diff\n+ kind: CustomResourceDefinition",
			}},
		},
	}
}

// TestCommentStrategyBatchKeepsCRDNotesPerSection proves each Application's CRD
// notices stay with that Application rather than merging into one block, which
// would leave the reader unable to tell which Application omitted what.
func TestCommentStrategyBatchKeepsCRDNotesPerSection(t *testing.T) {
	poster := &stubPoster{}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-batch-crd", t), Poster: poster}

	sections := []commentSection{
		crdSection("first-app", "first-crd.yaml"),
		crdSection("second-app", "second-crd.yaml"),
	}

	require.NoError(t, strategy.PresentSections(context.Background(), sections))
	require.Len(t, poster.bodies, 1)

	body := poster.bodies[0]
	assert.Equal(t, 2, strings.Count(body, "**CRD Notes**"), "one block per Application")
	assert.Contains(t, body, "first-crd.yaml")
	assert.Contains(t, body, "second-crd.yaml")

	// The first Application's notice belongs before the second's heading.
	assert.Less(t, strings.Index(body, "first-crd.yaml"), strings.Index(body, "`second-app`"))
}

// TestCommentStrategyBatchNotesHiddenSectionsPerApplication proves the
// "not shown" accounting is per Application, so a reader can see which one is
// hiding manifests.
func TestCommentStrategyBatchNotesHiddenSectionsPerApplication(t *testing.T) {
	poster := &stubPoster{}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-batch-hidden", t), Poster: poster}

	hidden := func(label string) commentSection {
		return commentSection{
			label: label,
			result: ComparisonResult{
				Added:   []DiffOutput{{File: File{Name: "added.yaml"}, Diff: "+ added"}},
				Removed: []DiffOutput{{File: File{Name: "removed.yaml"}, Diff: "- removed"}},
			},
		}
	}

	sections := []commentSection{hidden("first-app"), hidden("second-app")}

	require.NoError(t, strategy.PresentSections(context.Background(), sections))

	body := poster.bodies[0]
	assert.Equal(t, 4, strings.Count(body, "are present but not shown"), "added and removed, per Application")
	assert.Equal(t, 4, strings.Count(body, "(not shown)"))
	assert.NotContains(t, body, "+ added", "a hidden diff must not leak into the note")
	assert.NotContains(t, body, "- removed")
}

// TestCommentStrategyBatchRendersAllThreeSectionsInOneApplication proves added,
// removed and changed diffs for one Application all land inside its own span.
func TestCommentStrategyBatchRendersAllThreeSectionsInOneApplication(t *testing.T) {
	poster := &stubPoster{}
	strategy := CommentStrategy{
		Log:         setupSilentLogger("comment-batch-mixed", t),
		Poster:      poster,
		ShowAdded:   true,
		ShowRemoved: true,
	}

	mixed := commentSection{
		label: "mixed-app",
		result: ComparisonResult{
			Added:   []DiffOutput{{File: File{Name: "added.yaml"}, Diff: "+ added"}},
			Removed: []DiffOutput{{File: File{Name: "removed.yaml"}, Diff: "- removed"}},
			Changed: []DiffOutput{{File: File{Name: "changed.yaml"}, Diff: "@@ diff\n- old\n+ new"}},
		},
	}

	require.NoError(t, strategy.PresentSections(context.Background(),
		[]commentSection{mixed, changedSection("later-app", "later.yaml")}))

	body := poster.bodies[0]
	for _, want := range []string{
		"<summary>Added • added.yaml</summary>",
		"<summary>Removed • removed.yaml</summary>",
		"<summary>Changed • changed.yaml</summary>",
	} {
		require.Contains(t, body, want)
		assert.Less(t, strings.Index(body, want), strings.Index(body, "`later-app`"),
			"%s belongs to mixed-app, before the next Application", want)
	}
}

// TestCommentStrategyNamesAnUnlabelledApplication covers the fallback for a
// comparison that had no path to report.
func TestCommentStrategyNamesAnUnlabelledApplication(t *testing.T) {
	poster := &stubPoster{}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-unknown", t), Poster: poster}

	require.NoError(t, strategy.Present(context.Background(), ComparisonResult{}))

	assert.Contains(t, poster.bodies[0], "**Application:** `unknown`")
}

// TestAppRunDoesNotRepublishAnEarlierRun proves a second Run publishes only its
// own comparisons, so a reused App cannot double-report.
func TestAppRunDoesNotRepublishAnEarlierRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepo(t,
		applicationSetYAML([][2]string{{"dev", "1.0.0"}, {"prod", "1.0.0"}}),
		applicationSetYAML([][2]string{{"dev", "1.1.0"}, {"prod", "1.1.0"}}))

	poster := &stubPoster{}
	runner := newAppSetRunnerWith(t, Config{Comment: commentCfg()}, nil, poster)

	require.NoError(t, runner.app.Run(context.Background()))
	require.NoError(t, runner.app.Run(context.Background()))

	require.Len(t, poster.bodies, 2, "one note per run")
	assert.Equal(t,
		strings.Count(poster.bodies[0], "**Application:**"),
		strings.Count(poster.bodies[1], "**Application:**"),
		"the second run must not carry the first run's sections")
}
