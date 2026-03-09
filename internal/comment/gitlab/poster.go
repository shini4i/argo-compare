// Package gitlab implements the comment.Poster interface for posting
// diff comments to GitLab merge requests via the GitLab API.
package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/shini4i/argo-compare/internal/comment"
	"github.com/shini4i/argo-compare/internal/helpers"
)

const (
	defaultAPIPrefix      = "/api/v4"
	privateTokenHeader    = "PRIVATE-TOKEN"
	maxErrorResponseBytes = 4096
	defaultClientTimeout  = 15 * time.Second
)

// Config describes settings required to post comments to a GitLab Merge Request.
type Config struct {
	BaseURL         string
	Token           string
	ProjectID       string
	MergeRequestIID int
	HTTPClient      *http.Client
	APIPrefix       string
	Timeout         time.Duration
}

type Poster struct {
	client          *http.Client
	baseURL         *url.URL
	projectID       string
	mergeRequestIID int
	token           string
	apiPrefix       string
}

// Ensure Poster implements comment.Poster.
var _ comment.Poster = (*Poster)(nil)

// NewPoster creates a Poster configured to post comments to a GitLab merge request using cfg.
// It validates that cfg.BaseURL, cfg.Token, cfg.ProjectID and cfg.MergeRequestIID are provided and parses cfg.BaseURL;
// returns an error if validation or URL parsing fails. If cfg.HTTPClient is nil, a default http.Client is created using
// cfg.Timeout (or the package default). The Poster will use cfg.APIPrefix if set, otherwise the package default.
func NewPoster(cfg Config) (*Poster, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("gitlab: base URL is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("gitlab: token is required")
	}
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("gitlab: project ID is required")
	}
	if cfg.MergeRequestIID == 0 {
		return nil, fmt.Errorf("gitlab: merge request IID is required")
	}

	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("gitlab: parse base URL: %w", err)
	}

	client := cfg.HTTPClient
	if client == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = defaultClientTimeout
		}
		client = &http.Client{Timeout: timeout}
	}

	apiPrefix := cfg.APIPrefix
	if apiPrefix == "" {
		apiPrefix = defaultAPIPrefix
	}

	return &Poster{
		client:          client,
		baseURL:         base,
		projectID:       cfg.ProjectID,
		mergeRequestIID: cfg.MergeRequestIID,
		token:           cfg.Token,
		apiPrefix:       apiPrefix,
	}, nil
}

// Post sends the supplied comment body to the configured Merge Request note endpoint.
// The context can be used to cancel the request or set timeouts.
// Network errors and 5xx server errors are retried with exponential backoff.
func (p *Poster) Post(ctx context.Context, body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("gitlab: comment body is empty")
	}

	endpoint := *p.baseURL
	endpoint.Path = path.Join(endpoint.Path, p.apiPrefix, "projects", url.PathEscape(p.projectID), "merge_requests", fmt.Sprintf("%d", p.mergeRequestIID), "notes")

	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return fmt.Errorf("gitlab: marshal payload: %w", err)
	}

	retryCfg := helpers.DefaultRetryConfig()
	return helpers.WithRetry(ctx, retryCfg, func() error {
		return p.doRequest(ctx, endpoint.String(), payload)
	})
}

// doRequest performs the HTTP request and returns an error only for retryable failures.
// Client errors (4xx) are wrapped in a permanentError to prevent retry.
func (p *Poster) doRequest(ctx context.Context, endpoint string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("gitlab: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(privateTokenHeader, p.token)

	resp, err := p.client.Do(req) // #nosec G107 -- endpoint is built from operator-configured BaseURL, not user input
	if err != nil {
		// Network errors are retryable
		return fmt.Errorf("gitlab: perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		// Success - drain response body for connection reuse
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorResponseBytes))
	apiErr := fmt.Errorf("gitlab: unexpected status %s: %s", resp.Status, strings.TrimSpace(string(respBody)))

	// 3xx: Redirect responses that reach here (after http.Client's automatic handling)
	// indicate a configuration issue and should not be retried
	if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		return helpers.WrapPermanent(apiErr)
	}

	// 4xx: Client errors are permanent (bad request, auth issues, not found, etc.)
	if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError {
		return helpers.WrapPermanent(apiErr)
	}

	// 5xx: Server errors are transient and should be retried
	return apiErr
}
