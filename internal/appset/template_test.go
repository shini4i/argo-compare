package appset

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeProducesValidDNSNames pins the function that decides generated
// Application names; a name ArgoCD would reject makes the diff label wrong.
func TestNormalizeProducesValidDNSNames(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"lowercases", "Feature", "feature"},
		{"replaces unsupported characters", "feature/ABC_123", "feature-abc-123"},
		{"trims leading and trailing punctuation", ".Leading-and-trailing.", "leading-and-trailing"},
		{"collapses nothing", "already.valid-1", "already.valid-1"},
		{"multi-byte runes become hyphens", "café", "caf"},
		{"empty stays empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalize(tt.input)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, "_")
		})
	}
}

func TestNormalizeTruncatesToDNSLimit(t *testing.T) {
	got := normalize(strings.Repeat("a", 300))
	assert.Len(t, got, maxDNSNameLength)

	// Truncation must not leave a trailing hyphen, which is not a valid
	// DNS subdomain ending.
	trailing := normalize(strings.Repeat("a", 252) + strings.Repeat("_", 10))
	assert.False(t, strings.HasSuffix(trailing, "-"))
	assert.False(t, strings.HasPrefix(trailing, "-"))
}

// TestRenderRejectsOversizedOutput bounds the document every later stage
// carries. It does not make expansion DoS-proof: a template function still
// allocates its own result before writing, and `helm template` runs on
// pull-request-controlled charts with the same sprig helpers available.
func TestRenderRejectsOversizedOutput(t *testing.T) {
	_, err := render(`name: '{{ repeat 2000000 "x" }}'`, map[string]any{}, nil)
	require.ErrorIs(t, err, errRenderTooLarge)

	out, err := render(`name: '{{ repeat 16 "x" }}'`, map[string]any{}, nil)
	require.NoError(t, err)
	assert.Contains(t, out, strings.Repeat("x", 16))
}
