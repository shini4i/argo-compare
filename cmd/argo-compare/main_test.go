package main

import (
	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func TestCollectRepoCredentials(t *testing.T) {
	// Test case 1: repo credentials are set and valid
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

	if err := collectRepoCredentials(); err != nil {
		t.Fatalf("failed to collect repo credentials: %v", err)
	}

	expectedUrls := []string{"https://example.com", "https://example.org", "https://example.net"}
	for _, expectedUrl := range expectedUrls {
		found := false
		for _, repo := range repoCredentials {
			if repo.Url == expectedUrl {
				found = true
				break
			}
		}
		assert.True(t, found, "expected to find repo credentials for [%s], but none were found", expectedUrl)
	}

	// Test case 2: repo credentials are set but invalid
	envVars = map[string]string{
		"REPO_CREDS_1": `{"url":"https://example.com","username":"user1","password":"pass1"}`,
		"REPO_CREDS_2": `{"url":"https://example.org","username":"user2","password":"pass2"}`,
		"REPO_CREDS_3": `{"url":"https://example.net","username":"user3","password":"pass3"`,
	}

	for key, value := range envVars {
		err := os.Setenv(key, value)
		if err != nil {
			t.Fatalf("failed to set environment variable %q: %v", key, err)
		}
	}

	assert.Error(t, collectRepoCredentials(), "expected to get an error when repo credentials are invalid")
}

func TestLoggingInit(t *testing.T) {
	// Run the function being tested
	loggingInit(logging.DEBUG)

	// Check the result
	if logging.GetLevel("") != logging.DEBUG {
		t.Errorf("logging level not set to DEBUG")
	}
}

func TestInvalidFilesList(t *testing.T) {
	// Test case 1: invalid files list is not empty
	repo := GitRepo{CmdRunner: &utils.RealCmdRunner{}}
	repo.invalidFiles = []string{"file1", "file2", "file3"}

	err := printInvalidFilesList(&repo)
	// We need to return the error if any of the files is invalid
	assert.Error(t, err)

	// Test case 2: invalid files list is empty
	repo.invalidFiles = []string{}

	err = printInvalidFilesList(&repo)
	// If the list is empty, we should not return an error
	assert.NoError(t, err)
}
