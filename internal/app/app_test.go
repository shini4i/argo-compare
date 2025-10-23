package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterIgnored(t *testing.T) {
	files := []string{"a.yaml", "b.yaml", "c.yaml"}
	ignored := []string{"b.yaml"}

	result := filterIgnored(files, ignored)

	assert.Equal(t, []string{"a.yaml", "c.yaml"}, result)
}
