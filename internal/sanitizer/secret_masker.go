// Package sanitizer provides functionality for masking sensitive data in Kubernetes
// manifests, particularly Secret resources, to prevent exposure in diff outputs.
package sanitizer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/shini4i/argo-compare/internal/ports"
	"gopkg.in/yaml.v3"
)

const (
	maskPrefix      = "ENC[sha256:"
	maskSuffix      = "]"
	hashPrefixBytes = 16
)

// KubernetesSecretMasker redacts sensitive values contained within Kubernetes Secret manifests.
type KubernetesSecretMasker struct {
	mu        sync.RWMutex
	hashCache map[string]string // keyed by the full SHA-256 digest to avoid retaining plaintext secrets.
}

// Ensure compile-time conformance to the SensitiveDataMasker contract.
var _ ports.SensitiveDataMasker = (*KubernetesSecretMasker)(nil)

// NewKubernetesSecretMasker constructs a masker capable of redacting Kubernetes Secret data values.
func NewKubernetesSecretMasker() *KubernetesSecretMasker {
	return &KubernetesSecretMasker{
		hashCache: make(map[string]string),
	}
}

// Mask redacts data and stringData values of Kubernetes Secret manifests while preserving other resources untouched.
// It returns the potentially modified manifest bytes alongside a flag indicating whether masking occurred.
func (m *KubernetesSecretMasker) Mask(content []byte) ([]byte, bool, error) {
	if len(content) == 0 {
		return content, false, nil
	}

	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var documents []*yaml.Node
	var masked bool

	for {
		var document yaml.Node
		if err := decoder.Decode(&document); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, false, fmt.Errorf("decode manifest: %w", err)
		}

		if sanitizeSecretDocument(&document, m.buildMaskedValue) {
			masked = true
		}

		documents = append(documents, &document)
	}

	if !masked {
		return content, false, nil
	}

	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)

	for _, document := range documents {
		if err := encoder.Encode(document); err != nil {
			return nil, false, fmt.Errorf("encode manifest: %w", err)
		}
	}

	if err := encoder.Close(); err != nil {
		return nil, false, fmt.Errorf("close encoder: %w", err)
	}

	return buffer.Bytes(), true, nil
}

// sanitizeSecretDocument traverses a YAML document and redacts Kubernetes Secret values in-place using the supplied masker.
func sanitizeSecretDocument(document *yaml.Node, maskValue func(string) string) bool {
	if document == nil || document.Kind != yaml.DocumentNode || len(document.Content) == 0 {
		return false
	}

	root := document.Content[0]
	if root == nil || root.Kind != yaml.MappingNode {
		return false
	}

	kindNode := findMappingValue(root, "kind")
	if kindNode == nil || !strings.EqualFold(kindNode.Value, "Secret") {
		return false
	}

	maskedData := maskSecretMap(root, "data", maskValue)
	maskedStringData := maskSecretMap(root, "stringData", maskValue)

	return maskedData || maskedStringData
}

// maskSecretMap locates the provided key on the mapping node and redacts all scalar values within it using the provided masking function.
func maskSecretMap(parent *yaml.Node, key string, maskValue func(string) string) bool {
	keyIndex := findMappingKeyIndex(parent, key)
	if keyIndex < 0 {
		return false
	}

	valueNode := parent.Content[keyIndex+1]
	if valueNode == nil || valueNode.Kind != yaml.MappingNode {
		return false
	}

	var masked bool
	for i := 0; i < len(valueNode.Content); i += 2 {
		if i+1 >= len(valueNode.Content) {
			continue
		}
		value := valueNode.Content[i+1]
		if value == nil || value.Kind != yaml.ScalarNode {
			continue
		}

		value.Value = maskValue(value.Value)
		value.Tag = "!!str"
		value.Style = yaml.Style(0) // yaml.Style(0) keeps the scalar in plain style; yaml.v3 does not expose a named constant.
		masked = true
	}

	return masked
}

// findMappingValue retrieves the value node for the supplied key within a mapping node.
func findMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	index := findMappingKeyIndex(mapping, key)
	if index < 0 || index+1 >= len(mapping.Content) {
		return nil
	}
	return mapping.Content[index+1]
}

// findMappingKeyIndex returns the index of the key node within the mapping node content slice.
func findMappingKeyIndex(mapping *yaml.Node, key string) int {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return -1
	}

	for i := 0; i < len(mapping.Content); i += 2 {
		currentKey := mapping.Content[i]
		if currentKey == nil || currentKey.Kind != yaml.ScalarNode {
			continue
		}
		if strings.EqualFold(currentKey.Value, key) {
			return i
		}
	}

	return -1
}

// buildMaskedValue returns a deterministic redacted placeholder for the provided secret value while reusing cached computations.
func (m *KubernetesSecretMasker) buildMaskedValue(value string) string {
	digest := sha256.Sum256([]byte(value))
	digestKey := hex.EncodeToString(digest[:])

	m.mu.RLock()
	masked, ok := m.hashCache[digestKey]
	m.mu.RUnlock()
	if ok {
		return masked
	}

	prefix := hex.EncodeToString(digest[:hashPrefixBytes])
	masked = maskPrefix + prefix + maskSuffix

	m.mu.Lock()
	if cached, exists := m.hashCache[digestKey]; exists {
		m.mu.Unlock()
		return cached
	}
	m.hashCache[digestKey] = masked
	m.mu.Unlock()

	return masked
}
