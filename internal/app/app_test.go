package app

import (
	"context"
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

func (p *testPoster) Post(_ context.Context, _ string) error {
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

func TestSelectDiffStrategiesNilPosterFromFactory(t *testing.T) {
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

	logger := setupTestLogger(t, "app-select-nil")

	appInstance, err := New(cfg, Dependencies{
		FS:                   afero.NewMemMapFs(),
		Logger:               logger,
		CommentPosterFactory: func(Config) (comment.Poster, error) { return nil, nil },
	})
	require.NoError(t, err)

	_, err = appInstance.selectDiffStrategies("apps/foo.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "comment poster factory returned nil")
}

func TestSelectDiffStrategiesWithExternalTool(t *testing.T) {
	cfg, err := NewConfig("main",
		WithCacheDir("/tmp/cache"),
		WithExternalDiffTool("diff"),
	)
	require.NoError(t, err)

	logger := setupTestLogger(t, "app-select-external")

	appInstance, err := New(cfg, Dependencies{
		FS:     afero.NewMemMapFs(),
		Logger: logger,
	})
	require.NoError(t, err)

	strategies, err := appInstance.selectDiffStrategies("apps/foo.yaml")
	require.NoError(t, err)
	require.Len(t, strategies, 1)

	_, isExternal := strategies[0].(ExternalDiffStrategy)
	assert.True(t, isExternal)
}

func TestReportInvalidFilesEmpty(t *testing.T) {
	cfg, err := NewConfig("main", WithCacheDir("/tmp/cache"))
	require.NoError(t, err)

	logger := setupTestLogger(t, "app-report-empty")

	appInstance, err := New(cfg, Dependencies{
		FS:     afero.NewMemMapFs(),
		Logger: logger,
	})
	require.NoError(t, err)

	err = appInstance.reportInvalidFiles(nil)
	require.NoError(t, err)

	err = appInstance.reportInvalidFiles([]string{})
	require.NoError(t, err)
}

func TestReportInvalidFilesWithFiles(t *testing.T) {
	cfg, err := NewConfig("main", WithCacheDir("/tmp/cache"))
	require.NoError(t, err)

	logger := setupTestLogger(t, "app-report-files")

	appInstance, err := New(cfg, Dependencies{
		FS:     afero.NewMemMapFs(),
		Logger: logger,
	})
	require.NoError(t, err)

	err = appInstance.reportInvalidFiles([]string{"invalid1.yaml", "invalid2.yaml"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid files found")
}

func TestCollectRepoCredentials(t *testing.T) {
	cfg, err := NewConfig("main", WithCacheDir("/tmp/cache"))
	require.NoError(t, err)

	logger := setupTestLogger(t, "app-creds")

	appInstance, err := New(cfg, Dependencies{
		FS:     afero.NewMemMapFs(),
		Logger: logger,
	})
	require.NoError(t, err)

	// Test with no credentials set
	err = appInstance.collectRepoCredentials()
	require.NoError(t, err)
	assert.Empty(t, appInstance.repoCredentials)
}

func TestCollectRepoCredentialsWithEnvVars(t *testing.T) {
	cfg, err := NewConfig("main", WithCacheDir("/tmp/cache"))
	require.NoError(t, err)

	logger := setupTestLogger(t, "app-creds-env")

	appInstance, err := New(cfg, Dependencies{
		FS:     afero.NewMemMapFs(),
		Logger: logger,
	})
	require.NoError(t, err)

	// Set test environment variable
	t.Setenv("REPO_CREDS_TEST", `{"url":"https://example.com","username":"user","password":"pass"}`)

	err = appInstance.collectRepoCredentials()
	require.NoError(t, err)
	require.Len(t, appInstance.repoCredentials, 1)
	assert.Equal(t, "https://example.com", appInstance.repoCredentials[0].Url)
}

func TestCollectRepoCredentialsInvalidJSON(t *testing.T) {
	cfg, err := NewConfig("main", WithCacheDir("/tmp/cache"))
	require.NoError(t, err)

	logger := setupTestLogger(t, "app-creds-invalid")

	appInstance, err := New(cfg, Dependencies{
		FS:     afero.NewMemMapFs(),
		Logger: logger,
	})
	require.NoError(t, err)

	// Set invalid JSON environment variable
	t.Setenv("REPO_CREDS_INVALID", `not valid json`)

	err = appInstance.collectRepoCredentials()
	require.Error(t, err)
}

func TestDefaultCommentPosterFactoryNilConfig(t *testing.T) {
	cfg := Config{Comment: nil}

	_, err := defaultCommentPosterFactory(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil comment configuration")
}

func TestDefaultCommentPosterFactoryProviderNone(t *testing.T) {
	cfg := Config{Comment: &CommentConfig{Provider: CommentProviderNone}}

	_, err := defaultCommentPosterFactory(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "comment provider")
}

func TestDefaultCommentPosterFactoryUnsupportedProvider(t *testing.T) {
	cfg := Config{Comment: &CommentConfig{Provider: "unsupported"}}

	_, err := defaultCommentPosterFactory(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported comment provider")
}

func TestFilterIgnoredEmpty(t *testing.T) {
	files := []string{"a.yaml", "b.yaml"}

	result := filterIgnored(files, nil)
	assert.Equal(t, files, result)

	result = filterIgnored(files, []string{})
	assert.Equal(t, files, result)
}

func TestDefaultCommentPosterFactoryGitLab(t *testing.T) {
	cfg := Config{
		Comment: &CommentConfig{
			Provider: CommentProviderGitLab,
			GitLab: GitLabCommentConfig{
				BaseURL:         "https://gitlab.example.com",
				Token:           "token",
				ProjectID:       "123",
				MergeRequestIID: 456,
			},
		},
	}

	poster, err := defaultCommentPosterFactory(cfg)
	require.NoError(t, err)
	require.NotNil(t, poster)
}
