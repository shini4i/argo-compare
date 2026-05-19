package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/internal/ports"
)

// Kubeconform status strings as emitted by `kubeconform -output json`.
const (
	kubeconformStatusInvalid = "statusInvalid"
	kubeconformStatusError   = "statusError"
)

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
	// Log surfaces kubeconform's stderr and command line so failures (e.g. schema
	// download issues, "no files found" warnings) are visible in CI logs. Optional.
	Log *logging.Logger
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

	args := []string{
		"-output", "json",
		"-strict",
		"-schema-location", "default",
	}
	if len(v.SkipKinds) > 0 {
		for _, kind := range v.SkipKinds {
			if !isValidKindName(kind) {
				return ports.ValidationResult{}, fmt.Errorf("invalid kind name in SkipKinds: %q", kind)
			}
		}
		args = append(args, "-skip", strings.Join(v.SkipKinds, ","))
	}
	// "--" terminates options so manifestDir cannot be interpreted as a flag.
	args = append(args, "--", manifestDir)

	if v.Log != nil {
		v.Log.Debugf("Running kubeconform: %s %s", v.Path, strings.Join(args, " "))
	}

	stdout, stderr, err := v.CmdRunner.Run(ctx, v.Path, args...)

	// Surface kubeconform's stderr in CI logs so warnings (e.g. schema download
	// issues, files skipped, "no resources found") are visible. Stderr is empty
	// on a clean successful run; non-empty stderr always means something worth
	// seeing happened.
	if v.Log != nil && strings.TrimSpace(stderr) != "" {
		v.Log.Warningf("kubeconform stderr: %s", strings.TrimSpace(stderr))
	}

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

	if v.Log != nil {
		v.Log.Debugf("kubeconform stdout: %s", strings.TrimSpace(stdout))
	}

	return buildValidationResult(target, parsed), nil
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
// kubeconform's non-verbose JSON output only lists failed resources in the resources array;
// the total count of processed resources is only available via the Summary fields.
func buildValidationResult(target string, parsed kubeconformOutput) ports.ValidationResult {
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
			Filename: r.Filename,
			Kind:     r.Kind,
			Name:     r.Name,
			Message:  r.Msg,
		})
	}

	return result
}
