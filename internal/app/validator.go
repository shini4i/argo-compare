package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/shini4i/argo-compare/internal/ports"
)

// Kubeconform status strings as emitted by `kubeconform -output json`.
const (
	kubeconformStatusInvalid = "statusInvalid"
	kubeconformStatusError   = "statusError"
)

// kubeconformDefaultSchemaLocation is kubeconform's built-in schema registry,
// always passed first so user-supplied locations are tried afterwards.
const kubeconformDefaultSchemaLocation = "default"

// KubeconformValidator validates rendered manifests by shelling out to the
// kubeconform CLI. It implements ports.ManifestValidator.
type KubeconformValidator struct {
	// CmdRunner executes the kubeconform binary. Required.
	CmdRunner ports.CmdRunner
	// Path is the kubeconform binary (name resolved via PATH, or an absolute path).
	Path string
	// SkipKinds is an optional list of resource kinds to skip during validation
	// (passed through as `-skip Kind1,Kind2`).
	SkipKinds []string
	// SchemaLocations holds additional `-schema-location` values appended after the
	// hardcoded `default` registry. Each entry is a registry name, local path, or
	// URL template understood by kubeconform (e.g. the datreeio/CRDs-catalog URL
	// with `{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json`). Order is
	// preserved because kubeconform tries locations in the order they appear.
	SchemaLocations []string
}

// kubeconformOutput mirrors the JSON structure produced by `kubeconform -output json`.
type kubeconformOutput struct {
	Resources []kubeconformResource `json:"resources"`
	Summary   kubeconformSummary    `json:"summary"`
}

type kubeconformResource struct {
	Filename string `json:"filename"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Msg      string `json:"msg"`
}

type kubeconformSummary struct {
	Valid   int `json:"valid"`
	Invalid int `json:"invalid"`
	Errors  int `json:"errors"`
	Skipped int `json:"skipped"`
}

// Validate runs kubeconform against manifestDir and returns a structured
// ValidationResult tagged with target. Schema failures in user manifests are
// reported via the result (Valid=false, populated Errors); only failures of
// the validator itself (missing binary, malformed JSON, etc.) are returned
// as errors.
func (v *KubeconformValidator) Validate(ctx context.Context, target, manifestDir string) (ports.ValidationResult, error) {
	if v.CmdRunner == nil {
		return ports.ValidationResult{}, errors.New("kubeconform command runner is required")
	}
	if err := validateExecutable("kubeconform binary path", v.Path); err != nil {
		return ports.ValidationResult{}, err
	}

	args, err := v.buildValidateArgs(manifestDir)
	if err != nil {
		return ports.ValidationResult{}, err
	}

	stdout, stderr, err := v.CmdRunner.Run(ctx, v.Path, args...)

	// kubeconform exits non-zero when manifests are invalid; that is an expected
	// outcome, not a validator failure. Treat *exec.ExitError as benign and proceed
	// to parse stdout. Any other error means kubeconform itself could not run.
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return ports.ValidationResult{}, fmt.Errorf("kubeconform failed to run: %w (stderr: %s)", err, strings.TrimSpace(stderr))
	}

	// When kubeconform exited with an error but produced no JSON, stderr carries
	// the actual failure reason (e.g. unknown flag, missing schema-location).
	if exitErr != nil && strings.TrimSpace(stdout) == "" {
		return ports.ValidationResult{}, fmt.Errorf("kubeconform exited with error (stderr: %s)", strings.TrimSpace(stderr))
	}

	var parsed kubeconformOutput
	if jsonErr := json.Unmarshal([]byte(stdout), &parsed); jsonErr != nil {
		return ports.ValidationResult{}, fmt.Errorf("could not parse kubeconform output: %w (stderr: %s)", jsonErr, strings.TrimSpace(stderr))
	}

	return buildValidationResult(target, manifestDir, parsed), nil
}

// buildValidateArgs assembles the kubeconform argument list, validating the
// configured SchemaLocations and SkipKinds along the way. The trailing "--"
// terminates options so manifestDir can never be interpreted as a flag.
func (v *KubeconformValidator) buildValidateArgs(manifestDir string) ([]string, error) {
	args := []string{
		"-output", "json",
		"-strict",
		"-summary",
		"-schema-location", kubeconformDefaultSchemaLocation,
	}
	for _, loc := range v.SchemaLocations {
		if strings.TrimSpace(loc) == "" {
			return nil, fmt.Errorf("empty schema location in SchemaLocations")
		}
		args = append(args, "-schema-location", loc)
	}
	if len(v.SkipKinds) > 0 {
		for _, kind := range v.SkipKinds {
			if !isValidKindName(kind) {
				return nil, fmt.Errorf("invalid kind name in SkipKinds: %q", kind)
			}
		}
		args = append(args, "-skip", strings.Join(v.SkipKinds, ","))
	}
	args = append(args, "--", manifestDir)
	return args, nil
}

// isValidKindName reports whether s is a valid Kubernetes resource kind identifier.
// Kubernetes API conventions require Kinds to be PascalCase: ^[A-Z][A-Za-z0-9]*$.
// Lowercase-first names are rejected because kubeconform's -skip is case-sensitive
// and accepting them would silently no-op (e.g. "deployment" would not skip Deployments).
func isValidKindName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case i == 0:
			if !(r >= 'A' && r <= 'Z') {
				return false // must start with an uppercase letter
			}
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			// ok at any non-first position
		default:
			return false
		}
	}
	return true
}

// buildValidationResult converts kubeconform JSON output into a ValidationResult.
// kubeconform's JSON output only lists failed resources in the resources array;
// the total count of processed resources comes from the Summary fields, which
// are only emitted when -summary is passed (we add it in Validate above).
// manifestDir is the path passed to kubeconform; it is used to convert the
// absolute filenames kubeconform emits into paths relative to that directory.
func buildValidationResult(target, manifestDir string, parsed kubeconformOutput) ports.ValidationResult {
	result := ports.ValidationResult{
		Target:        target,
		ResourceCount: parsed.Summary.Valid + parsed.Summary.Invalid + parsed.Summary.Errors + parsed.Summary.Skipped,
		ErrorCount:    parsed.Summary.Invalid + parsed.Summary.Errors,
	}
	result.Valid = result.ErrorCount == 0

	for _, r := range parsed.Resources {
		if r.Status != kubeconformStatusInvalid && r.Status != kubeconformStatusError {
			continue
		}
		result.Errors = append(result.Errors, ports.ValidationError{
			Filename: cleanFilename(manifestDir, r.Filename),
			Kind:     r.Kind,
			Name:     r.Name,
			Message:  r.Msg,
		})
	}

	return result
}

// cleanFilename normalizes a filename emitted by kubeconform into a path
// relative to manifestDir. kubeconform reports absolute paths under a
// per-invocation tmpdir; surfacing them raw would make the MR comment churn
// across runs (defeating GitLab's note dedup) and bury the useful name inside
// noise. When the file lies outside manifestDir, or the relative result is
// ambiguous ("." when raw equals manifestDir), fall back to the base name so
// the bullet has a meaningful identifier. When manifestDir is empty, the base
// name is returned directly.
func cleanFilename(manifestDir, raw string) string {
	if raw == "" {
		return ""
	}
	if manifestDir != "" {
		if rel, err := filepath.Rel(manifestDir, raw); err == nil &&
			rel != "." &&
			rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return rel
		}
	}
	return filepath.Base(raw)
}
