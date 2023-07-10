package main

import (
	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
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

func TestMainGetChangedFiles(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Test case 1: there are changed files, and specific file was not provided
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)
	mockCmdRunner.EXPECT().Run("git", "--no-pager", "diff", "--name-only", gomock.Any()).Return("testdata/test.yaml\nfile2", "", nil)

	repo := GitRepo{CmdRunner: mockCmdRunner}

	changedFiles, err := getChangedFiles(utils.OsFileReader{}, &repo, "")

	assert.NoError(t, err)
	assert.Equal(t, []string{"testdata/test.yaml"}, changedFiles)

	// Test case 2: an unexpected error occurred
	mockCmdRunner.EXPECT().Run("git", "--no-pager", "diff", "--name-only", gomock.Any()).Return("", "", os.ErrPermission)

	repo = GitRepo{CmdRunner: mockCmdRunner}
	_, err = getChangedFiles(utils.OsFileReader{}, &repo, "")
	assert.ErrorIsf(t, err, os.ErrPermission, "expected to get an error when running git diff")

	// Test case 3: a specific file was provided
	changedFiles, err = getChangedFiles(utils.OsFileReader{}, &repo, "file1")
	assert.NoError(t, err)
	assert.Equal(t, []string{"file1"}, changedFiles)
}

func TestCliCommands(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	testCases := []struct {
		name             string
		args             []string
		expectedBranch   string
		expectedAdded    bool
		expectedRemoved  bool
		expectedPreserve bool
	}{
		{"minimal input", []string{"cmd", "branch", "main"}, "main", false, false, false},
		{"full output", []string{"cmd", "branch", "main", "--full-output"}, "main", true, true, false},
		{"print added", []string{"cmd", "branch", "main", "--print-added-manifests"}, "main", true, false, false},
		{"print removed", []string{"cmd", "branch", "main", "--print-removed-manifests"}, "main", false, true, false},
		{"preserve helm labels", []string{"cmd", "branch", "main", "--preserve-helm-labels"}, "main", false, false, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = tc.args
			err := parseCli()
			assert.NoErrorf(t, err, "expected to get no error when parsing valid command")
			assert.Equal(t, tc.expectedBranch, targetBranch)

			updateConfigurations()

			assert.Equal(t, tc.expectedAdded, printAddedManifests)
			assert.Equal(t, tc.expectedRemoved, printRemovedManifests)
			assert.Equal(t, tc.expectedPreserve, preserveHelmLabels)

			// Reset global vars
			targetBranch = ""
			printAddedManifests = false
			printRemovedManifests = false
			preserveHelmLabels = false
		})
	}
}
