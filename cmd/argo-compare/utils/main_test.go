package utils

import (
	"io"
	"os"
	"testing"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
)

// TestMain silences logger output by default for all tests in this package.
// Tests that need to assert on log output call logger.RedirectForTest(t, &buf),
// which captures during the test and restores io.Discard on cleanup.
func TestMain(m *testing.M) {
	logger.SetOutput(io.Discard)
	os.Exit(m.Run())
}
