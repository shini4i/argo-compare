package utils

import (
	"context"
	"testing"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaticCredentialProvider_Matches(t *testing.T) {
	creds := []models.RepoCredentials{
		{Url: "https://charts.example.com", Username: "user", Password: "pass"},
		{Url: "123456789012.dkr.ecr.us-east-1.amazonaws.com", Username: "AWS", Password: "token"},
	}
	provider := NewStaticCredentialProvider(creds)

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "exact match HTTP", url: "https://charts.example.com", want: true},
		{name: "exact match ECR", url: "123456789012.dkr.ecr.us-east-1.amazonaws.com", want: true},
		{name: "no match", url: "https://other.example.com", want: false},
		{name: "empty URL", url: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, provider.Matches(tt.url))
		})
	}
}

func TestStaticCredentialProvider_GetCredentials(t *testing.T) {
	creds := []models.RepoCredentials{
		{Url: "https://charts.example.com", Username: "user", Password: "pass"},
	}
	provider := NewStaticCredentialProvider(creds)

	t.Run("found", func(t *testing.T) {
		got, err := provider.GetCredentials(context.Background(), "https://charts.example.com")
		require.NoError(t, err)
		assert.Equal(t, ports.RegistryCredentials{Username: "user", Password: "pass"}, got)
	})

	t.Run("not found returns empty", func(t *testing.T) {
		got, err := provider.GetCredentials(context.Background(), "https://unknown.example.com")
		require.NoError(t, err)
		assert.Equal(t, ports.RegistryCredentials{}, got)
	})
}

func TestStaticCredentialProvider_EmptyList(t *testing.T) {
	provider := NewStaticCredentialProvider(nil)

	assert.False(t, provider.Matches("https://any.url"))

	got, err := provider.GetCredentials(context.Background(), "https://any.url")
	require.NoError(t, err)
	assert.Equal(t, ports.RegistryCredentials{}, got)
}
