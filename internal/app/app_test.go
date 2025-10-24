package app

import (
	"io"
	"os"
	"testing"

	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/internal/comment"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterIgnored(t *testing.T) {
	files := []string{"a.yaml", "b.yaml", "c.yaml"}
	ignored := []string{"b.yaml"}

	result := filterIgnored(files, ignored)

	assert.Equal(t, []string{"a.yaml", "c.yaml"}, result)
}

type testPoster struct{}

func (p *testPoster) Post(string) error {
	return nil
}

func setupTestLogger(t *testing.T, name string) *logging.Logger {
	logger := logging.MustGetLogger(name)
	logging.SetBackend(logging.NewLogBackend(io.Discard, "", 0))
	t.Cleanup(func() {
		logging.SetBackend(logging.NewLogBackend(os.Stdout, "", 0))
	})
	return logger
}

func TestSelectDiffStrategiesIncludesCommentStrategy(t *testing.T) {
	cfg, err := NewConfig("main",
		WithCacheDir("/tmp/cache"),
		WithCommentConfig(CommentConfig{
			Provider: CommentProviderGitLab,
			GitLab: GitLabCommentConfig{
				BaseURL:         "https://gitlab.example.com",
				Token:           "token",
				ProjectID:       "1",
				MergeRequestIID: 101,
			},
		}),
	)
	require.NoError(t, err)

	logger := setupTestLogger(t, "app-select")

	appInstance, err := New(cfg, Dependencies{
		FS:                   afero.NewMemMapFs(),
		Logger:               logger,
		CommentPosterFactory: func(Config) (comment.Poster, error) { return &testPoster{}, nil },
	})
	require.NoError(t, err)

	strategies, err := appInstance.selectDiffStrategies("apps/foo.yaml")
	require.NoError(t, err)
	require.Len(t, strategies, 2)

	_, isStdout := strategies[0].(StdoutStrategy)
	assert.True(t, isStdout)
	_, isComment := strategies[1].(CommentStrategy)
	assert.True(t, isComment)
}

func TestSelectDiffStrategiesErrorFromFactory(t *testing.T) {
	cfg, err := NewConfig("main",
		WithCacheDir("/tmp/cache"),
		WithCommentConfig(CommentConfig{
			Provider: CommentProviderGitLab,
			GitLab: GitLabCommentConfig{
				BaseURL:         "https://gitlab.example.com",
				Token:           "token",
				ProjectID:       "1",
				MergeRequestIID: 101,
			},
		}),
	)
	require.NoError(t, err)

	logger := setupTestLogger(t, "app-select-error")

	appInstance, err := New(cfg, Dependencies{
		FS:                   afero.NewMemMapFs(),
		Logger:               logger,
		CommentPosterFactory: func(Config) (comment.Poster, error) { return nil, assert.AnError },
	})
	require.NoError(t, err)

	_, err = appInstance.selectDiffStrategies("apps/foo.yaml")
	require.Error(t, err)
}
