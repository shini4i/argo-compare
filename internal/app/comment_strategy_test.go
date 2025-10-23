package app

import (
	"io"
	"os"
	"testing"

	"github.com/op/go-logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubPoster struct {
	body string
	err  error
}

func (s *stubPoster) Post(body string) error {
	s.body = body
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
		Log:         logger,
		Poster:      poster,
		ShowAdded:   true,
		ShowRemoved: true,
	}

	result := ComparisonResult{
		Added: []DiffOutput{
			{File: File{Name: "/added.yaml"}, Diff: "+ added"},
		},
		Changed: []DiffOutput{
			{File: File{Name: "changed.yaml"}, Diff: "@@ diff"},
		},
		Removed: []DiffOutput{
			{File: File{Name: "/removed.yaml"}, Diff: "- removed"},
		},
	}

	require.NoError(t, strategy.Present(result))

	assert.Contains(t, poster.body, "## Argo Compare Results")
	assert.Contains(t, poster.body, "### Added (1)")
	assert.Contains(t, poster.body, "```diff")
	assert.Contains(t, poster.body, "added.yaml")
	assert.Contains(t, poster.body, "@@ diff")
}

func TestCommentStrategyPresentNoDiff(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-empty", t)

	strategy := CommentStrategy{
		Log:    logger,
		Poster: poster,
	}

	require.NoError(t, strategy.Present(ComparisonResult{}))
	assert.Contains(t, poster.body, "No manifest differences detected")
}

func TestCommentStrategyRequiresPoster(t *testing.T) {
	logger := setupSilentLogger("comment-missing", t)

	strategy := CommentStrategy{
		Log: logger,
	}

	err := strategy.Present(ComparisonResult{})
	require.Error(t, err)
}

func TestCommentStrategyRequiresLogger(t *testing.T) {
	strategy := CommentStrategy{
		Poster: &stubPoster{},
	}

	err := strategy.Present(ComparisonResult{})
	require.Error(t, err)
}
