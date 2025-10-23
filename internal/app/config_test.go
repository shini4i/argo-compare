package app

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfigDefaults(t *testing.T) {
	cfg, err := NewConfig("main")
	require.NoError(t, err)

	assert.Equal(t, "main", cfg.TargetBranch)
	assert.Equal(t, os.TempDir(), cfg.TempDirBase)
	assert.False(t, cfg.PreserveHelmLabels)
	assert.False(t, cfg.PrintAddedManifests)
	assert.False(t, cfg.PrintRemovedManifests)
}

func TestNewConfigWithOptions(t *testing.T) {
	cfg, err := NewConfig(
		"feature",
		WithFileToCompare("app.yaml"),
		WithFilesToIgnore([]string{"ignore.yaml"}),
		WithPreserveHelmLabels(true),
		WithPrintAdded(true),
		WithPrintRemoved(true),
		WithCacheDir("/tmp/cache"),
		WithTempDirBase("/tmp/work"),
		WithExternalDiffTool("diff-tool"),
		WithDebug(true),
		WithVersion("1.2.3"),
	)
	require.NoError(t, err)

	assert.Equal(t, "app.yaml", cfg.FileToCompare)
	assert.Equal(t, []string{"ignore.yaml"}, cfg.FilesToIgnore)
	assert.True(t, cfg.PreserveHelmLabels)
	assert.True(t, cfg.PrintAddedManifests)
	assert.True(t, cfg.PrintRemovedManifests)
	assert.Equal(t, "/tmp/cache", cfg.CacheDir)
	assert.Equal(t, "/tmp/work", cfg.TempDirBase)
	assert.Equal(t, "diff-tool", cfg.ExternalDiffTool)
	assert.True(t, cfg.Debug)
	assert.Equal(t, "1.2.3", cfg.Version)
}

func TestNewConfigRequiresTargetBranch(t *testing.T) {
	_, err := NewConfig("")
	assert.Error(t, err)
}

func TestNewConfigWithGitLabComment(t *testing.T) {
	cfg, err := NewConfig("main",
		WithCacheDir("/tmp/cache"),
		WithCommentConfig(CommentConfig{
			Provider: CommentProviderGitLab,
			GitLab: GitLabCommentConfig{
				BaseURL:         "https://gitlab.example.com",
				Token:           "secret",
				ProjectID:       "1",
				MergeRequestIID: 42,
			},
		}),
	)
	require.NoError(t, err)
	require.NotNil(t, cfg.Comment)
	assert.Equal(t, CommentProviderGitLab, cfg.Comment.Provider)
	assert.Equal(t, "https://gitlab.example.com", cfg.Comment.GitLab.BaseURL)
	assert.Equal(t, 42, cfg.Comment.GitLab.MergeRequestIID)
}

func TestNewConfigWithInvalidGitLabComment(t *testing.T) {
	_, err := NewConfig("main",
		WithCommentConfig(CommentConfig{
			Provider: CommentProviderGitLab,
		}),
	)
	require.Error(t, err)
}

func TestNewConfigWithUnsupportedCommentProvider(t *testing.T) {
	_, err := NewConfig("main",
		WithCommentConfig(CommentConfig{Provider: "bitbucket"}),
	)
	require.Error(t, err)
}
