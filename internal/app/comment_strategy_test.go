package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubPoster struct {
	bodies []string
	err    error
}

func (s *stubPoster) Post(_ context.Context, body string) error {
	s.bodies = append(s.bodies, body)
	return s.err
}

func setupSilentLogger(name string, t *testing.T) *logging.Logger {
	logger := logging.MustGetLogger(name)
	logging.SetBackend(logging.NewLogBackend(io.Discard, "", 0))
	t.Cleanup(func() {
		logging.SetBackend(logging.NewLogBackend(os.Stdout, "", 0))
	})
	return logger
}

func TestCommentStrategyPresentWithDiff(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-diff", t)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ShowAdded:       true,
		ShowRemoved:     true,
		ApplicationPath: "apps/ingress.yaml",
	}

	result := ComparisonResult{
		Added: []DiffOutput{
			{File: File{Name: "/added.yaml"}, Diff: "--- /tmp/src\n+++ /tmp/dst\n+ added"},
		},
		Changed: []DiffOutput{
			{File: File{Name: "changed.yaml"}, Diff: "--- /tmp/src\n+++ /tmp/dst\n@@ diff\n- old\n+ new"},
		},
		Removed: []DiffOutput{
			{File: File{Name: "/removed.yaml"}, Diff: "--- /tmp/src\n+++ /tmp/dst\n- removed"},
		},
	}

	require.NoError(t, strategy.Present(context.Background(), result))
	require.Len(t, poster.bodies, 1)
	body := poster.bodies[0]
	assert.Contains(t, body, "## Argo Compare Results")
	assert.Contains(t, body, "**Application:** `apps/ingress.yaml`")
	assert.Contains(t, body, "**Summary**")
	assert.Contains(t, body, "- Added: 1")
	assert.Contains(t, body, "<summary>Added • added.yaml</summary>")
	assert.Contains(t, body, "<summary>Changed • changed.yaml</summary>")
	assert.Contains(t, body, "<summary>Removed • removed.yaml</summary>")
	assert.Contains(t, body, "```diff")
	assert.Contains(t, body, "@@ diff")
	assert.NotContains(t, body, "--- /")
	assert.NotContains(t, body, "+++ /")
	assert.Contains(t, body, "+ added")
	assert.Contains(t, body, "- old")
	assert.Contains(t, body, "+ new")
	assert.Contains(t, body, "- removed")
}

func TestCommentStrategyPresentNoDiff(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-empty", t)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ApplicationPath: "apps/foo.yaml",
	}

	require.NoError(t, strategy.Present(context.Background(), ComparisonResult{}))
	require.Len(t, poster.bodies, 1)
	assert.Contains(t, poster.bodies[0], "No manifest differences detected")
	assert.Contains(t, poster.bodies[0], "**Application:** `apps/foo.yaml`")
}

func TestCommentStrategyRequiresPoster(t *testing.T) {
	logger := setupSilentLogger("comment-missing", t)

	strategy := CommentStrategy{
		Log:             logger,
		ApplicationPath: "apps/foo.yaml",
	}

	err := strategy.Present(context.Background(), ComparisonResult{})
	require.Error(t, err)
}

func TestCommentStrategyRequiresLogger(t *testing.T) {
	strategy := CommentStrategy{
		Poster:          &stubPoster{},
		ApplicationPath: "apps/foo.yaml",
	}

	err := strategy.Present(context.Background(), ComparisonResult{})
	require.Error(t, err)
}

func TestCommentStrategySplitsLargeBody(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-large", t)

	largeDiff := strings.Repeat("+ oversized line\n", 160000)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ShowAdded:       true,
		ApplicationPath: "apps/big.yaml",
	}

	result := ComparisonResult{
		Added: []DiffOutput{{File: File{Name: "big.yaml"}, Diff: largeDiff}},
	}

	require.NoError(t, strategy.Present(context.Background(), result))
	assert.Greater(t, len(poster.bodies), 1)
	assert.Contains(t, poster.bodies[0], "Part 1 of")
	assert.Contains(t, poster.bodies[len(poster.bodies)-1], "Part "+fmt.Sprint(len(poster.bodies))+" of "+fmt.Sprint(len(poster.bodies)))
}

func TestCommentStrategyNotesHiddenSections(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-hidden", t)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ShowAdded:       false,
		ShowRemoved:     false,
		ApplicationPath: "apps/partial.yaml",
	}

	result := ComparisonResult{
		Added:   []DiffOutput{{File: File{Name: "/new.yaml"}, Diff: "+ new"}},
		Removed: []DiffOutput{{File: File{Name: "/old.yaml"}, Diff: "- old"}},
	}

	require.NoError(t, strategy.Present(context.Background(), result))
	require.Len(t, poster.bodies, 1)
	body := poster.bodies[0]
	assert.Contains(t, body, "(not shown)")
	assert.Contains(t, body, "Added manifests (1) are present but not shown")
	assert.Contains(t, body, "Removed manifests (1) are present but not shown")
}

func TestCommentStrategyStripsDiffHeaders(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-headers", t)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ApplicationPath: "apps/sample.yaml",
	}

	diff := "--- /tmp/argo-compare-123/src.yaml\n+++ /tmp/argo-compare-123/dst.yaml\n@@ diff"
	result := ComparisonResult{
		Changed: []DiffOutput{{File: File{Name: "/manifests/sample.yaml"}, Diff: diff}},
	}

	require.NoError(t, strategy.Present(context.Background(), result))
	require.Len(t, poster.bodies, 1)
	body := poster.bodies[0]
	assert.NotContains(t, body, "--- ")
	assert.NotContains(t, body, "+++ ")
	assert.Contains(t, body, "@@ diff")
}

// TestCommentStrategySkipsCRDManifests ensures CRD manifest diffs are replaced
// with a concise notice in posted comments.
func TestCommentStrategySkipsCRDManifests(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-crd", t)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ApplicationPath: "apps/crd.yaml",
	}

	diff := "--- a/crd.yaml\n+++ b/crd.yaml\n+ kind: CustomResourceDefinition\n+ metadata: {}"
	result := ComparisonResult{
		Changed: []DiffOutput{{File: File{Name: "/crds/crd.yaml"}, Diff: diff}},
	}

	require.NoError(t, strategy.Present(context.Background(), result))
	require.Len(t, poster.bodies, 1)
	body := poster.bodies[0]
	assert.Contains(t, body, "**CRD Notes**")
	assert.Contains(t, body, "CRD manifest `crds/crd.yaml`")
	assert.Contains(t, body, "Diff omitted")
	assert.NotContains(t, body, "kind: CustomResourceDefinition")
	if lastDetails := strings.LastIndex(body, "</details>"); lastDetails != -1 {
		tail := body[lastDetails+len("</details>"):]
		assert.Contains(t, tail, "**CRD Notes**", "CRD notes should appear after diff sections")
	}
}

func TestCommentStrategyIgnoresNonCRDManifests(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-non-crd", t)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ApplicationPath: "apps/credentials.yaml",
	}

	diff := "--- a/config/credentials.yaml\n+++ b/config/credentials.yaml\n+ username: demo"
	result := ComparisonResult{
		Changed: []DiffOutput{{File: File{Name: "/config/credentials.yaml"}, Diff: diff}},
	}

	require.NoError(t, strategy.Present(context.Background(), result))
	require.Len(t, poster.bodies, 1)
	body := poster.bodies[0]
	assert.Contains(t, body, "credentials.yaml")
	assert.NotContains(t, body, "CRD Notes")
}

func TestBuildValidationSummaryEmpty(t *testing.T) {
	result := buildValidationSummary(nil)
	assert.Empty(t, result)

	result = buildValidationSummary(map[string]ports.ValidationResult{})
	assert.Empty(t, result)
}

func TestBuildValidationSummaryAllValid(t *testing.T) {
	results := map[string]ports.ValidationResult{
		"src": {Target: "src", Valid: true, ResourceCount: 5},
	}

	summary := buildValidationSummary(results)

	assert.Contains(t, summary, "**Validation**")
	assert.Contains(t, summary, "✓")
	assert.Contains(t, summary, "5/5 valid")
}

func TestBuildValidationSummaryWithErrors(t *testing.T) {
	// Locks in the exact rendered block so accidental format drift is caught.
	// Each resource is a parent bullet with filename; each issue from the
	// kubeconform message becomes a nested sub-bullet.
	results := map[string]ports.ValidationResult{
		"dst": {
			Target:        "dst",
			Valid:         false,
			ResourceCount: 3,
			ErrorCount:    2,
			Errors: []ports.ValidationError{
				{Kind: "Deployment", Name: "broken", Filename: "templates/deployment.yaml", Message: "missing field spec.selector"},
				{Kind: "Service", Name: "svc", Filename: "templates/service.yaml", Message: "invalid port"},
			},
		},
	}

	summary := buildValidationSummary(results)

	expected := "**Validation**\n" +
		"- ✗ 1/3 valid\n" +
		"  - `Deployment.broken` — `templates/deployment.yaml`:\n" +
		"    - missing field spec.selector\n" +
		"  - `Service.svc` — `templates/service.yaml`:\n" +
		"    - invalid port\n\n"
	assert.Equal(t, expected, summary)
}

func TestBuildValidationSummaryMultiIssueMessage(t *testing.T) {
	// Kubeconform packs multiple schema failures for a single resource into one
	// msg field separated by newlines. Each non-empty line must render as its
	// own sub-bullet so issues stay individually scannable.
	results := map[string]ports.ValidationResult{
		"src": {
			Target:        "src",
			Valid:         false,
			ResourceCount: 1,
			ErrorCount:    1,
			Errors: []ports.ValidationError{
				{
					Kind:     "Deployment",
					Name:     "api",
					Filename: "templates/deployment.yaml",
					Message:  "/spec/replicas: expected integer, got string\n/spec/containers/0/image: required field missing\n\n/spec/strategy/type: invalid value",
				},
			},
		},
	}

	summary := buildValidationSummary(results)

	expected := "**Validation**\n" +
		"- ✗ 0/1 valid\n" +
		"  - `Deployment.api` — `templates/deployment.yaml`:\n" +
		"    - /spec/replicas: expected integer, got string\n" +
		"    - /spec/containers/0/image: required field missing\n" +
		"    - /spec/strategy/type: invalid value\n\n"
	assert.Equal(t, expected, summary)
}

func TestBuildValidationSummaryOmitsFilenameWhenEmpty(t *testing.T) {
	// Defensive: if the adapter could not produce a meaningful filename, render
	// the parent bullet without the em-dash filename slot so we don't show
	// "Kind.Name — `` :" with an empty backtick pair.
	results := map[string]ports.ValidationResult{
		"src": {
			Target:        "src",
			Valid:         false,
			ResourceCount: 1,
			ErrorCount:    1,
			Errors: []ports.ValidationError{
				{Kind: "Service", Name: "broken", Message: "port required"},
			},
		},
	}

	summary := buildValidationSummary(results)

	expected := "**Validation**\n" +
		"- ✗ 0/1 valid\n" +
		"  - `Service.broken`:\n" +
		"    - port required\n\n"
	assert.Equal(t, expected, summary)
}

func TestBuildValidationSummaryEmptyMessageRendersParentOnly(t *testing.T) {
	// If the message is empty after splitting/trimming, emit only the parent
	// bullet with a "(no message)" sentinel rather than a dangling colon.
	results := map[string]ports.ValidationResult{
		"src": {
			Target:        "src",
			Valid:         false,
			ResourceCount: 1,
			ErrorCount:    1,
			Errors: []ports.ValidationError{
				{Kind: "Pod", Name: "ghost", Filename: "templates/pod.yaml", Message: ""},
			},
		},
	}

	summary := buildValidationSummary(results)

	expected := "**Validation**\n" +
		"- ✗ 0/1 valid\n" +
		"  - `Pod.ghost` — `templates/pod.yaml` (no message)\n\n"
	assert.Equal(t, expected, summary)
}

func TestBuildValidationSummaryEmptyFilenameAndEmptyMessage(t *testing.T) {
	// Covers the buildResourceHeader branch where err.Filename == "" AND the
	// message produces no issues: emit only "Kind.Name (no message)" without
	// the em-dash filename slot or a dangling colon.
	results := map[string]ports.ValidationResult{
		"src": {
			Target:        "src",
			Valid:         false,
			ResourceCount: 1,
			ErrorCount:    1,
			Errors: []ports.ValidationError{
				{Kind: "Service", Name: "broken", Message: ""},
			},
		},
	}

	summary := buildValidationSummary(results)

	expected := "**Validation**\n" +
		"- ✗ 0/1 valid\n" +
		"  - `Service.broken` (no message)\n\n"
	assert.Equal(t, expected, summary)
}

func TestBuildValidationSummaryWhitespaceOnlyMessage(t *testing.T) {
	// A message containing only whitespace lines must be treated the same as
	// an empty message: no sub-bullets, parent rendered with "(no message)".
	results := map[string]ports.ValidationResult{
		"src": {
			Target:        "src",
			Valid:         false,
			ResourceCount: 1,
			ErrorCount:    1,
			Errors: []ports.ValidationError{
				{Kind: "Pod", Name: "ghost", Filename: "pod.yaml", Message: "   \n\t\n  "},
			},
		},
	}

	summary := buildValidationSummary(results)

	expected := "**Validation**\n" +
		"- ✗ 0/1 valid\n" +
		"  - `Pod.ghost` — `pod.yaml` (no message)\n\n"
	assert.Equal(t, expected, summary)
}

func TestBuildValidationSummaryWindowsLineEndings(t *testing.T) {
	// kubeconform messages may arrive with CRLF line endings depending on the
	// environment. Each non-empty line must still render as its own sub-bullet
	// and the trailing CR must not leak into the rendered output.
	results := map[string]ports.ValidationResult{
		"src": {
			Target:        "src",
			Valid:         false,
			ResourceCount: 1,
			ErrorCount:    1,
			Errors: []ports.ValidationError{
				{
					Kind:     "Deployment",
					Name:     "api",
					Filename: "deployment.yaml",
					Message:  "first failure\r\nsecond failure\r\n",
				},
			},
		},
	}

	summary := buildValidationSummary(results)

	expected := "**Validation**\n" +
		"- ✗ 0/1 valid\n" +
		"  - `Deployment.api` — `deployment.yaml`:\n" +
		"    - first failure\n" +
		"    - second failure\n\n"
	assert.Equal(t, expected, summary)
}

func TestEscapeInlineMarkdownBackslashOnly(t *testing.T) {
	// A bare backslash must become a doubled backslash so a downstream backtick
	// escape cannot accidentally form a CommonMark backslash-escape sequence.
	assert.Equal(t, `\\`, escapeInlineMarkdown(`\`))
	// Pre-existing double backslash must remain balanced after escaping.
	assert.Equal(t, `\\\\`, escapeInlineMarkdown(`\\`))
	// Backslash followed by backtick: backslash must be escaped first so the
	// backtick is still escaped independently.
	assert.Equal(t, "\\\\\\`", escapeInlineMarkdown("\\`"))
}

func TestBuildValidationSummaryInvocationError(t *testing.T) {
	results := map[string]ports.ValidationResult{
		"src": {Target: "src", InvocationError: "executable file not found in $PATH"},
	}

	summary := buildValidationSummary(results)

	assert.Contains(t, summary, "**Validation**")
	assert.Contains(t, summary, "✗")
	assert.Contains(t, summary, "validator could not run")
	assert.Contains(t, summary, "executable file not found")
}

func TestBuildValidationSummaryEscapesMarkdownInjection(t *testing.T) {
	// Crafted manifest fields could otherwise break out of the inline-code span
	// or inject control characters that disrupt the bullet layout. Backticks
	// must be escaped in every slot (Kind, Name, Filename, issue text), and CR
	// must be normalized so a single message line stays on a single sub-bullet.
	// Backslashes must be escaped before backticks so a trailing `\` does not
	// create a CommonMark escape sequence that leaves the backtick unescaped.
	results := map[string]ports.ValidationResult{
		"src": {
			Target:        "src",
			Valid:         false,
			ResourceCount: 1,
			ErrorCount:    1,
			Errors: []ports.ValidationError{
				{
					Kind:     "Deployment`hax",
					Name:     "evil`name",
					Filename: "evil`file.yaml",
					Message:  "line1\nline2\rwith CR\nline3`hax",
				},
			},
		},
	}

	summary := buildValidationSummary(results)

	expected := "**Validation**\n" +
		"- ✗ 0/1 valid\n" +
		"  - `Deployment\\`hax.evil\\`name` — `evil\\`file.yaml`:\n" +
		"    - line1\n" +
		"    - line2 with CR\n" +
		"    - line3\\`hax\n\n"
	assert.Equal(t, expected, summary)
}

func TestBuildValidationSummaryEscapesBackslash(t *testing.T) {
	// A trailing backslash before a backtick must not leave the backtick
	// unescaped: `\\`` in CommonMark renders as literal `\` + open code-span.
	// escapeInlineMarkdown must escape backslashes first.
	results := map[string]ports.ValidationResult{
		"src": {
			Target:        "src",
			Valid:         false,
			ResourceCount: 1,
			ErrorCount:    1,
			Errors: []ports.ValidationError{
				{
					Kind:     "Service",
					Name:     "broken",
					Filename: "path\\with\\backslash.yaml",
					Message:  "field\\value: unexpected",
				},
			},
		},
	}

	summary := buildValidationSummary(results)

	expected := "**Validation**\n" +
		"- ✗ 0/1 valid\n" +
		"  - `Service.broken` — `path\\\\with\\\\backslash.yaml`:\n" +
		"    - field\\\\value: unexpected\n\n"
	assert.Equal(t, expected, summary)
}

func TestCommentStrategyIncludesValidationResults(t *testing.T) {
	poster := &stubPoster{}
	logger := setupSilentLogger("comment-validation", t)

	strategy := CommentStrategy{
		Log:             logger,
		Poster:          poster,
		ApplicationPath: "apps/app.yaml",
	}

	result := ComparisonResult{
		Changed: []DiffOutput{{File: File{Name: "/test.yaml"}, Diff: "--- a\n+++ b\n+ added"}},
		ValidationResults: map[string]ports.ValidationResult{
			"src": {Target: "src", Valid: true, ResourceCount: 3},
		},
	}

	require.NoError(t, strategy.Present(context.Background(), result))
	require.Len(t, poster.bodies, 1)
	body := poster.bodies[0]
	assert.Contains(t, body, "**Validation**")
	assert.Contains(t, body, "3/3 valid")
}
