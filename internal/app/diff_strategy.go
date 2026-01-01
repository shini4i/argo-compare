package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/op/go-logging"
)

// DiffPresenter presents comparison results to the user.
// The context can be used for cancellation and timeout control.
type DiffPresenter interface {
	Present(ctx context.Context, result ComparisonResult) error
}

const currentFilePrintPattern = "▶ %s"

// StdoutStrategy writes diff summaries to the configured logger.
type StdoutStrategy struct {
	Log         *logging.Logger
	ShowAdded   bool
	ShowRemoved bool
}

// ExternalDiffStrategy pipes unified diffs into an external command.
type ExternalDiffStrategy struct {
	Log         *logging.Logger
	Tool        string
	ShowAdded   bool
	ShowRemoved bool
}

// Present prints comparison results using the configured stdout logger.
// The context parameter is accepted for interface compliance but not used.
func (s StdoutStrategy) Present(_ context.Context, result ComparisonResult) error {
	if result.IsEmpty() {
		s.Log.Info("No diff was found in rendered manifests!")
		return nil
	}

	if s.ShowAdded {
		s.printSection("added", result.Added)
	}

	if s.ShowRemoved {
		s.printSection("removed", result.Removed)
	}

	s.printSection("changed", result.Changed)

	return nil
}

// printSection logs a summary of diff entries and prints their unified diffs.
func (s StdoutStrategy) printSection(operation string, entries []DiffOutput) {
	if len(entries) == 0 {
		return
	}

	fileText := "file"
	if len(entries) > 1 {
		fileText = "files"
	}

	s.Log.Infof("The following %d %s would be %s:", len(entries), fileText, operation)

	for _, entry := range entries {
		s.Log.Infof(currentFilePrintPattern, entry.File.Name)
		fmt.Println(entry.Diff)
	}
}

// Present streams diff content to the configured external tool.
// The context is used for cancellation of external tool execution.
func (s ExternalDiffStrategy) Present(ctx context.Context, result ComparisonResult) error {
	if result.IsEmpty() {
		s.Log.Info("No diff was found in rendered manifests!")
		return nil
	}

	if s.ShowAdded {
		if err := s.runSection(ctx, result.Added); err != nil {
			return err
		}
	}

	if s.ShowRemoved {
		if err := s.runSection(ctx, result.Removed); err != nil {
			return err
		}
	}

	return s.runSection(ctx, result.Changed)
}

// runSection streams a set of diff outputs through the configured external diff tool.
// It collects and returns all errors encountered during execution.
// The context is used for cancellation of each tool invocation.
func (s ExternalDiffStrategy) runSection(ctx context.Context, entries []DiffOutput) error {
	var errs []error
	for _, entry := range entries {
		if err := s.runTool(ctx, entry.Diff); err != nil {
			s.Log.Errorf("External diff tool failed for %s: %v", entry.File.Name, err)
			errs = append(errs, fmt.Errorf("%s: %w", entry.File.Name, err))
		}
	}
	return errors.Join(errs...)
}

// isValidToolChar returns true if the rune is allowed in a tool name.
// Allowed characters are ASCII letters, digits, dash (`-`), underscore (`_`), dot (`.`) and forward slash (`/`).
func isValidToolChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '-' || r == '_' || r == '.' || r == '/'
}

// validateToolName checks if the tool name contains only allowed characters.
// validateToolName checks that a tool name is non-empty, contains only allowed
// characters (letters, digits, '-', '_', '.', '/') and does not include path
// traversal sequences like "..". It returns an error describing the invalid name
// when a check fails.
func validateToolName(tool string) error {
	if tool == "" {
		return fmt.Errorf("invalid diff tool name: empty")
	}

	for _, r := range tool {
		if !isValidToolChar(r) {
			return fmt.Errorf("invalid diff tool name: %q", tool)
		}
	}

	// Reject path traversal
	if strings.Contains(tool, "..") {
		return fmt.Errorf("invalid diff tool name: %q", tool)
	}

	return nil
}

// runTool executes the external diff command with the given diff content.
// It validates the tool name to prevent command injection attacks.
// The context is used for cancellation of the command execution.
func (s ExternalDiffStrategy) runTool(ctx context.Context, diff string) error {
	if err := validateToolName(s.Tool); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, s.Tool) // #nosec G204 -- tool name is validated above
	cmd.Stdin = strings.NewReader(diff)

	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		fmt.Println(string(output))
	}

	return err
}