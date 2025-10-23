package gitlab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/shini4i/argo-compare/internal/comment"
)

const (
	defaultAPIPrefix   = "/api/v4"
	privateTokenHeader = "PRIVATE-TOKEN"
)

// Config describes settings required to post comments to a GitLab Merge Request.
type Config struct {
	BaseURL         string
	Token           string
	ProjectID       string
	MergeRequestIID int
	HTTPClient      *http.Client
	APIPrefix       string
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

// NewPoster builds a GitLab Merge Request comment poster.
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
		client = &http.Client{Timeout: 15 * time.Second}
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
func (p *Poster) Post(body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("gitlab: comment body is empty")
	}

	endpoint := *p.baseURL
	endpoint.Path = path.Join(endpoint.Path, p.apiPrefix, "projects", url.PathEscape(p.projectID), "merge_requests", fmt.Sprintf("%d", p.mergeRequestIID), "notes")

	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return fmt.Errorf("gitlab: marshal payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("gitlab: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(privateTokenHeader, p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("gitlab: perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gitlab: unexpected status %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	// Drain response body for connection reuse; errors are intentionally ignored.
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
