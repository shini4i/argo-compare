package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// changedSection is one Application's result carrying a single changed file.
func changedSection(label, file string) commentSection {
	return commentSection{
		label: label,
		result: ComparisonResult{
			Changed: []DiffOutput{{File: File{Name: file}, Diff: "--- a\n+++ b\n@@ diff\n- old\n+ new"}},
		},
	}
}

// TestCommentStrategyBatchesSections is the reason batching exists: an
// ApplicationSet generating many Applications must produce one note, not one
// per Application.
func TestCommentStrategyBatchesSections(t *testing.T) {
	poster := &stubPoster{}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-batch", t), Poster: poster}

	sections := []commentSection{
		changedSection("apps/appset.yaml [dev-guestbook]", "dev.yaml"),
		changedSection("apps/appset.yaml [prod-guestbook]", "prod.yaml"),
	}

	require.NoError(t, strategy.PresentSections(context.Background(), sections))

	require.Len(t, poster.bodies, 1, "every generated Application belongs to one note")
	body := poster.bodies[0]
	assert.Equal(t, 1, strings.Count(body, "## Argo Compare Results"), "the run header appears once")
	assert.Contains(t, body, "**Application:** `apps/appset.yaml [dev-guestbook]`")
	assert.Contains(t, body, "**Application:** `apps/appset.yaml [prod-guestbook]`")
	assert.NotContains(t, body, "Part 1 of")

	// Each Application's diffs must sit between its own heading and the next,
	// or the note would attribute one Application's change to another.
	for _, want := range []string{"<summary>Changed • dev.yaml</summary>", "<summary>Changed • prod.yaml</summary>"} {
		require.Contains(t, body, want)
	}
	assert.Less(t, strings.Index(body, "[dev-guestbook]"), strings.Index(body, "dev.yaml"))
	assert.Less(t, strings.Index(body, "dev.yaml"), strings.Index(body, "[prod-guestbook]"))
	assert.Less(t, strings.Index(body, "[prod-guestbook]"), strings.Index(body, "prod.yaml"))
}

// TestCommentStrategyBatchKeepsSectionOrder pins the order to the order the
// comparisons ran, so the note reads the same way as the log.
func TestCommentStrategyBatchKeepsSectionOrder(t *testing.T) {
	poster := &stubPoster{}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-batch-order", t), Poster: poster}

	sections := []commentSection{
		changedSection("b-app", "b.yaml"),
		changedSection("a-app", "a.yaml"),
	}

	require.NoError(t, strategy.PresentSections(context.Background(), sections))

	body := poster.bodies[0]
	require.Contains(t, body, "`b-app`")
	require.Contains(t, body, "`a-app`")
	assert.Less(t, strings.Index(body, "`b-app`"), strings.Index(body, "`a-app`"))
}

// TestCommentStrategyBatchReportsEmptySections proves an Application with no
// differences is still named, rather than silently missing from the note.
func TestCommentStrategyBatchReportsEmptySections(t *testing.T) {
	poster := &stubPoster{}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-batch-empty", t), Poster: poster}

	sections := []commentSection{
		{label: "quiet-app"},
		changedSection("busy-app", "busy.yaml"),
	}

	require.NoError(t, strategy.PresentSections(context.Background(), sections))

	require.Len(t, poster.bodies, 1)
	body := poster.bodies[0]
	assert.Contains(t, body, "**Application:** `quiet-app`")
	assert.Contains(t, body, "No manifest differences detected")
	assert.Contains(t, body, "<summary>Changed • busy.yaml</summary>")
}

func TestCommentStrategyBatchAllSectionsEmpty(t *testing.T) {
	poster := &stubPoster{}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-batch-all-empty", t), Poster: poster}

	sections := []commentSection{{label: "one"}, {label: "two"}}

	require.NoError(t, strategy.PresentSections(context.Background(), sections))

	require.Len(t, poster.bodies, 1)
	assert.Equal(t, 2, strings.Count(poster.bodies[0], "No manifest differences detected"))
}

// TestCommentStrategyBatchSplitsAcrossNotes proves batching still respects the
// note size limit, and that each part carries the run header.
func TestCommentStrategyBatchSplitsAcrossNotes(t *testing.T) {
	poster := &stubPoster{}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-batch-large", t), Poster: poster, ShowAdded: true}

	largeDiff := strings.Repeat("+ oversized line\n", 160000)
	sections := []commentSection{
		{label: "big-one", result: ComparisonResult{Added: []DiffOutput{{File: File{Name: "a.yaml"}, Diff: largeDiff}}}},
		{label: "big-two", result: ComparisonResult{Added: []DiffOutput{{File: File{Name: "b.yaml"}, Diff: largeDiff}}}},
	}

	require.NoError(t, strategy.PresentSections(context.Background(), sections))

	require.Greater(t, len(poster.bodies), 1)
	total := len(poster.bodies)
	for idx, body := range poster.bodies {
		assert.Contains(t, body, "## Argo Compare Results", "part %d must carry the run header", idx+1)
		assert.LessOrEqual(t, len(body), gitlabNoteLengthLimit, "part %d must fit GitLab's note limit", idx+1)
	}
	assert.Contains(t, poster.bodies[0], "Part 1 of "+fmt.Sprint(total))
	assert.Contains(t, poster.bodies[total-1], fmt.Sprintf("Part %d of %d", total, total))
}

// TestCommentStrategyBatchCarriesValidationPerSection proves each Application's
// validation result stays attached to its own section.
func TestCommentStrategyBatchCarriesValidationPerSection(t *testing.T) {
	poster := &stubPoster{}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-batch-validation", t), Poster: poster}

	failing := changedSection("bad-app", "bad.yaml")
	failing.result.ValidationResults = map[string]ports.ValidationResult{
		"src": {Target: "src", Valid: false, ResourceCount: 2, ErrorCount: 1},
	}
	passing := changedSection("good-app", "good.yaml")
	passing.result.ValidationResults = map[string]ports.ValidationResult{
		"src": {Target: "src", Valid: true, ResourceCount: 3},
	}

	require.NoError(t, strategy.PresentSections(context.Background(), []commentSection{failing, passing}))

	body := poster.bodies[0]
	require.Contains(t, body, "1/2 valid")
	require.Contains(t, body, "3/3 valid")
	require.Contains(t, body, "`good-app`")
	assert.Less(t, strings.Index(body, "1/2 valid"), strings.Index(body, "`good-app`"))
}

func TestCommentStrategyPresentSectionsWithoutPoster(t *testing.T) {
	strategy := CommentStrategy{Log: setupSilentLogger("comment-batch-nil", t)}

	require.Error(t, strategy.PresentSections(context.Background(), []commentSection{{label: "x"}}))
}

// TestCommentStrategyPresentSectionsNoSections proves an empty batch posts
// nothing at all, so a run with no comparisons stays silent.
func TestCommentStrategyPresentSectionsNoSections(t *testing.T) {
	poster := &stubPoster{}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-batch-none", t), Poster: poster}

	require.NoError(t, strategy.PresentSections(context.Background(), nil))
	assert.Empty(t, poster.bodies)
}

// commentCfg enables GitLab comments with throwaway connection details; the
// poster is stubbed, so none of them are used.
func commentCfg() *CommentConfig {
	return &CommentConfig{
		Provider: CommentProviderGitLab,
		GitLab: GitLabCommentConfig{
			BaseURL:         "https://gitlab.example.com",
			Token:           "token",
			ProjectID:       "1",
			MergeRequestIID: 101,
		},
	}
}

// TestAppRunPostsOneCommentForAnApplicationSet is the problem this batching
// solves: a 30-element ApplicationSet used to post 30 merge request notes.
func TestAppRunPostsOneCommentForAnApplicationSet(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	elements := make([][2]string, 0, 6)
	for i := range 6 {
		elements = append(elements, [2]string{fmt.Sprintf("cluster%d", i), "1.0.0"})
	}
	bumped := make([][2]string, len(elements))
	copy(bumped, elements)
	for i := range bumped {
		bumped[i][1] = "1.1.0"
	}

	seedAppSetRepo(t, applicationSetYAML(elements), applicationSetYAML(bumped))

	poster := &stubPoster{}
	runner := newAppSetRunnerWith(t, Config{Comment: commentCfg()}, nil, poster)
	require.NoError(t, runner.app.Run(context.Background()))

	require.Len(t, poster.bodies, 1, "six generated Applications must produce one note")
	body := poster.bodies[0]
	assert.Equal(t, 6, strings.Count(body, "**Application:**"), "one heading per generated Application")
	for i := range 6 {
		assert.Contains(t, body, fmt.Sprintf("**Application:** `%s`",
			generatedLabel(appSetPath, fmt.Sprintf("cluster%d-guestbook", i))))
	}
	assert.Equal(t, 1, strings.Count(body, "## Argo Compare Results"))
}

// TestAppRunPostsOneCommentForAnAnchoredApplicationSet covers the other way a
// run fans out to many Applications: an anchor naming an ApplicationSet.
func TestAppRunPostsOneCommentForAnAnchoredApplicationSet(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepoState(t,
		branchState{manifest: anchoredAppSetYAML, files: anchoredChartFiles("1")},
		branchState{manifest: anchoredAppSetYAML, files: anchoredChartFiles("7")})

	poster := &stubPoster{}
	runner := newAppSetRunnerWith(t, Config{AnchorFileName: DefaultAnchorFileName, Comment: commentCfg()}, nil, poster)
	require.NoError(t, runner.app.Run(context.Background()))

	require.Len(t, poster.bodies, 1, "both generated Applications belong to one note")
	body := poster.bodies[0]
	assert.Equal(t, 2, strings.Count(body, "**Application:**"))
	assert.Contains(t, body, "**Application:** `"+generatedLabel(appSetPath, "dev-demo")+"`")
	assert.Contains(t, body, "**Application:** `"+generatedLabel(appSetPath, "prod-demo")+"`")
}

// commentLabels returns the Application headings a comment body carries.
func commentLabels(body string) []string {
	var labels []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "**Application:**") {
			labels = append(labels, strings.TrimSpace(line))
		}
	}

	return labels
}

// TestCommentStrategyBatchAttributesEverySplitNote guards the shape batching
// makes routine: many Applications share one note budget, so a comment starting
// partway through one must still name it, and must never open with one
// Application's diff under another's heading.
func TestCommentStrategyBatchAttributesEverySplitNote(t *testing.T) {
	poster := &stubPoster{}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-batch-attribution", t), Poster: poster, ShowAdded: true}

	largeDiff := strings.Repeat("+ oversized line\n", 160000)
	sections := []commentSection{
		{label: "first-app", result: ComparisonResult{Added: []DiffOutput{{File: File{Name: "a.yaml"}, Diff: largeDiff}}}},
		{label: "second-app", result: ComparisonResult{Added: []DiffOutput{{File: File{Name: "b.yaml"}, Diff: largeDiff}}}},
	}

	require.NoError(t, strategy.PresentSections(context.Background(), sections))
	require.Greater(t, len(poster.bodies), len(sections), "the batch must actually split for this to mean anything")

	var order []string
	for idx, body := range poster.bodies {
		labels := commentLabels(body)
		require.Len(t, labels, 1, "note %d must name exactly one Application", idx+1)
		assert.Contains(t, body, "oversized line", "note %d must carry diff content, not a heading alone", idx+1)

		label := labels[0]
		switch {
		case strings.Contains(label, "first-app"):
			order = append(order, "first")
		case strings.Contains(label, "second-app"):
			order = append(order, "second")
		default:
			t.Fatalf("note %d names an unexpected Application: %s", idx+1, label)
		}
	}

	// Each Application's notes stay together, so a reader never has to stitch
	// one Application's diff back together across another's notes.
	assert.Equal(t, "first", order[0])
	assert.Equal(t, "second", order[len(order)-1])
	assert.Equal(t, 1, strings.Count(strings.Join(order, ","), "first,second"), "notes must not interleave")
}
