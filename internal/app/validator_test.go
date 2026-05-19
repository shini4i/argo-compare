package app

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const validResourcesJSON = `{
  "resources": [
    {"filename": "/tmp/templates/src/deployment.yaml", "kind": "Deployment", "name": "app", "status": "statusValid", "msg": ""},
    {"filename": "/tmp/templates/src/service.yaml", "kind": "Service", "name": "app", "status": "statusValid", "msg": ""}
  ],
  "summary": {"valid": 2, "invalid": 0, "errors": 0, "skipped": 0}
}`

const invalidResourcesJSON = `{
  "resources": [
    {"filename": "/tmp/templates/src/deployment.yaml", "kind": "Deployment", "name": "app", "status": "statusValid", "msg": ""},
    {"filename": "/tmp/templates/src/bad.yaml", "kind": "Service", "name": "broken", "status": "statusInvalid", "msg": "spec.ports.port: required field missing"}
  ],
  "summary": {"valid": 1, "invalid": 1, "errors": 0, "skipped": 0}
}`

const skippedResourceJSON = `{
  "resources": [
    {"filename": "/tmp/templates/src/crd.yaml", "kind": "MyCRD", "name": "custom", "status": "statusSkipped", "msg": ""}
  ],
  "summary": {"valid": 0, "invalid": 0, "errors": 0, "skipped": 1}
}`

const errorResourceJSON = `{
  "resources": [
    {"filename": "/tmp/templates/src/ingress.yaml", "kind": "Ingress", "name": "broken", "status": "statusError", "msg": "error validating schema"}
  ],
  "summary": {"valid": 0, "invalid": 0, "errors": 1, "skipped": 0}
}`

// allValidEmptyResourcesJSON reproduces kubeconform's non-verbose JSON output when every
// resource is valid: the resources array is empty and only the summary carries counts.
// Reading the count from len(resources) would incorrectly report 0 resources validated.
const allValidEmptyResourcesJSON = `{
  "resources": [],
  "summary": {"valid": 3, "invalid": 0, "errors": 0, "skipped": 0}
}`

func TestKubeconformValidator_ValidManifests(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	mockCmdRunner.EXPECT().
		Run(gomock.Any(), "kubeconform", gomock.Any()).
		Return(validResourcesJSON, "", nil)

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
	}

	result, err := v.Validate(context.Background(), "src", "/tmp/templates/src")

	require.NoError(t, err)
	assert.Equal(t, "src", result.Target)
	assert.True(t, result.Valid)
	assert.Equal(t, 2, result.ResourceCount)
	assert.Equal(t, 0, result.ErrorCount)
	assert.Empty(t, result.Errors)
}

func TestKubeconformValidator_AllValidWithEmptyResourcesArray(t *testing.T) {
	// Regression test: kubeconform's non-verbose JSON output omits valid resources
	// from the resources array. Before the fix, ResourceCount was derived from
	// len(parsed.Resources) and incorrectly reported 0 even when every resource passed.
	// ResourceCount must now come from the summary fields (valid+invalid+errors+skipped).
	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	mockCmdRunner.EXPECT().
		Run(gomock.Any(), "kubeconform", gomock.Any()).
		Return(allValidEmptyResourcesJSON, "", nil)

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
	}

	result, err := v.Validate(context.Background(), "src", "/tmp/templates/src")

	require.NoError(t, err)
	assert.True(t, result.Valid)
	assert.Equal(t, 3, result.ResourceCount, "ResourceCount must come from summary, not len(resources)")
	assert.Equal(t, 0, result.ErrorCount)
	assert.Empty(t, result.Errors)
}

func TestKubeconformValidator_InvalidManifests(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	// kubeconform exits with non-zero when manifests are invalid; treat exit error as expected.
	exitErr := &exec.ExitError{}
	mockCmdRunner.EXPECT().
		Run(gomock.Any(), "kubeconform", gomock.Any()).
		Return(invalidResourcesJSON, "", exitErr)

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
	}

	result, err := v.Validate(context.Background(), "dst", "/tmp/templates/dst")

	require.NoError(t, err, "validation failures should not return an error from Validate")
	assert.Equal(t, "dst", result.Target)
	assert.False(t, result.Valid)
	assert.Equal(t, 2, result.ResourceCount)
	assert.Equal(t, 1, result.ErrorCount)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "Service", result.Errors[0].Kind)
	assert.Equal(t, "broken", result.Errors[0].Name)
	assert.Contains(t, result.Errors[0].Message, "spec.ports.port")
}

func TestKubeconformValidator_StatusErrorCountsAsFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	exitErr := &exec.ExitError{}
	mockCmdRunner.EXPECT().
		Run(gomock.Any(), "kubeconform", gomock.Any()).
		Return(errorResourceJSON, "", exitErr)

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
	}

	result, err := v.Validate(context.Background(), "src", "/tmp/templates/src")

	require.NoError(t, err)
	assert.False(t, result.Valid)
	assert.Equal(t, 1, result.ErrorCount)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "Ingress", result.Errors[0].Kind)
	assert.Contains(t, result.Errors[0].Message, "error validating schema")
}

func TestKubeconformValidator_SkippedManifestsAreNotErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	mockCmdRunner.EXPECT().
		Run(gomock.Any(), "kubeconform", gomock.Any()).
		Return(skippedResourceJSON, "", nil)

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
	}

	result, err := v.Validate(context.Background(), "src", "/tmp/templates/src")

	require.NoError(t, err)
	assert.True(t, result.Valid)
	assert.Equal(t, 1, result.ResourceCount)
	assert.Equal(t, 0, result.ErrorCount)
}

func TestKubeconformValidator_PassesSkipKindsFlag(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	const manifestDir = "/tmp/templates/src"

	mockCmdRunner.EXPECT().
		Run(gomock.Any(), "kubeconform", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, args ...string) (string, string, error) {
			// Verify -skip appears adjacent to the joined kinds value.
			var skipIdx int = -1
			for i, a := range args {
				if a == "-skip" {
					skipIdx = i
					break
				}
			}
			require.GreaterOrEqual(t, skipIdx, 0, "-skip flag not found in args")
			require.Less(t, skipIdx+1, len(args), "-skip has no value")
			assert.Equal(t, "ServiceMonitor,ArgoApplication", args[skipIdx+1])
			// manifestDir must be the last positional argument, preceded by "--".
			require.GreaterOrEqual(t, len(args), 2)
			assert.Equal(t, "--", args[len(args)-2], "-- separator missing before manifestDir")
			assert.Equal(t, manifestDir, args[len(args)-1])
			return validResourcesJSON, "", nil
		})

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
		SkipKinds: []string{"ServiceMonitor", "ArgoApplication"},
	}

	_, err := v.Validate(context.Background(), "src", manifestDir)
	require.NoError(t, err)
}

func TestKubeconformValidator_RejectsInvalidSkipKind(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	// No EXPECT() — any call to Run must not happen.

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
		SkipKinds: []string{"ValidKind", "bad-kind!"},
	}

	_, err := v.Validate(context.Background(), "src", "/tmp/templates/src")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid kind name")
	assert.Contains(t, err.Error(), "bad-kind!")
}

func TestKubeconformValidator_RejectsInvalidPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	// No EXPECT() — any call to Run must not happen.

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform; rm -rf /",
	}

	_, err := v.Validate(context.Background(), "src", "/tmp/templates/src")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestKubeconformValidator_RejectsEmptyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	// No EXPECT() — any call to Run must not happen.

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "",
	}

	_, err := v.Validate(context.Background(), "src", "/tmp/templates/src")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestKubeconformValidator_RejectsNilCmdRunner(t *testing.T) {
	// Guards against hand-constructed KubeconformValidator{} that bypasses the
	// default-injection in app.New(). A clear error beats a nil-pointer panic.
	v := &KubeconformValidator{
		Path: "kubeconform",
	}

	_, err := v.Validate(context.Background(), "src", "/tmp/templates/src")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "command runner is required")
}

func TestKubeconformValidator_HandlesMalformedJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	mockCmdRunner.EXPECT().
		Run(gomock.Any(), "kubeconform", gomock.Any()).
		Return("not json {{{", "", nil)

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
	}

	_, err := v.Validate(context.Background(), "src", "/tmp/templates/src")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestKubeconformValidator_PropagatesNonExitErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	// Non-exit errors (e.g. "executable file not found") should propagate so the
	// caller can detect missing-binary situations.
	mockCmdRunner.EXPECT().
		Run(gomock.Any(), "kubeconform", gomock.Any()).
		Return("", "exec: \"kubeconform\": executable file not found in $PATH", errors.New("executable file not found in $PATH"))

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
	}

	_, err := v.Validate(context.Background(), "src", "/tmp/templates/src")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to run")
}

func TestIsValidKindName(t *testing.T) {
	tests := []struct {
		name string
		kind string
		want bool
	}{
		{"empty string", "", false},
		{"leading digit", "1Pod", false},
		{"all digits", "123", false},
		{"digit after letter ok", "Deploy2", true},
		{"mixed alnum ok", "App2Service3", true},
		{"single uppercase letter", "A", true},
		{"lowercase first letter rejected", "deployment", false},
		{"single lowercase letter rejected", "a", false},
		{"valid pascal case", "ServiceMonitor", true},
		{"hyphen rejected", "Service-Monitor", false},
		{"underscore rejected", "Service_Monitor", false},
		{"space rejected", "Service Monitor", false},
		{"dot rejected", "Service.Monitor", false},
		{"unicode rejected", "Sërvice", false},
		{"trailing exclamation rejected", "Service!", false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := isValidKindName(tt.kind)
			assert.Equal(t, tt.want, got, "isValidKindName(%q)", tt.kind)
		})
	}
}

func TestKubeconformValidator_RejectsEmptyStringInSkipKinds(t *testing.T) {
	// Programmatic callers may pass an empty string; the validator must reject it
	// before invoking kubeconform.
	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	// No EXPECT() — any call to Run must not happen.

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
		SkipKinds: []string{"ValidKind", ""},
	}

	_, err := v.Validate(context.Background(), "src", "/tmp/templates/src")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid kind name")
}

func TestKubeconformValidator_RejectsDigitFirstSkipKind(t *testing.T) {
	// Kind names must start with a letter; a digit-first kind must be rejected.
	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	// No EXPECT() — any call to Run must not happen.

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
		SkipKinds: []string{"1BadKind"},
	}

	_, err := v.Validate(context.Background(), "src", "/tmp/templates/src")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid kind name")
	assert.Contains(t, err.Error(), "1BadKind")
}

func TestKubeconformValidator_FilenameIsRelativeToManifestDir(t *testing.T) {
	// kubeconform reports absolute paths under the per-invocation tmpdir.
	// Surfacing them raw would make MR comments non-deterministic across runs
	// (defeating GitLab's note dedup) and bury the useful path inside noise.
	// The adapter must strip the manifestDir prefix at the boundary.
	const manifestDir = "/tmp/argo-compare-12345/templates/src"
	const rawJSON = `{
	  "resources": [
	    {"filename": "/tmp/argo-compare-12345/templates/src/templates/deployment.yaml", "kind": "Deployment", "name": "broken", "status": "statusInvalid", "msg": "field required"}
	  ],
	  "summary": {"valid": 0, "invalid": 1, "errors": 0, "skipped": 0}
	}`

	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	mockCmdRunner.EXPECT().
		Run(gomock.Any(), "kubeconform", gomock.Any()).
		Return(rawJSON, "", &exec.ExitError{})

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
	}

	result, err := v.Validate(context.Background(), "src", manifestDir)

	require.NoError(t, err)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "templates/deployment.yaml", result.Errors[0].Filename)
}

func TestKubeconformValidator_FilenameFallsBackToBase(t *testing.T) {
	// When kubeconform reports a filename outside the manifestDir (filepath.Rel
	// returns a ".."-prefixed path) the adapter must fall back to filepath.Base
	// so the bullet still has a meaningful identifier rather than ../../foo.yaml.
	const manifestDir = "/tmp/argo-compare-12345/templates/src"
	const rawJSON = `{
	  "resources": [
	    {"filename": "/some/other/path/file.yaml", "kind": "Service", "name": "broken", "status": "statusInvalid", "msg": "port required"}
	  ],
	  "summary": {"valid": 0, "invalid": 1, "errors": 0, "skipped": 0}
	}`

	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	mockCmdRunner.EXPECT().
		Run(gomock.Any(), "kubeconform", gomock.Any()).
		Return(rawJSON, "", &exec.ExitError{})

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
	}

	result, err := v.Validate(context.Background(), "src", manifestDir)

	require.NoError(t, err)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "file.yaml", result.Errors[0].Filename)
}

func TestKubeconformValidator_FilenameRelativePathStartingWithDotDotName(t *testing.T) {
	// Regression: `strings.HasPrefix(rel, "..")` matched valid directory names like
	// "..hidden/file.yaml". Only the path-traversal sequences ".." and "../" should
	// trigger the fallback; a name that merely starts with ".." as a substring must
	// not be treated as an escape.
	const manifestDir = "/srv/templates"
	const rawJSON = `{
	  "resources": [
	    {"filename": "/srv/templates/..hidden/file.yaml", "kind": "Deployment", "name": "ok", "status": "statusInvalid", "msg": "error"}
	  ],
	  "summary": {"valid": 0, "invalid": 1, "errors": 0, "skipped": 0}
	}`

	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	mockCmdRunner.EXPECT().
		Run(gomock.Any(), "kubeconform", gomock.Any()).
		Return(rawJSON, "", &exec.ExitError{})

	v := &KubeconformValidator{CmdRunner: mockCmdRunner, Path: "kubeconform"}

	result, err := v.Validate(context.Background(), "src", manifestDir)

	require.NoError(t, err)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "..hidden/file.yaml", result.Errors[0].Filename)
}

func TestKubeconformValidator_FilenameIsManifestDirItselfFallsBackToBase(t *testing.T) {
	// filepath.Rel returns "." when raw == manifestDir. Emitting "." as a filename
	// bullet would be confusing; fall back to the base name (which is the last
	// segment of the directory path).
	const manifestDir = "/srv/templates"
	const rawJSON = `{
	  "resources": [
	    {"filename": "/srv/templates", "kind": "Service", "name": "broken", "status": "statusInvalid", "msg": "error"}
	  ],
	  "summary": {"valid": 0, "invalid": 1, "errors": 0, "skipped": 0}
	}`

	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	mockCmdRunner.EXPECT().
		Run(gomock.Any(), "kubeconform", gomock.Any()).
		Return(rawJSON, "", &exec.ExitError{})

	v := &KubeconformValidator{CmdRunner: mockCmdRunner, Path: "kubeconform"}

	result, err := v.Validate(context.Background(), "src", manifestDir)

	require.NoError(t, err)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "templates", result.Errors[0].Filename)
}

func TestKubeconformValidator_FilenameEmptyStaysEmpty(t *testing.T) {
	// Defensive: if kubeconform reports an empty filename, the adapter must
	// preserve it rather than emit "." (the filepath.Rel result for empty input).
	const manifestDir = "/tmp/argo-compare-12345/templates/src"
	const rawJSON = `{
	  "resources": [
	    {"filename": "", "kind": "Service", "name": "broken", "status": "statusInvalid", "msg": "port required"}
	  ],
	  "summary": {"valid": 0, "invalid": 1, "errors": 0, "skipped": 0}
	}`

	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	mockCmdRunner.EXPECT().
		Run(gomock.Any(), "kubeconform", gomock.Any()).
		Return(rawJSON, "", &exec.ExitError{})

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
	}

	result, err := v.Validate(context.Background(), "src", manifestDir)

	require.NoError(t, err)
	require.Len(t, result.Errors, 1)
	assert.Empty(t, result.Errors[0].Filename)
}

func TestCleanFilenameEmptyManifestDir(t *testing.T) {
	// Defensive: when manifestDir is empty the Rel branch is skipped and
	// cleanFilename must fall straight through to filepath.Base so the
	// caller still gets a meaningful identifier.
	assert.Equal(t, "file.yaml", cleanFilename("", "/tmp/deep/path/file.yaml"))
	// Empty raw with empty manifestDir must remain empty (no spurious ".").
	assert.Equal(t, "", cleanFilename("", ""))
}

func TestKubeconformValidator_ExitErrorWithEmptyStdout(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	// kubeconform exits non-zero with empty stdout when it fails early (e.g. bad
	// flag, missing schema-location). The stderr message should surface to the caller.
	mockCmdRunner.EXPECT().
		Run(gomock.Any(), "kubeconform", gomock.Any()).
		Return("", "unknown flag: -bogus", &exec.ExitError{})

	v := &KubeconformValidator{
		CmdRunner: mockCmdRunner,
		Path:      "kubeconform",
	}

	_, err := v.Validate(context.Background(), "src", "/tmp/templates/src")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exited with error")
	assert.Contains(t, err.Error(), "unknown flag")
}
