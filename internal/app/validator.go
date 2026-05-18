package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

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

	return buildValidationResult(target, parsed), nil
}

// isValidKindName reports whether s is a valid Kubernetes resource kind identifier.
// Kind names are PascalCase ASCII identifiers: start with a letter, followed by
// letters or digits only.
func isValidKindName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
			// ok at any position
		case r >= '0' && r <= '9':
			if i == 0 {
				return false // must start with a letter
			}
		default:
			return false
		}
	}
	return true
}

// buildValidationResult converts kubeconform JSON output into a ValidationResult.
func buildValidationResult(target string, parsed kubeconformOutput) ports.ValidationResult {
	result := ports.ValidationResult{
		Target:        target,
		ResourceCount: len(parsed.Resources),
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
