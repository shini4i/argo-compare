package gitlab

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPosterPost(t *testing.T) {
	var received struct {
		Method string
		Path   string
		Body   map[string]string
		Token  string
	}

	client := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			received.Method = req.Method
			received.Path = req.URL.Path
			received.Token = req.Header.Get(privateTokenHeader)

			data, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &received.Body))

			resp := httptest.NewRecorder()
			resp.WriteHeader(http.StatusCreated)
			return resp.Result(), nil
		}),
	}

	cfg := Config{
		BaseURL:         "http://gitlab.example",
		Token:           "token",
		ProjectID:       "group/project",
		MergeRequestIID: 7,
		HTTPClient:      client,
	}
	poster, err := NewPoster(cfg)
	require.NoError(t, err)

	err = poster.Post("hello world")
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, received.Method)
	assert.Equal(t, "token", received.Token)
	assert.Equal(t, "hello world", received.Body["body"])

	baseURL, _ := url.Parse(cfg.BaseURL)
	expectedPath := baseURL.Path + "/api/v4/projects/group%2Fproject/merge_requests/7/notes"
	assert.Equal(t, expectedPath, received.Path)
}

func TestPosterPostErrorStatus(t *testing.T) {
	client := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			resp := httptest.NewRecorder()
			http.Error(resp, "boom", http.StatusBadRequest)
			return resp.Result(), nil
		}),
	}

	poster, err := NewPoster(Config{
		BaseURL:         "https://gitlab.example",
		Token:           "token",
		ProjectID:       "1",
		MergeRequestIID: 2,
		HTTPClient:      client,
	})
	require.NoError(t, err)

	err = poster.Post("body")
	require.Error(t, err)
}

func TestNewPosterValidatesConfig(t *testing.T) {
	_, err := NewPoster(Config{})
	require.Error(t, err)

	_, err = NewPoster(Config{BaseURL: "http://example.com"})
	require.Error(t, err)

	_, err = NewPoster(Config{BaseURL: "http://example.com", Token: "token"})
	require.Error(t, err)

	_, err = NewPoster(Config{BaseURL: "http://example.com", Token: "token", ProjectID: "1"})
	require.Error(t, err)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
