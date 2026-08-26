package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAppRunFailsWhenTheCommentCannotBePosted proves a rejected note fails the
// run instead of reporting success over an empty merge request.
func TestAppRunFailsWhenTheCommentCannotBePosted(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepo(t,
		applicationSetYAML([][2]string{{"dev", "1.0.0"}}),
		applicationSetYAML([][2]string{{"dev", "1.1.0"}}))

	poster := &stubPoster{err: assert.AnError}
	runner := newAppSetRunnerWith(t, Config{Comment: commentCfg()}, nil, poster)

	err := runner.app.Run(context.Background())

	require.ErrorIs(t, err, assert.AnError)
	assert.Contains(t, err.Error(), "post diff comment")
	assert.Len(t, poster.bodies, 1, "the note is attempted once, not retried")
}

// TestAppRunPublishesWhatItComparedBeforeAFailure is the reordering this change
// hinges on: comparisons no longer publish as they go, so a later failure must
// not cost the reviewer the results already gathered.
func TestAppRunPublishesWhatItComparedBeforeAFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	elements := [][2]string{{"alpha", "1.0.0"}, {"beta", "1.0.0"}, {"gamma", "1.0.0"}}
	bumped := [][2]string{{"alpha", "1.1.0"}, {"beta", "1.1.0"}, {"gamma", "1.1.0"}}
	seedAppSetRepo(t, applicationSetYAML(elements), applicationSetYAML(bumped))

	poster := &stubPoster{}
	runner := newAppSetRunnerWith(t, Config{Comment: commentCfg()}, nil, poster)
	runner.helm.renderErrFor = map[string]error{"gamma-guestbook": assert.AnError}

	err := runner.app.Run(context.Background())

	require.ErrorIs(t, err, assert.AnError, "the comparison failure is still reported")
	require.Len(t, poster.bodies, 1, "the partial batch is published")

	body := poster.bodies[0]
	assert.Contains(t, body, "alpha-guestbook")
	assert.Contains(t, body, "beta-guestbook")
	assert.NotContains(t, body, "**Application:** `"+generatedLabel(appSetPath, "gamma-guestbook")+"`")
}

// TestAppRunPrefersTheComparisonErrorOverTheCommentError proves the root cause
// wins when both fail, with the publishing failure still surfaced in the log.
func TestAppRunPrefersTheComparisonErrorOverTheCommentError(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	seedAppSetRepo(t,
		applicationSetYAML([][2]string{{"alpha", "1.0.0"}, {"beta", "1.0.0"}}),
		applicationSetYAML([][2]string{{"alpha", "1.1.0"}, {"beta", "1.1.0"}}))

	postErr := fmt.Errorf("gitlab rejected the note")
	poster := &stubPoster{err: postErr}
	runner := newAppSetRunnerWith(t, Config{Comment: commentCfg()}, nil, poster)
	runner.helm.renderErrFor = map[string]error{"beta-guestbook": assert.AnError}

	err := runner.app.Run(context.Background())

	require.ErrorIs(t, err, assert.AnError, "the comparison error is the root cause")
	assert.NotErrorIs(t, err, postErr)
	assert.Contains(t, runner.log.String(), "Failed to publish the comparison comment")
}

// TestAppRunPostsNothingWithoutComparisons proves a run that compares nothing
// stays silent rather than posting an empty note. The branches differ only in a
// non-manifest file, so there is a diff but no Application to compare.
func TestAppRunPostsNothingWithoutComparisons(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	manifest := applicationSetYAML([][2]string{{"dev", "1.0.0"}})
	seedAppSetRepoState(t,
		branchState{manifest: manifest, files: map[string]string{"docs/notes.md": "before\n"}},
		branchState{manifest: manifest, files: map[string]string{"docs/notes.md": "after\n"}})

	poster := &stubPoster{}
	runner := newAppSetRunnerWith(t, Config{Comment: commentCfg()}, nil, poster)

	require.NoError(t, runner.app.Run(context.Background()))
	assert.Empty(t, poster.bodies)
}

// TestCommentStrategyAbandonsRemainingPartsAfterAFailure proves a failed part
// stops the batch, so a half-published note does not keep growing.
func TestCommentStrategyAbandonsRemainingPartsAfterAFailure(t *testing.T) {
	poster := &stubPoster{err: assert.AnError, failOnCall: 2}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-part-failure", t), Poster: poster, ShowAdded: true}

	largeDiff := strings.Repeat("+ oversized line\n", 160000)
	sections := []commentSection{
		{label: "big-one", result: ComparisonResult{Added: []DiffOutput{{File: File{Name: "a.yaml"}, Diff: largeDiff}}}},
		{label: "big-two", result: ComparisonResult{Added: []DiffOutput{{File: File{Name: "b.yaml"}, Diff: largeDiff}}}},
	}

	err := strategy.PresentSections(context.Background(), sections)

	require.ErrorIs(t, err, assert.AnError)
	assert.Contains(t, err.Error(), "part 2/")
	assert.Len(t, poster.bodies, 2, "parts after the failure are not attempted")
}

// TestCommentStrategySingleBodyFailureNamesNoPart proves a lone note's error
// carries no part numbering, which would be meaningless.
func TestCommentStrategySingleBodyFailureNamesNoPart(t *testing.T) {
	poster := &stubPoster{err: assert.AnError}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-single-failure", t), Poster: poster}

	err := strategy.PresentSections(context.Background(), []commentSection{changedSection("app", "a.yaml")})

	require.ErrorIs(t, err, assert.AnError)
	assert.Contains(t, err.Error(), "post diff comment:")
	assert.NotContains(t, err.Error(), "part")
}
