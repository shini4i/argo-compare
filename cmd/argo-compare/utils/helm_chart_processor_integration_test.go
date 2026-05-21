//go:build helm_integration

// Build-tagged integration tests for the HTTP credential flow. These run
// against the real `helm` binary on PATH, not against mocks. Run with:
//
//	go test -tags=helm_integration ./cmd/argo-compare/utils
//
// Skipped by default; CI selects this tag explicitly. The all-mock unit tests
// in helm_chart_processor_test.go cannot catch regressions where we pass a
// flag (e.g. --password-stdin) that the underlying helm subcommand does not
// accept — these tests close that gap by actually invoking helm.

package utils_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
)

// TestHelm_PullDoesNotSupportPasswordStdin documents and locks in the constraint
// that drove the implementation of pullHTTPChartWithCreds. If a future helm
// release adds --password-stdin to `helm pull`, this test will fail and the
// helper can be simplified — that failure is the trigger to do so.
func TestHelm_PullDoesNotSupportPasswordStdin(t *testing.T) {
	cmd := exec.Command("helm", "pull", "--password-stdin", "--repo", "https://invalid.example", "fake")
	cmd.Stdin = strings.NewReader("")
	out, _ := cmd.CombinedOutput()
	assert.Contains(t, string(out), "unknown flag: --password-stdin",
		"helm pull MUST reject --password-stdin; if this changes, pullHTTPChartWithCreds can be simplified to a single helm pull call")
}

// TestPullHTTPChartWithCreds_RealHelm exercises the full credential flow against
// a live helm binary and an in-process HTTP server that requires Basic auth.
// It guards specifically against the regression class where the mock test
// asserts on argv shape but the real helm rejects the call.
func TestPullHTTPChartWithCreds_RealHelm(t *testing.T) {
	const (
		username  = "testuser"
		password  = "testpass"
		chartName = "integration-chart"
		version   = "0.1.0"
	)

	// 1) Build a minimal chart tarball with the real helm CLI.
	srcDir := t.TempDir()
	chartDir := filepath.Join(srcDir, chartName)
	require.NoError(t, os.MkdirAll(chartDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(chartDir, "Chart.yaml"),
		[]byte(fmt.Sprintf("apiVersion: v2\nname: %s\nversion: %s\n", chartName, version)),
		0o644,
	))
	packageCmd := exec.Command("helm", "package", chartDir, "--destination", srcDir)
	packageOut, err := packageCmd.CombinedOutput()
	require.NoError(t, err, "helm package failed: %s", packageOut)

	tgzPath := filepath.Join(srcDir, fmt.Sprintf("%s-%s.tgz", chartName, version))
	tgzBytes, err := os.ReadFile(tgzPath)
	require.NoError(t, err)
	sum := sha256.Sum256(tgzBytes)
	digest := hex.EncodeToString(sum[:])

	// 2) Serve the chart over HTTP with Basic auth.
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != username || p != password {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/index.yaml":
			fmt.Fprintf(w, `apiVersion: v1
entries:
  %s:
  - apiVersion: v2
    name: %s
    version: %s
    urls:
    - %s/%s-%s.tgz
    digest: %s
generated: "0001-01-01T00:00:00Z"
`, chartName, chartName, version, server.URL, chartName, version, digest)
		case fmt.Sprintf("/%s-%s.tgz", chartName, version):
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(tgzBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	// 3) Exercise pullHTTPChartWithCreds via DownloadHelmChart.
	cacheDir := t.TempDir()
	processor := utils.RealHelmChartProcessor{Log: logger.New("integration")}
	staticProvider := utils.NewStaticCredentialProvider([]models.RepoCredentials{
		{Url: server.URL, Username: username, Password: password},
	})
	deps := ports.HelmDeps{
		CmdRunner:           &utils.RealCmdRunner{},
		Globber:             utils.CustomGlobber{},
		CredentialProviders: []ports.CredentialProvider{staticProvider},
	}
	req := ports.ChartDownloadRequest{
		CacheDir:       cacheDir,
		RepoURL:        server.URL,
		ChartName:      chartName,
		TargetRevision: version,
	}
	err = processor.DownloadHelmChart(context.Background(), deps, req)
	require.NoError(t, err, "DownloadHelmChart should succeed with valid Basic auth credentials")

	// 4) Confirm the chart actually landed somewhere in the cache.
	matches, err := filepath.Glob(filepath.Join(cacheDir, "**", fmt.Sprintf("%s-%s.tgz", chartName, version)))
	require.NoError(t, err)
	if len(matches) == 0 {
		// Fall back to a deeper walk in case the cache layout uses extra path segments.
		_ = filepath.Walk(cacheDir, func(path string, _ os.FileInfo, _ error) error {
			if strings.HasSuffix(path, fmt.Sprintf("%s-%s.tgz", chartName, version)) {
				matches = append(matches, path)
			}
			return nil
		})
	}
	assert.NotEmpty(t, matches, "downloaded chart tarball should exist somewhere under %s", cacheDir)
}

// TestPullHTTPChartWithCreds_RealHelm_BadAuth verifies that the wrong password
// surfaces as ErrFailedToDownloadChart rather than being swallowed.
func TestPullHTTPChartWithCreds_RealHelm_BadAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	processor := utils.RealHelmChartProcessor{Log: logger.New("integration-badauth")}
	staticProvider := utils.NewStaticCredentialProvider([]models.RepoCredentials{
		{Url: server.URL, Username: "wrong", Password: "wrong"},
	})
	deps := ports.HelmDeps{
		CmdRunner:           &utils.RealCmdRunner{},
		Globber:             utils.CustomGlobber{},
		CredentialProviders: []ports.CredentialProvider{staticProvider},
	}
	req := ports.ChartDownloadRequest{
		CacheDir:       t.TempDir(),
		RepoURL:        server.URL,
		ChartName:      "anything",
		TargetRevision: "0.1.0",
	}
	err := processor.DownloadHelmChart(context.Background(), deps, req)
	require.Error(t, err)
	assert.ErrorIs(t, err, utils.ErrFailedToDownloadChart)
}
