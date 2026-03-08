package utils

import (
	"context"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
)

// Verify interface compliance at compile time.
var _ ports.CredentialProvider = (*StaticCredentialProvider)(nil)

// StaticCredentialProvider resolves credentials from a pre-loaded list of
// repository credentials (typically sourced from REPO_CREDS_* environment variables).
// It implements ports.CredentialProvider and acts as the fallback provider in the chain.
type StaticCredentialProvider struct {
	credentials []models.RepoCredentials
}

// NewStaticCredentialProvider creates a StaticCredentialProvider from the given
// repository credential entries.
func NewStaticCredentialProvider(creds []models.RepoCredentials) *StaticCredentialProvider {
	return &StaticCredentialProvider{credentials: creds}
}

// Matches reports whether any stored credential entry has a URL matching registryURL.
func (p *StaticCredentialProvider) Matches(registryURL string) bool {
	for _, c := range p.credentials {
		if c.Url == registryURL {
			return true
		}
	}
	return false
}

// GetCredentials returns the username and password for the matching registry URL.
// If no match is found, empty credentials are returned without error.
func (p *StaticCredentialProvider) GetCredentials(_ context.Context, registryURL string) (ports.RegistryCredentials, error) {
	for _, c := range p.credentials {
		if c.Url == registryURL {
			return ports.RegistryCredentials{
				Username: c.Username,
				Password: c.Password,
			}, nil
		}
	}
	return ports.RegistryCredentials{}, nil
}
