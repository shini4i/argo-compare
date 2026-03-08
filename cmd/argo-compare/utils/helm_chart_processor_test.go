package utils

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func TestGenerateValuesFile(t *testing.T) {
	helmChartProcessor := RealHelmChartProcessor{}

	tmpDir := t.TempDir()

	chartName := "ingress-nginx"
	targetType := "src"
	values := "fullnameOverride: ingress-nginx\ncontroller:\n  kind: DaemonSet\n  service:\n    externalTrafficPolicy: Local\n    annotations:\n      fancyAnnotation: false\n"

	// Test case 1: Everything works as expected
	err := helmChartProcessor.GenerateValuesFile(chartName, tmpDir, targetType, values, nil)
	assert.NoError(t, err, "expected no error, got %v", err)

	// Read the generated file
	generatedValues, err := os.ReadFile(filepath.Join(tmpDir, chartName+"-values-"+targetType+".yaml"))
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, values, string(generatedValues))

	// Test case 2: Error when creating the file
	err = helmChartProcessor.GenerateValuesFile(chartName, "/non/existing/path", targetType, values, nil)
	assert.Error(t, err, "expected error, got nil")

	// Test case 3: Error when neither values nor valuesObject is provided
	err = helmChartProcessor.GenerateValuesFile(chartName, tmpDir, targetType, "", nil)
	assert.Error(t, err, "expected error when both values and valuesObject are empty")
	assert.Contains(t, err.Error(), "either 'values' or 'valuesObject' must be provided")
}

func TestDownloadHelmChart(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	helmChartProcessor := RealHelmChartProcessor{Log: logging.MustGetLogger("test")}
	cacheDir := t.TempDir()

	// Create the mocks
	mockGlobber := mocks.NewMockGlobber(ctrl)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	deps := ports.HelmDeps{CmdRunner: mockCmdRunner, Globber: mockGlobber}

	// Test case 1: chart exists in cache
	mockGlobber.EXPECT().Glob(gomock.Any()).Return([]string{filepath.Join(cacheDir, "ingress-nginx-3.34.0.tgz")}, nil)
	req := ports.ChartDownloadRequest{
		CacheDir:       filepath.Join(cacheDir, "cache"),
		RepoURL:        "https://chart.example.com",
		ChartName:      "ingress-nginx",
		TargetRevision: "3.34.0",
	}
	err := helmChartProcessor.DownloadHelmChart(context.Background(), deps, req)
	assert.NoError(t, err, "expected no error, got %v", err)
}

func TestDownloadHelmChart_HTTPWithCredentials(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	helmChartProcessor := RealHelmChartProcessor{Log: logging.MustGetLogger("test")}
	cacheDir := t.TempDir()

	mockGlobber := mocks.NewMockGlobber(ctrl)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	staticProvider := NewStaticCredentialProvider([]models.RepoCredentials{
		{Url: "https://chart.example.com", Username: "user", Password: "pass"},
	})
	deps := ports.HelmDeps{
		CmdRunner:           mockCmdRunner,
		Globber:             mockGlobber,
		CredentialProviders: []ports.CredentialProvider{staticProvider},
	}

	mockGlobber.EXPECT().Glob(gomock.Any()).Return([]string{}, nil)
	mockCmdRunner.EXPECT().Run(gomock.Any(), "helm",
		"pull",
		"--repo", "https://chart.example.com",
		"ingress-nginx",
		"--version", "3.34.0",
		"--destination", gomock.Any(),
		"--username", "user",
		"--password", "pass").Return("", "", nil)

	req := ports.ChartDownloadRequest{
		CacheDir:       filepath.Join(cacheDir, "cache"),
		RepoURL:        "https://chart.example.com",
		ChartName:      "ingress-nginx",
		TargetRevision: "3.34.0",
	}
	err := helmChartProcessor.DownloadHelmChart(context.Background(), deps, req)
	assert.NoError(t, err)
}

func TestDownloadHelmChart_HTTPWithoutCredentials(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	helmChartProcessor := RealHelmChartProcessor{Log: logging.MustGetLogger("test")}
	cacheDir := t.TempDir()

	mockGlobber := mocks.NewMockGlobber(ctrl)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	deps := ports.HelmDeps{CmdRunner: mockCmdRunner, Globber: mockGlobber}

	mockGlobber.EXPECT().Glob(gomock.Any()).Return([]string{}, nil)
	// No --username, --password flags when credentials are empty.
	mockCmdRunner.EXPECT().Run(gomock.Any(), "helm",
		"pull",
		"--repo", "https://public-charts.example.com",
		"my-chart",
		"--version", "1.0.0",
		"--destination", gomock.Any()).Return("", "", nil)

	req := ports.ChartDownloadRequest{
		CacheDir:       filepath.Join(cacheDir, "cache"),
		RepoURL:        "https://public-charts.example.com",
		ChartName:      "my-chart",
		TargetRevision: "1.0.0",
	}
	err := helmChartProcessor.DownloadHelmChart(context.Background(), deps, req)
	assert.NoError(t, err)
}

func TestDownloadHelmChart_HTTPFailedDownload(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	helmChartProcessor := RealHelmChartProcessor{Log: logging.MustGetLogger("test")}
	cacheDir := t.TempDir()

	mockGlobber := mocks.NewMockGlobber(ctrl)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	deps := ports.HelmDeps{CmdRunner: mockCmdRunner, Globber: mockGlobber}

	osErr := &exec.ExitError{ProcessState: &os.ProcessState{}}
	mockGlobber.EXPECT().Glob(gomock.Any()).Return([]string{}, nil)
	mockCmdRunner.EXPECT().Run(gomock.Any(), "helm",
		"pull",
		"--repo", "https://chart.example.com",
		"ingress-nginx",
		"--version", "3.34.0",
		"--destination", gomock.Any()).Return("", "dummy error message", osErr).Times(3)

	req := ports.ChartDownloadRequest{
		CacheDir:       filepath.Join(cacheDir, "cache"),
		RepoURL:        "https://chart.example.com",
		ChartName:      "ingress-nginx",
		TargetRevision: "3.34.0",
	}
	err := helmChartProcessor.DownloadHelmChart(context.Background(), deps, req)
	assert.ErrorIsf(t, err, ErrFailedToDownloadChart, "expected error %v, got %v", ErrFailedToDownloadChart, err)
}

func TestDownloadHelmChart_OCIWithCredentials(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	helmChartProcessor := RealHelmChartProcessor{Log: logging.MustGetLogger("test")}
	cacheDir := t.TempDir()

	mockGlobber := mocks.NewMockGlobber(ctrl)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	staticProvider := NewStaticCredentialProvider([]models.RepoCredentials{
		{Url: "123456789012.dkr.ecr.us-east-1.amazonaws.com", Username: "AWS", Password: "ecr-token"},
	})
	deps := ports.HelmDeps{
		CmdRunner:           mockCmdRunner,
		Globber:             mockGlobber,
		CredentialProviders: []ports.CredentialProvider{staticProvider},
	}

	mockGlobber.EXPECT().Glob(gomock.Any()).Return([]string{}, nil)

	// Expect helm registry login first.
	mockCmdRunner.EXPECT().Run(gomock.Any(), "helm",
		"registry", "login",
		"123456789012.dkr.ecr.us-east-1.amazonaws.com",
		"--username", "AWS",
		"--password", "ecr-token").Return("", "", nil)

	// Then expect helm pull without --repo, --username, --password.
	mockCmdRunner.EXPECT().Run(gomock.Any(), "helm",
		"pull", "oci://123456789012.dkr.ecr.us-east-1.amazonaws.com/my-chart",
		"--destination", gomock.Any(),
		"--version", "1.0.0").Return("", "", nil)

	req := ports.ChartDownloadRequest{
		CacheDir:       filepath.Join(cacheDir, "cache"),
		RepoURL:        "123456789012.dkr.ecr.us-east-1.amazonaws.com",
		ChartName:      "my-chart",
		TargetRevision: "1.0.0",
	}
	err := helmChartProcessor.DownloadHelmChart(context.Background(), deps, req)
	assert.NoError(t, err)
}

func TestDownloadHelmChart_OCIWithoutCredentials(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	helmChartProcessor := RealHelmChartProcessor{Log: logging.MustGetLogger("test")}
	cacheDir := t.TempDir()

	mockGlobber := mocks.NewMockGlobber(ctrl)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	deps := ports.HelmDeps{CmdRunner: mockCmdRunner, Globber: mockGlobber}

	mockGlobber.EXPECT().Glob(gomock.Any()).Return([]string{}, nil)

	// No helm registry login. Just helm pull.
	mockCmdRunner.EXPECT().Run(gomock.Any(), "helm",
		"pull", "oci://ghcr.io/my-org/my-chart",
		"--destination", gomock.Any(),
		"--version", "2.0.0").Return("", "", nil)

	req := ports.ChartDownloadRequest{
		CacheDir:       filepath.Join(cacheDir, "cache"),
		RepoURL:        "ghcr.io/my-org",
		ChartName:      "my-chart",
		TargetRevision: "2.0.0",
	}
	err := helmChartProcessor.DownloadHelmChart(context.Background(), deps, req)
	assert.NoError(t, err)
}

func TestDownloadHelmChart_OCIPrefixNormalization(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	helmChartProcessor := RealHelmChartProcessor{Log: logging.MustGetLogger("test")}
	cacheDir := t.TempDir()

	mockGlobber := mocks.NewMockGlobber(ctrl)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	deps := ports.HelmDeps{CmdRunner: mockCmdRunner, Globber: mockGlobber}

	mockGlobber.EXPECT().Glob(gomock.Any()).Return([]string{}, nil)

	// Even though RepoURL has "oci://" prefix, the pull ref should not double it
	// and helm registry login should receive a bare hostname.
	mockCmdRunner.EXPECT().Run(gomock.Any(), "helm",
		"pull", "oci://ghcr.io/my-org/my-chart",
		"--destination", gomock.Any(),
		"--version", "2.0.0").Return("", "", nil)

	req := ports.ChartDownloadRequest{
		CacheDir:       filepath.Join(cacheDir, "cache"),
		RepoURL:        "oci://ghcr.io/my-org",
		ChartName:      "my-chart",
		TargetRevision: "2.0.0",
	}
	err := helmChartProcessor.DownloadHelmChart(context.Background(), deps, req)
	assert.NoError(t, err)
}

func TestExtractHelmChart(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	helmChartProcessor := RealHelmChartProcessor{Log: logging.MustGetLogger("test")}
	baseDir := t.TempDir()
	expectedChartLocation := filepath.Join(baseDir, "cache")
	expectedTmpDir := filepath.Join(baseDir, "tmp")

	// Create the mocks
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	mockGlobber := mocks.NewMockGlobber(ctrl)
	deps := ports.HelmDeps{CmdRunner: mockCmdRunner, Globber: mockGlobber}

	// Set up the expected behavior for the mocks

	// Test case 1: Single chart file found
	expectedChartFileName := filepath.Join(baseDir, "charts", "ingress-nginx", "ingress-nginx-3.34.0.tgz")
	expectedTargetType := "target"

	// Mock the behavior of the globber
	mockGlobber.EXPECT().Glob(fmt.Sprintf("%s/%s-%s*.tgz", expectedChartLocation, "ingress-nginx", "3.34.0")).Return([]string{expectedChartFileName}, nil)

	// Mock the behavior of the cmdRunner
	mockCmdRunner.EXPECT().Run(gomock.Any(), "tar",
		"xf",
		expectedChartFileName,
		"-C",
		fmt.Sprintf("%s/charts/%s", expectedTmpDir, expectedTargetType),
	).Return("", "", nil)

	req := ports.ChartExtractRequest{
		ChartName:     "ingress-nginx",
		ChartVersion:  "3.34.0",
		ChartLocation: expectedChartLocation,
		TmpDir:        expectedTmpDir,
		TargetType:    expectedTargetType,
	}
	err := helmChartProcessor.ExtractHelmChart(context.Background(), deps, req)

	assert.NoError(t, err, "expected no error, got %v", err)

	// Test case 2: Multiple chart files found, error expected
	expectedChartFilesNames := []string{
		filepath.Join(baseDir, "charts", "sonarqube", "sonarqube-4.0.0+315.tgz"),
		filepath.Join(baseDir, "charts", "sonarqube", "sonarqube-4.0.0+316.tgz"),
	}

	mockGlobber.EXPECT().Glob(fmt.Sprintf("%s/%s-%s*.tgz", expectedChartLocation, "sonarqube", "4.0.0")).Return(expectedChartFilesNames, nil)

	req2 := ports.ChartExtractRequest{
		ChartName:     "sonarqube",
		ChartVersion:  "4.0.0",
		ChartLocation: expectedChartLocation,
		TmpDir:        expectedTmpDir,
		TargetType:    expectedTargetType,
	}
	err = helmChartProcessor.ExtractHelmChart(context.Background(), deps, req2)
	assert.Error(t, err, "expected error, got %v", err)

	// Test case 3: Chart file found, but failed to extract
	mockGlobber.EXPECT().Glob(fmt.Sprintf("%s/%s-%s*.tgz", expectedChartLocation, "ingress-nginx", "3.34.0")).Return([]string{expectedChartFileName}, nil)
	mockCmdRunner.EXPECT().Run(gomock.Any(), "tar",
		"xf",
		expectedChartFileName,
		"-C",
		fmt.Sprintf("%s/charts/%s", expectedTmpDir, expectedTargetType),
	).Return("", "some unexpected error", errors.New("some unexpected error"))

	err = helmChartProcessor.ExtractHelmChart(context.Background(), deps, req)
	assert.Error(t, err, "expected error, got %v", err)

	// Test case 4: zglob failed to run
	mockGlobber.EXPECT().Glob(fmt.Sprintf("%s/%s-%s*.tgz", expectedChartLocation, "ingress-nginx", "3.34.0")).Return([]string{}, os.ErrPermission)

	err = helmChartProcessor.ExtractHelmChart(context.Background(), deps, req)
	assert.Error(t, err, "expected error, got %v", err)

	// Test case 5: Failed to find chart file
	mockGlobber.EXPECT().Glob(fmt.Sprintf("%s/%s-%s*.tgz", expectedChartLocation, "ingress-nginx", "3.34.0")).Return([]string{}, nil)

	err = helmChartProcessor.ExtractHelmChart(context.Background(), deps, req)
	assert.Error(t, err, "expected error, got %v", err)
}

func TestRenderAppSource(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	helmChartProcessor := RealHelmChartProcessor{Log: logging.MustGetLogger("test")}

	// Create an instance of the mock CmdRunner
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	tmpDir := t.TempDir()

	req := ports.ChartRenderRequest{
		ReleaseName:  "my-release",
		ChartName:    "my-chart",
		ChartVersion: "1.2.3",
		TmpDir:       tmpDir,
		TargetType:   "src",
		Namespace:    "my-namespace",
	}

	// Test case 1: Successful render
	mockCmdRunner.EXPECT().Run(gomock.Any(), "helm",
		"template",
		"--release-name", gomock.Any(),
		gomock.Any(),
		"--output-dir", gomock.Any(),
		"--values", gomock.Any(),
		"--values", gomock.Any(),
		"--namespace", gomock.Any()).Return("", "", nil)

	// Call the function under test
	err := helmChartProcessor.RenderAppSource(context.Background(), mockCmdRunner, req)
	assert.NoError(t, err, "expected no error, got %v", err)

	// Test case 2: Failed render
	osErr := &exec.ExitError{
		ProcessState: &os.ProcessState{},
	}
	mockCmdRunner.EXPECT().Run(gomock.Any(), "helm",
		"template",
		"--release-name", gomock.Any(),
		gomock.Any(),
		"--output-dir", gomock.Any(),
		"--values", gomock.Any(),
		"--values", gomock.Any(),
		"--namespace", gomock.Any()).Return("", "", osErr)

	err = helmChartProcessor.RenderAppSource(context.Background(), mockCmdRunner, req)
	assert.Error(t, err, "expected error, got nil")
	assert.Errorf(t, err, "expected error, got %v", err)
}

func TestResolveCredentials(t *testing.T) {
	log := logging.MustGetLogger("test")

	t.Run("first matching provider wins", func(t *testing.T) {
		p1 := NewStaticCredentialProvider([]models.RepoCredentials{
			{Url: "https://charts.example.com", Username: "first", Password: "first-pass"},
		})
		p2 := NewStaticCredentialProvider([]models.RepoCredentials{
			{Url: "https://charts.example.com", Username: "second", Password: "second-pass"},
		})

		creds := resolveCredentials(context.Background(), log, []ports.CredentialProvider{p1, p2}, "https://charts.example.com")
		assert.Equal(t, "first", creds.Username)
		assert.Equal(t, "first-pass", creds.Password)
	})

	t.Run("no match returns empty", func(t *testing.T) {
		p := NewStaticCredentialProvider([]models.RepoCredentials{
			{Url: "https://other.com", Username: "user", Password: "pass"},
		})

		creds := resolveCredentials(context.Background(), log, []ports.CredentialProvider{p}, "https://charts.example.com")
		assert.Equal(t, ports.RegistryCredentials{}, creds)
	})

	t.Run("nil providers returns empty", func(t *testing.T) {
		creds := resolveCredentials(context.Background(), log, nil, "https://charts.example.com")
		assert.Equal(t, ports.RegistryCredentials{}, creds)
	})

	t.Run("partial credentials fall through to next provider", func(t *testing.T) {
		partialProvider := NewStaticCredentialProvider([]models.RepoCredentials{
			{Url: "https://charts.example.com", Username: "user-only", Password: ""},
		})
		completeProvider := NewStaticCredentialProvider([]models.RepoCredentials{
			{Url: "https://charts.example.com", Username: "full-user", Password: "full-pass"},
		})

		creds := resolveCredentials(context.Background(), log,
			[]ports.CredentialProvider{partialProvider, completeProvider},
			"https://charts.example.com")
		assert.Equal(t, "full-user", creds.Username)
		assert.Equal(t, "full-pass", creds.Password)
	})

	t.Run("provider error falls through to next", func(t *testing.T) {
		failing := &errorCredentialProvider{
			matchURL: "https://charts.example.com",
			err:      errors.New("token exchange failed"),
		}
		fallback := NewStaticCredentialProvider([]models.RepoCredentials{
			{Url: "https://charts.example.com", Username: "fallback", Password: "fallback-pass"},
		})

		creds := resolveCredentials(context.Background(), log, []ports.CredentialProvider{failing, fallback}, "https://charts.example.com")
		assert.Equal(t, "fallback", creds.Username)
	})
}

// errorCredentialProvider is a test helper that always returns an error from GetCredentials.
type errorCredentialProvider struct {
	matchURL string
	err      error
}

func (p *errorCredentialProvider) Matches(registryURL string) bool {
	return registryURL == p.matchURL
}

func (p *errorCredentialProvider) GetCredentials(_ context.Context, _ string) (ports.RegistryCredentials, error) {
	return ports.RegistryCredentials{}, p.err
}

func TestDownloadHelmChart_OCILoginFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	helmChartProcessor := RealHelmChartProcessor{Log: logging.MustGetLogger("test")}
	cacheDir := t.TempDir()

	mockGlobber := mocks.NewMockGlobber(ctrl)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	staticProvider := NewStaticCredentialProvider([]models.RepoCredentials{
		{Url: "123456789012.dkr.ecr.us-east-1.amazonaws.com", Username: "AWS", Password: "ecr-token"},
	})
	deps := ports.HelmDeps{
		CmdRunner:           mockCmdRunner,
		Globber:             mockGlobber,
		CredentialProviders: []ports.CredentialProvider{staticProvider},
	}

	mockGlobber.EXPECT().Glob(gomock.Any()).Return([]string{}, nil)

	// helm registry login fails.
	mockCmdRunner.EXPECT().Run(gomock.Any(), "helm",
		"registry", "login",
		"123456789012.dkr.ecr.us-east-1.amazonaws.com",
		"--username", "AWS",
		"--password", "ecr-token").Return("", "login failed", errors.New("login error"))

	// helm pull should NOT be called.

	req := ports.ChartDownloadRequest{
		CacheDir:       filepath.Join(cacheDir, "cache"),
		RepoURL:        "123456789012.dkr.ecr.us-east-1.amazonaws.com",
		ChartName:      "my-chart",
		TargetRevision: "1.0.0",
	}
	err := helmChartProcessor.DownloadHelmChart(context.Background(), deps, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to login to OCI registry")
}

func TestIsOCIRegistry(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "HTTP URL", url: "https://charts.example.com", want: false},
		{name: "HTTP URL plain", url: "http://charts.example.com", want: false},
		{name: "ECR registry", url: "123456789012.dkr.ecr.us-east-1.amazonaws.com", want: true},
		{name: "GHCR", url: "ghcr.io/my-org", want: true},
		{name: "empty", url: "", want: false},
		{name: "hostname with http substring", url: "httpbin-charts.example.com", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isOCIRegistry(tt.url))
		})
	}
}
