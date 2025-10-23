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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Method = r.Method
		received.Path = r.URL.Path
		received.Token = r.Header.Get(privateTokenHeader)

		defer r.Body.Close()
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &received.Body))

		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(server.Close)

	cfg := Config{
		BaseURL:         server.URL,
		Token:           "token",
		ProjectID:       "group/project",
		MergeRequestIID: 7,
		HTTPClient:      server.Client(),
	}
	poster, err := NewPoster(cfg)
	require.NoError(t, err)

	err = poster.Post("hello world")
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, received.Method)
	assert.Equal(t, "token", received.Token)
	assert.Equal(t, "hello world", received.Body["body"])

	baseURL, _ := url.Parse(server.URL)
	expectedPath := baseURL.Path + "/api/v4/projects/group%2Fproject/merge_requests/7/notes"
	assert.Equal(t, expectedPath, received.Path)
}

func TestPosterPostErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)

	poster, err := NewPoster(Config{
		BaseURL:         server.URL,
		Token:           "token",
		ProjectID:       "1",
		MergeRequestIID: 2,
		HTTPClient:      server.Client(),
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
