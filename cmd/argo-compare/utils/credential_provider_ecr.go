package utils

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/op/go-logging"

	"github.com/shini4i/argo-compare/internal/ports"
)

// ecrURLRegex matches private AWS ECR registry URLs and captures the region.
// Format: <account-id>.dkr.ecr.<region>.amazonaws.com
// ECR Public (public.ecr.aws) is intentionally excluded as it does not require
// ecr:GetAuthorizationToken for anonymous pulls.
var ecrURLRegex = regexp.MustCompile(`^(\d+)\.dkr\.ecr\.([a-z0-9-]+)\.amazonaws\.com`)

// AuthorizationTokenGetter abstracts the subset of the AWS ECR client used by ECRCredentialProvider,
// enabling unit testing without real AWS calls.
type AuthorizationTokenGetter interface {
	GetAuthorizationToken(ctx context.Context, params *ecr.GetAuthorizationTokenInput, optFns ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error)
}

// awsConfigLoader is a function type that loads AWS SDK configuration.
// It is used as a field in ECRCredentialProvider so tests can inject a stub.
type awsConfigLoader func(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error)

// Verify interface compliance at compile time.
var _ ports.CredentialProvider = (*ECRCredentialProvider)(nil)

// cachedToken holds a cached ECR authorization token and its expiry.
type cachedToken struct {
	creds     ports.RegistryCredentials
	expiresAt time.Time
}

// ECRCredentialProvider resolves credentials for AWS ECR registries by calling
// ecr:GetAuthorizationToken. Tokens are cached until their expiry (up to 12 hours)
// to avoid redundant API calls when comparing multiple charts from the same registry.
type ECRCredentialProvider struct {
	log       *logging.Logger
	loadCfg   awsConfigLoader
	clientFor func(cfg aws.Config, region string) AuthorizationTokenGetter

	mu    sync.RWMutex
	cache map[string]cachedToken
}

// NewECRCredentialProvider creates an ECRCredentialProvider that uses the default
// AWS credential chain (env vars, IRSA, instance profiles, shared config).
func NewECRCredentialProvider(log *logging.Logger) *ECRCredentialProvider {
	return &ECRCredentialProvider{
		log:     log,
		loadCfg: config.LoadDefaultConfig,
		clientFor: func(cfg aws.Config, region string) AuthorizationTokenGetter {
			return ecr.NewFromConfig(cfg, func(o *ecr.Options) {
				o.Region = region
			})
		},
		cache: make(map[string]cachedToken),
	}
}

// Matches reports whether the given URL is an AWS ECR registry.
func (p *ECRCredentialProvider) Matches(registryURL string) bool {
	return ecrURLRegex.MatchString(registryURL)
}

// GetCredentials returns ECR authorization credentials for the given registry URL.
// If AWS credentials are unavailable (no env vars, no IRSA, no instance profile),
// it returns empty credentials with a debug log instead of an error. This allows
// helm pull to proceed without auth — succeeding for public ECR registries and
// producing a clear helm error for private ones.
// Only unexpected errors (e.g. network failures when credentials ARE configured) are propagated.
func (p *ECRCredentialProvider) GetCredentials(ctx context.Context, registryURL string) (ports.RegistryCredentials, error) {
	region := extractRegion(registryURL)
	if region == "" {
		return ports.RegistryCredentials{}, fmt.Errorf("failed to extract region from ECR URL %q", registryURL)
	}

	// Check cache first.
	if creds, ok := p.getCached(registryURL); ok {
		p.log.Debugf("Using cached ECR token for [%s]", registryURL)
		return creds, nil
	}

	// Load AWS config.
	cfg, err := p.loadCfg(ctx, config.WithRegion(region))
	if err != nil {
		p.log.Debugf("AWS credentials unavailable for ECR registry [%s]: %v; proceeding without auth", registryURL, err)
		return ports.RegistryCredentials{}, nil
	}

	client := p.clientFor(cfg, region)
	output, err := client.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		// If this is a credential-related error (no creds configured), gracefully degrade.
		if isCredentialError(err) {
			p.log.Debugf("AWS credentials unavailable for ECR registry [%s]: %v; proceeding without auth", registryURL, err)
			return ports.RegistryCredentials{}, nil
		}
		return ports.RegistryCredentials{}, fmt.Errorf("ECR GetAuthorizationToken failed for [%s]: %w", registryURL, err)
	}

	if len(output.AuthorizationData) == 0 {
		return ports.RegistryCredentials{}, fmt.Errorf("ECR returned no authorization data for [%s]", registryURL)
	}

	authData := output.AuthorizationData[0]
	username, password, err := decodeAuthToken(aws.ToString(authData.AuthorizationToken))
	if err != nil {
		return ports.RegistryCredentials{}, fmt.Errorf("failed to decode ECR auth token for [%s]: %w", registryURL, err)
	}

	creds := ports.RegistryCredentials{Username: username, Password: password}

	// Cache the token until its expiry.
	if authData.ExpiresAt != nil {
		p.setCached(registryURL, creds, *authData.ExpiresAt)
	}

	return creds, nil
}

// extractRegion parses the AWS region from an ECR registry URL.
func extractRegion(registryURL string) string {
	matches := ecrURLRegex.FindStringSubmatch(registryURL)
	if len(matches) < 3 {
		return ""
	}
	return matches[2]
}

// decodeAuthToken decodes a base64-encoded ECR authorization token (format: "AWS:<password>").
func decodeAuthToken(token string) (username, password string, err error) {
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", "", fmt.Errorf("base64 decode failed: %w", err)
	}

	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected token format: expected 'user:password'")
	}

	return parts[0], parts[1], nil
}

// isCredentialError returns true if the error indicates missing AWS credentials
// rather than a transient network issue.
func isCredentialError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "no EC2 IMDS role found") ||
		strings.Contains(msg, "failed to retrieve credentials") ||
		strings.Contains(msg, "NoCredentialProviders")
}

// getCached returns cached credentials if they exist and haven't expired.
// Expired entries are not eagerly deleted; they will be overwritten by setCached
// after a fresh token is fetched.
func (p *ECRCredentialProvider) getCached(registryURL string) (ports.RegistryCredentials, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	entry, ok := p.cache[registryURL]
	if !ok {
		return ports.RegistryCredentials{}, false
	}

	// Add a safety margin: consider expired 5 minutes before actual expiry.
	if time.Now().After(entry.expiresAt.Add(-5 * time.Minute)) {
		return ports.RegistryCredentials{}, false
	}

	return entry.creds, true
}

// setCached stores credentials in the cache with the given expiry time.
func (p *ECRCredentialProvider) setCached(registryURL string, creds ports.RegistryCredentials, expiresAt time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.cache[registryURL] = cachedToken{creds: creds, expiresAt: expiresAt}
}
