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

// hugeValidationResult builds a validation summary far larger than a single
// comment can hold, as a chart with thousands of schema failures would.
func hugeValidationResult(entries int) map[string]ports.ValidationResult {
	errs := make([]ports.ValidationError, 0, entries)
	for i := range entries {
		errs = append(errs, ports.ValidationError{
			Kind:     "Deployment",
			Name:     fmt.Sprintf("workload-%d", i),
			Filename: fmt.Sprintf("templates/deployment-%d.yaml", i),
			Message:  strings.Repeat("field is invalid; ", 20),
		})
	}

	return map[string]ports.ValidationResult{
		"src": {Target: "src", Valid: false, ResourceCount: entries, ErrorCount: entries, Errors: errs},
	}
}

// TestCommentStrategyTruncatesAnOversizedPreamble proves one Application's
// runaway validation output cannot produce a comment GitLab would reject.
func TestCommentStrategyTruncatesAnOversizedPreamble(t *testing.T) {
	poster := &stubPoster{}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-oversized", t), Poster: poster}

	oversized := changedSection("noisy-app", "noisy.yaml")
	oversized.result.ValidationResults = hugeValidationResult(20000)

	require.NoError(t, strategy.PresentSections(context.Background(), []commentSection{oversized}))

	require.NotEmpty(t, poster.bodies)
	for idx, body := range poster.bodies {
		assert.LessOrEqual(t, len(body), gitlabNoteLengthLimit, "comment %d must fit the limit", idx+1)
	}

	joined := strings.Join(poster.bodies, "")
	assert.Contains(t, joined, "**Application:** `noisy-app`")
	assert.Contains(t, joined, "Review the job logs for full details")
}

// TestCommentStrategyOversizedSectionKeepsOthersPublishable is why the bound
// matters under batching: a rejected comment abandons the ones behind it, so an
// oversized Application must not take its neighbours down.
func TestCommentStrategyOversizedSectionKeepsOthersPublishable(t *testing.T) {
	poster := &stubPoster{}
	strategy := CommentStrategy{Log: setupSilentLogger("comment-oversized-batch", t), Poster: poster}

	oversized := changedSection("noisy-app", "noisy.yaml")
	oversized.result.ValidationResults = hugeValidationResult(20000)

	sections := []commentSection{oversized, changedSection("quiet-app", "quiet.yaml")}
	require.NoError(t, strategy.PresentSections(context.Background(), sections))

	for idx, body := range poster.bodies {
		require.LessOrEqual(t, len(body), gitlabNoteLengthLimit, "comment %d must fit the limit", idx+1)
	}

	joined := strings.Join(poster.bodies, "")
	assert.Contains(t, joined, "**Application:** `quiet-app`", "the neighbour is still reported")
	assert.Contains(t, joined, "<summary>Changed • quiet.yaml</summary>")
}

func TestTruncatePreamble(t *testing.T) {
	t.Run("leaves a preamble that fits untouched", func(t *testing.T) {
		preamble := "**Application:** `app`\n\n**Summary**\n- Changed: 1\n\n"

		assert.Equal(t, preamble, truncatePreamble(preamble, len(preamble)))
	})

	t.Run("cuts on a line boundary and names the omission", func(t *testing.T) {
		preamble := "**Application:** `app`\n\n" + strings.Repeat("- a failing resource\n", 200)

		got := truncatePreamble(preamble, len(preambleTruncationNotice)+200)

		assert.LessOrEqual(t, len(got), len(preambleTruncationNotice)+200)
		assert.True(t, strings.HasSuffix(got, preambleTruncationNotice))
		assert.Contains(t, got, "**Application:** `app`", "the Application must survive truncation")
		assert.NotContains(t, strings.TrimSuffix(got, preambleTruncationNotice), "resource-")
	})

	t.Run("still names the omission when nothing fits", func(t *testing.T) {
		got := truncatePreamble(strings.Repeat("x\n", 500), 10)

		assert.Contains(t, got, "Review the job logs")
	})
}
