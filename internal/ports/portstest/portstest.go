// Package portstest provides minimal hand-rolled fakes for the interfaces
// defined in internal/ports. They are intentionally small: each one satisfies
// a port with a no-op (or a single configurable error) so tests that need a
// "this dependency exists but does nothing" stand-in don't have to redeclare
// the same trivial type per file.
//
// For tests that need to assert call arguments or call counts, prefer the
// gomock-generated mocks in cmd/argo-compare/mocks. These helpers are only
// for cases where the test does not care what the dependency was called with.
package portstest

import (
	"context"
)

// NoopCmdRunner satisfies ports.CmdRunner by returning empty output and no error.
type NoopCmdRunner struct{}

// Run returns ("", "", nil) regardless of arguments.
func (NoopCmdRunner) Run(_ context.Context, _ string, _ ...string) (string, string, error) {
	return "", "", nil
}

// RunWithStdin returns ("", "", nil) regardless of arguments.
func (NoopCmdRunner) RunWithStdin(_ context.Context, _, _ string, _ ...string) (string, string, error) {
	return "", "", nil
}

// NoopFileReader satisfies ports.FileReader by returning (nil, nil), which the
// port contract treats as "file absent" (see ports.FileReader docstring).
type NoopFileReader struct{}

// ReadFile returns (nil, nil) regardless of the path.
func (NoopFileReader) ReadFile(string) ([]byte, error) { return nil, nil }

// ErrFileReader satisfies ports.FileReader by always returning the configured
// error. Useful for verifying that callers propagate I/O failures correctly.
type ErrFileReader struct{ Err error }

// ReadFile returns (nil, r.Err) regardless of the path.
func (r ErrFileReader) ReadFile(string) ([]byte, error) { return nil, r.Err }

// NoopGlobber satisfies ports.Globber by returning no matches and no error.
type NoopGlobber struct{}

// Glob returns (nil, nil) regardless of the pattern.
func (NoopGlobber) Glob(string) ([]string, error) { return nil, nil }
