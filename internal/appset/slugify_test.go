package appset

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gosimple/slug"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSlugify pins ArgoCD's argument order: the name is always last, an
// optional max length comes first, and an optional smart-truncate flag second.
func TestSlugify(t *testing.T) {
	tests := []struct {
		name string
		args []any
		want string
	}{
		{name: "name only", args: []any{"Feature/ABC 123"}, want: "feature-abc-123"},
		{name: "max length", args: []any{7, "feature branch name"}, want: "feature"},
		{name: "smart truncate off cuts mid-word", args: []any{10, false, "feature branch name"}, want: "feature-br"},
		{name: "smart truncate on keeps whole words", args: []any{10, true, "feature branch name"}, want: "feature"},
		{name: "no arguments", args: nil, want: ""},
		// Zero length is asymmetric in the slug library, and argo-compare
		// inherits that from ArgoCD rather than smoothing it over.
		{name: "zero length truncates to nothing", args: []any{0, false, "feature branch"}, want: ""},
		{name: "zero length with smart truncate does not truncate", args: []any{0, true, "feature branch"}, want: "feature-branch"},
		// Only the first two options and the last argument are read.
		{name: "extra arguments are ignored", args: []any{10, false, "junk", "feature branch name"}, want: "feature-br"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := slugify(tt.args...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestSlugifyRejectsBadArguments keeps a mistyped argument from producing an
// empty name, which would generate an Application no branch actually has and
// make the diff wrong rather than absent.
func TestSlugifyRejectsBadArguments(t *testing.T) {
	tests := []struct {
		name string
		args []any
	}{
		{name: "name is not a string", args: []any{42}},
		{name: "length is not an int", args: []any{"not-an-int", "name"}},
		{name: "a float length, as fromYaml yields", args: []any{float64(10), "name"}},
		// sprig's add/sub/mul return int64, so this is the shape a computed
		// length actually arrives in.
		{name: "an int64 length, as sprig arithmetic yields", args: []any{int64(7), "name"}},
		{name: "flag is not a bool", args: []any{10, "not-a-bool", "name"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := slugify(tt.args...)
			require.Error(t, err)
			assert.Empty(t, got)
			assert.Contains(t, err.Error(), "slugify:")
		})
	}
}

// TestSlugifyRejectsNegativeLength covers the one argument the slug library
// answers with a slice-bounds panic rather than a value.
func TestSlugifyRejectsNegativeLength(t *testing.T) {
	got, err := slugify(-1, false, "feature branch")

	require.Error(t, err)
	assert.Empty(t, got)
	assert.Contains(t, err.Error(), "must not be negative")
}

// TestSlugifyRestoresGlobals observes gosimple/slug's package-level settings
// directly, because slugify assigns both on every call and so can never
// observe a leak through its own return value.
func TestSlugifyRestoresGlobals(t *testing.T) {
	slug.MaxLength, slug.EnableSmartTruncate = 17, false
	t.Cleanup(func() { slug.MaxLength, slug.EnableSmartTruncate = 0, true })

	_, err := slugify(5, true, "some name")
	require.NoError(t, err)

	assert.Equal(t, 17, slug.MaxLength, "the caller's setting must survive")
	assert.False(t, slug.EnableSmartTruncate, "the caller's setting must survive")
}

// TestSlugifyRestoresGlobalsAfterPanic proves the restore is deferred rather
// than merely trailing, so a panic inside the library cannot leak settings.
func TestSlugifyRestoresGlobalsAfterPanic(t *testing.T) {
	slug.MaxLength, slug.EnableSmartTruncate = 17, false
	t.Cleanup(func() { slug.MaxLength, slug.EnableSmartTruncate = 0, true })

	assert.Panics(t, func() { makeSlug("feature branch", -1, false) })

	assert.Equal(t, 17, slug.MaxLength)
	assert.False(t, slug.EnableSmartTruncate)
}

// TestSlugifyErrorFailsTheRender proves the error reaches the caller as a
// render failure rather than being swallowed into an unnamed Application.
func TestSlugifyErrorFailsTheRender(t *testing.T) {
	appSet := mustParse(t, `
kind: ApplicationSet
metadata:
  name: guestbook
spec:
  goTemplate: true
  generators:
    - list:
        elements:
          - id: 42
  template:
    metadata:
      name: '{{ slugify .id }}'
    spec:
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: 1.0.0
`)

	_, err := Expand(appSet, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generator element 0")
	assert.Contains(t, err.Error(), "slugify:")
}

// TestSlugifyDefaultLengthApplies keeps the documented 50-character default
// from drifting.
func TestSlugifyDefaultLengthApplies(t *testing.T) {
	got, err := slugify(strings.Repeat("a", 80))

	require.NoError(t, err)
	assert.Len(t, got, defaultSlugMaxLength)
}

// TestSlugifyIsSafeUnderConcurrency asserts values rather than relying on the
// race detector, which this project's test task does not enable. Without the
// mutex, workers read each other's truncation settings and return the wrong
// slug, so the failure shows up as a bad Application name.
func TestSlugifyIsSafeUnderConcurrency(t *testing.T) {
	cases := []struct {
		args []any
		want string
	}{
		{args: []any{7, "feature branch name"}, want: "feature"},
		{args: []any{10, false, "feature branch name"}, want: "feature-br"},
		{args: []any{"feature branch name"}, want: "feature-branch-name"},
	}

	var wg sync.WaitGroup
	failures := make(chan string, 300)

	for range 100 {
		for _, tc := range cases {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := slugify(tc.args...)
				if err != nil {
					failures <- err.Error()
					return
				}
				if got != tc.want {
					failures <- fmt.Sprintf("got %q, want %q", got, tc.want)
				}
			}()
		}
	}

	wg.Wait()
	close(failures)

	for failure := range failures {
		t.Error(failure)
	}
}
