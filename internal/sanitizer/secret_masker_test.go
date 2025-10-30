package sanitizer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKubernetesSecretMasker_MaskRedactsSecretValues ensures secret manifests have their sensitive values masked.
func TestKubernetesSecretMasker_MaskRedactsSecretValues(t *testing.T) {
	masker := NewKubernetesSecretMasker()
	input := `apiVersion: v1
kind: Secret
metadata:
  name: sample
data:
  password: c2VjcmV0cGFzc3dvcmQ=
stringData:
  token: plain-token
`

	result, masked, err := masker.Mask([]byte(input))
	require.NoError(t, err)
	assert.True(t, masked)

	output := string(result)
	assert.NotContains(t, output, "c2VjcmV0cGFzc3dvcmQ=")
	assert.NotContains(t, output, "plain-token")
	assert.Contains(t, output, "ENC[sha256:")

	again, maskedAgain, err := masker.Mask([]byte(input))
	require.NoError(t, err)
	assert.True(t, maskedAgain)
	assert.Equal(t, string(result), string(again))
}

// TestKubernetesSecretMasker_MaskLeavesNonSecrets ensures non-secret manifests remain unchanged.
func TestKubernetesSecretMasker_MaskLeavesNonSecrets(t *testing.T) {
	masker := NewKubernetesSecretMasker()
	input := `apiVersion: v1
kind: ConfigMap
metadata:
  name: example
data:
  key: value
`

	result, masked, err := masker.Mask([]byte(input))
	require.NoError(t, err)
	assert.False(t, masked)
	assert.Equal(t, input, string(result))
}

// TestKubernetesSecretMasker_MaskHandlesMultipleDocuments ensures multi-document YAML is masked correctly.
func TestKubernetesSecretMasker_MaskHandlesMultipleDocuments(t *testing.T) {
	masker := NewKubernetesSecretMasker()
	input := `apiVersion: v1
kind: Secret
metadata:
  name: first
data:
  password: c2VjcmV0
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: config
data:
  key: value
---
apiVersion: v1
kind: Secret
metadata:
  name: second
stringData:
  api-key: another value
`

	result, masked, err := masker.Mask([]byte(input))
	require.NoError(t, err)
	assert.True(t, masked)

	output := string(result)
	assert.NotContains(t, output, "c2VjcmV0")
	assert.NotContains(t, output, "another value")
	assert.Equal(t, 2, strings.Count(output, "ENC[sha256:"))
}

// TestKubernetesSecretMasker_MaskDifferentiatesValues ensures masked placeholders differ for distinct inputs.
func TestKubernetesSecretMasker_MaskDifferentiatesValues(t *testing.T) {
	masker := NewKubernetesSecretMasker()

	first := `apiVersion: v1
kind: Secret
metadata:
  name: sample
data:
  password: c2VjcmV0
`
	second := `apiVersion: v1
kind: Secret
metadata:
  name: sample
data:
  password: ZGlmZmVyZW50
`

	resultOne, maskedOne, err := masker.Mask([]byte(first))
	require.NoError(t, err)
	assert.True(t, maskedOne)

	resultTwo, maskedTwo, err := masker.Mask([]byte(second))
	require.NoError(t, err)
	assert.True(t, maskedTwo)

	assert.NotEqual(t, string(resultOne), string(resultTwo))
	assert.Contains(t, string(resultOne), "ENC[sha256:")
	assert.Contains(t, string(resultTwo), "ENC[sha256:")
}

// TestKubernetesSecretMasker_HandlesNonMappingData ensures non-mapping secret data blocks are ignored safely.
func TestKubernetesSecretMasker_HandlesNonMappingData(t *testing.T) {
	masker := NewKubernetesSecretMasker()
	input := `apiVersion: v1
kind: Secret
metadata:
  name: sample
data: []
`

	result, masked, err := masker.Mask([]byte(input))
	require.NoError(t, err)
	assert.False(t, masked)
	assert.Equal(t, input, string(result))
}
