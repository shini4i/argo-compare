package app

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/shini4i/argo-compare/internal/comment"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
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

func TestNewWithValidationEnabledCreatesValidator(t *testing.T) {
	cfg, err := NewConfig("main",
		WithCacheDir("/tmp/cache"),
		WithValidateManifests(true),
	)
	require.NoError(t, err)

	logger := setupTestLogger(t, "app-validator-enabled")

	appInstance, err := New(cfg, Dependencies{
		FS:     afero.NewMemMapFs(),
		Logger: logger,
	})
	require.NoError(t, err)

	require.NotNil(t, appInstance.validator, "validator should be initialized when validation is enabled")

	kubeconformValidator, ok := appInstance.validator.(*KubeconformValidator)
	require.True(t, ok, "default validator should be KubeconformValidator")
	assert.Equal(t, "kubeconform", kubeconformValidator.Path)
}

func TestNewWithValidationDisabledOmitsValidator(t *testing.T) {
	cfg, err := NewConfig("main",
		WithCacheDir("/tmp/cache"),
	)
	require.NoError(t, err)

	logger := setupTestLogger(t, "app-validator-disabled")

	appInstance, err := New(cfg, Dependencies{
		FS:     afero.NewMemMapFs(),
		Logger: logger,
	})
	require.NoError(t, err)

	assert.Nil(t, appInstance.validator, "validator should be nil when validation is disabled")
}

func TestNewWithCustomKubeconformPath(t *testing.T) {
	cfg, err := NewConfig("main",
		WithCacheDir("/tmp/cache"),
		WithValidateManifests(true),
		WithKubeconformPath("/opt/kubeconform"),
		WithValidateSkipKinds([]string{"ServiceMonitor", "ArgoApplication"}),
	)
	require.NoError(t, err)

	logger := setupTestLogger(t, "app-validator-custom")

	appInstance, err := New(cfg, Dependencies{
		FS:     afero.NewMemMapFs(),
		Logger: logger,
	})
	require.NoError(t, err)

	kubeconformValidator, ok := appInstance.validator.(*KubeconformValidator)
	require.True(t, ok)
	assert.Equal(t, "/opt/kubeconform", kubeconformValidator.Path)
	assert.Equal(t, []string{"ServiceMonitor", "ArgoApplication"}, kubeconformValidator.SkipKinds)
}

func TestNewWithInjectedValidator(t *testing.T) {
	cfg, err := NewConfig("main",
		WithCacheDir("/tmp/cache"),
		WithValidateManifests(false),
	)
	require.NoError(t, err)

	logger := setupTestLogger(t, "app-validator-injected")

	injectedValidator := &KubeconformValidator{Path: "injected"}
	appInstance, err := New(cfg, Dependencies{
		FS:                afero.NewMemMapFs(),
		Logger:            logger,
		ManifestValidator: injectedValidator,
	})
	require.NoError(t, err)

	require.NotNil(t, appInstance.validator, "injected validator should be used")
	assert.Same(t, injectedValidator, appInstance.validator)
}

func TestProcessFileCallsValidatorWithCorrectPath(t *testing.T) {
	// Verifies the validator is invoked with <tmpDir>/templates/<fileType> and that
	// the result is stored in the caller-supplied validationResults map.
	// Uses TargetTypeDestination to skip the parse() step (which reads from the filesystem).
	ctrl := gomock.NewController(t)
	mockValidator := mocks.NewMockManifestValidator(ctrl)
	mockHelmProcessor := mocks.NewMockHelmChartsProcessor(ctrl)

	cfg, err := NewConfig("main", WithCacheDir("/tmp/cache"), WithValidateManifests(true))
	require.NoError(t, err)

	logger := setupTestLogger(t, "app-processfile-validator")

	appInstance, err := New(cfg, Dependencies{
		FS:                afero.NewMemMapFs(),
		Logger:            logger,
		ManifestValidator: mockValidator,
		HelmProcessor:     mockHelmProcessor,
	})
	require.NoError(t, err)

	tmpDir := t.TempDir()

	// Stub all Helm processor calls so processFile reaches the validation step.
	mockHelmProcessor.EXPECT().GenerateValuesFile(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockHelmProcessor.EXPECT().DownloadHelmChart(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockHelmProcessor.EXPECT().ExtractHelmChart(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockHelmProcessor.EXPECT().RenderAppSource(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	expectedManifestDir := tmpDir + "/templates/" + TargetTypeDestination
	expectedResult := ports.ValidationResult{
		Target:        TargetTypeDestination,
		Valid:         true,
		ResourceCount: 3,
	}

	mockValidator.EXPECT().
		Validate(gomock.Any(), TargetTypeDestination, expectedManifestDir).
		Return(expectedResult, nil)

	// Provide a minimal Application with non-nil Source and Destination to avoid
	// nil dereferences in generateValuesFiles and renderAppSources.
	testApp := models.Application{}
	testApp.Spec.Source = &models.Source{Chart: "my-chart", RepoURL: "https://charts.example.com"}
	testApp.Spec.Destination = &models.Destination{Server: "https://kubernetes.default.svc", Namespace: "default"}

	validationResults := make(map[string]ports.ValidationResult)
	err = appInstance.processFile(context.Background(), "apps/test.yaml", TargetTypeDestination, testApp, tmpDir, validationResults)
	require.NoError(t, err)

	require.Contains(t, validationResults, TargetTypeDestination)
	assert.Equal(t, expectedResult, validationResults[TargetTypeDestination])
}
