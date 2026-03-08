package utils

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockECRClient implements the AuthorizationTokenGetter interface for testing.
type mockECRClient struct {
	output *ecr.GetAuthorizationTokenOutput
	err    error
	calls  int
}

func (m *mockECRClient) GetAuthorizationToken(_ context.Context, _ *ecr.GetAuthorizationTokenInput, _ ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error) {
	m.calls++
	return m.output, m.err
}

func newTestECRProvider(log *logging.Logger, client *mockECRClient) *ECRCredentialProvider {
	return &ECRCredentialProvider{
		log: log,
		loadCfg: func(_ context.Context, _ ...func(*config.LoadOptions) error) (aws.Config, error) {
			return aws.Config{}, nil
		},
		clientFor: func(_ aws.Config, _ string) AuthorizationTokenGetter {
			return client
		},
		cache: make(map[string]cachedToken),
	}
}

func TestECRCredentialProvider_Matches(t *testing.T) {
	log := logging.MustGetLogger("test-ecr")
	provider := NewECRCredentialProvider(log)

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "standard ECR URL", url: "123456789012.dkr.ecr.us-east-1.amazonaws.com", want: true},
		{name: "ECR URL eu-west-1", url: "999888777666.dkr.ecr.eu-west-1.amazonaws.com", want: true},
		{name: "ECR URL ap-southeast-2", url: "111222333444.dkr.ecr.ap-southeast-2.amazonaws.com", want: true},
		{name: "ECR URL with path", url: "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-repo", want: true},
		{name: "HTTP repo", url: "https://charts.example.com", want: false},
		{name: "GHCR", url: "ghcr.io/my-org", want: false},
		{name: "empty", url: "", want: false},
		{name: "partial ECR", url: "dkr.ecr.us-east-1.amazonaws.com", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, provider.Matches(tt.url))
		})
	}
}

func TestECRCredentialProvider_GetCredentials_Success(t *testing.T) {
	log := logging.MustGetLogger("test-ecr")
	token := base64.StdEncoding.EncodeToString([]byte("AWS:my-secret-token"))
	expiresAt := time.Now().Add(12 * time.Hour)

	client := &mockECRClient{
		output: &ecr.GetAuthorizationTokenOutput{
			AuthorizationData: []types.AuthorizationData{
				{
					AuthorizationToken: aws.String(token),
					ExpiresAt:          &expiresAt,
				},
			},
		},
	}

	provider := newTestECRProvider(log, client)
	creds, err := provider.GetCredentials(context.Background(), "123456789012.dkr.ecr.us-east-1.amazonaws.com")

	require.NoError(t, err)
	assert.Equal(t, "AWS", creds.Username)
	assert.Equal(t, "my-secret-token", creds.Password)
	assert.Equal(t, 1, client.calls)
}

func TestECRCredentialProvider_GetCredentials_CachedToken(t *testing.T) {
	log := logging.MustGetLogger("test-ecr")
	token := base64.StdEncoding.EncodeToString([]byte("AWS:cached-token"))
	expiresAt := time.Now().Add(12 * time.Hour)

	client := &mockECRClient{
		output: &ecr.GetAuthorizationTokenOutput{
			AuthorizationData: []types.AuthorizationData{
				{
					AuthorizationToken: aws.String(token),
					ExpiresAt:          &expiresAt,
				},
			},
		},
	}

	provider := newTestECRProvider(log, client)
	registryURL := "123456789012.dkr.ecr.us-east-1.amazonaws.com"

	// First call fetches from API.
	creds1, err := provider.GetCredentials(context.Background(), registryURL)
	require.NoError(t, err)
	assert.Equal(t, "cached-token", creds1.Password)
	assert.Equal(t, 1, client.calls)

	// Second call uses cache — no additional API call.
	creds2, err := provider.GetCredentials(context.Background(), registryURL)
	require.NoError(t, err)
	assert.Equal(t, creds1, creds2)
	assert.Equal(t, 1, client.calls, "expected no additional API call due to caching")
}

func TestECRCredentialProvider_GetCredentials_AWSCredsUnavailable(t *testing.T) {
	log := logging.MustGetLogger("test-ecr")

	provider := &ECRCredentialProvider{
		log: log,
		loadCfg: func(_ context.Context, _ ...func(*config.LoadOptions) error) (aws.Config, error) {
			return aws.Config{}, errors.New("failed to retrieve credentials")
		},
		clientFor: func(_ aws.Config, _ string) AuthorizationTokenGetter {
			return &mockECRClient{}
		},
		cache: make(map[string]cachedToken),
	}

	creds, err := provider.GetCredentials(context.Background(), "123456789012.dkr.ecr.us-east-1.amazonaws.com")
	require.NoError(t, err, "should not error when AWS credentials are unavailable")
	assert.Equal(t, ports.RegistryCredentials{}, creds, "should return empty credentials")
}

func TestECRCredentialProvider_GetCredentials_APICredentialError(t *testing.T) {
	log := logging.MustGetLogger("test-ecr")

	client := &mockECRClient{
		err: errors.New("NoCredentialProviders: no valid providers in chain"),
	}

	provider := newTestECRProvider(log, client)
	creds, err := provider.GetCredentials(context.Background(), "123456789012.dkr.ecr.us-east-1.amazonaws.com")

	require.NoError(t, err, "credential errors should return empty creds, not error")
	assert.Equal(t, ports.RegistryCredentials{}, creds)
}

func TestECRCredentialProvider_GetCredentials_NetworkError(t *testing.T) {
	log := logging.MustGetLogger("test-ecr")

	client := &mockECRClient{
		err: errors.New("dial tcp: lookup ecr.us-east-1.amazonaws.com: no such host"),
	}

	provider := newTestECRProvider(log, client)
	_, err := provider.GetCredentials(context.Background(), "123456789012.dkr.ecr.us-east-1.amazonaws.com")

	require.Error(t, err, "network errors should be propagated")
	assert.Contains(t, err.Error(), "ECR GetAuthorizationToken failed")
}

func TestExtractRegion(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		region string
	}{
		{name: "us-east-1", url: "123456789012.dkr.ecr.us-east-1.amazonaws.com", region: "us-east-1"},
		{name: "eu-west-1", url: "999888777666.dkr.ecr.eu-west-1.amazonaws.com", region: "eu-west-1"},
		{name: "ap-southeast-2", url: "111222333444.dkr.ecr.ap-southeast-2.amazonaws.com", region: "ap-southeast-2"},
		{name: "with path", url: "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-chart", region: "us-west-2"},
		{name: "invalid URL", url: "https://charts.example.com", region: ""},
		{name: "empty", url: "", region: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.region, extractRegion(tt.url))
		})
	}
}

func TestDecodeAuthToken(t *testing.T) {
	t.Run("valid token", func(t *testing.T) {
		token := base64.StdEncoding.EncodeToString([]byte("AWS:my-password"))
		user, pass, err := decodeAuthToken(token)
		require.NoError(t, err)
		assert.Equal(t, "AWS", user)
		assert.Equal(t, "my-password", pass)
	})

	t.Run("invalid base64", func(t *testing.T) {
		_, _, err := decodeAuthToken("not-valid-base64!!!")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "base64 decode failed")
	})

	t.Run("wrong format", func(t *testing.T) {
		token := base64.StdEncoding.EncodeToString([]byte("no-colon-here"))
		_, _, err := decodeAuthToken(token)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected token format")
	})
}

func TestECRCredentialProvider_GetCredentials_InvalidURL(t *testing.T) {
	log := logging.MustGetLogger("test-ecr")
	provider := NewECRCredentialProvider(log)

	_, err := provider.GetCredentials(context.Background(), "not-an-ecr-url")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to extract region")
}

func TestECRCredentialProvider_GetCredentials_EmptyAuthData(t *testing.T) {
	log := logging.MustGetLogger("test-ecr")

	client := &mockECRClient{
		output: &ecr.GetAuthorizationTokenOutput{
			AuthorizationData: []types.AuthorizationData{},
		},
	}

	provider := newTestECRProvider(log, client)
	_, err := provider.GetCredentials(context.Background(), "123456789012.dkr.ecr.us-east-1.amazonaws.com")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authorization data")
}

func TestECRCredentialProvider_GetCredentials_ExpiredCacheTriggersRefresh(t *testing.T) {
	log := logging.MustGetLogger("test-ecr")
	token := base64.StdEncoding.EncodeToString([]byte("AWS:fresh-token"))
	freshExpiry := time.Now().Add(12 * time.Hour)

	client := &mockECRClient{
		output: &ecr.GetAuthorizationTokenOutput{
			AuthorizationData: []types.AuthorizationData{
				{
					AuthorizationToken: aws.String(token),
					ExpiresAt:          &freshExpiry,
				},
			},
		},
	}

	provider := newTestECRProvider(log, client)
	registryURL := "123456789012.dkr.ecr.us-east-1.amazonaws.com"

	// Manually insert an expired cache entry (within the 5-minute safety margin).
	provider.mu.Lock()
	provider.cache[registryURL] = cachedToken{
		creds:     ports.RegistryCredentials{Username: "AWS", Password: "stale-token"},
		expiresAt: time.Now().Add(3 * time.Minute), // Within 5-min margin, so considered expired.
	}
	provider.mu.Unlock()

	// Should bypass cache and call the API.
	creds, err := provider.GetCredentials(context.Background(), registryURL)
	require.NoError(t, err)
	assert.Equal(t, "fresh-token", creds.Password)
	assert.Equal(t, 1, client.calls, "expected API call because cached token is expired")
}

func TestIsCredentialError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "no EC2 IMDS role", err: errors.New("no EC2 IMDS role found"), want: true},
		{name: "failed to retrieve credentials", err: errors.New("failed to retrieve credentials"), want: true},
		{name: "NoCredentialProviders", err: errors.New("NoCredentialProviders: no valid providers in chain"), want: true},
		{name: "network error", err: errors.New("dial tcp: connection refused"), want: false},
		{name: "generic error", err: errors.New("something went wrong"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isCredentialError(tt.err))
		})
	}
}
