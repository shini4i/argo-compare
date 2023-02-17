package main

import (
	"github.com/op/go-logging"
	"os"
	"testing"
)

func TestCollectRepoCredentials(t *testing.T) {
	envVars := map[string]string{
		"REPO_CREDS_1": `{"url":"https://example.com","username":"user1","password":"pass1"}`,
		"REPO_CREDS_2": `{"url":"https://example.org","username":"user2","password":"pass2"}`,
		"REPO_CREDS_3": `{"url":"https://example.net","username":"user3","password":"pass3"}`,
	}
	for key, value := range envVars {
		err := os.Setenv(key, value)
		if err != nil {
			t.Fatalf("failed to set environment variable %q: %v", key, err)
		}
	}

	collectRepoCredentials()

	expectedUrls := []string{"https://example.com", "https://example.org", "https://example.net"}
	for _, expectedUrl := range expectedUrls {
		found := false
		for _, repo := range repoCredentials {
			if repo.Url == expectedUrl {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected to find repo credentials for [%s], but none were found", expectedUrl)
		}
	}
}

func TestLoggingInit(t *testing.T) {
	// Run the function being tested
	loggingInit(logging.DEBUG)

	// Check the result
	if logging.GetLevel("") != logging.DEBUG {
		t.Errorf("logging level not set to DEBUG")
	}
}
