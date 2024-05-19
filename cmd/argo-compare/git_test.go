package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/require"

	"github.com/spf13/afero"

	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/mocks"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

const (
	appFile = "testdata/test.yaml"
)

func init() {
	// We don't want to see any logs in tests
	loggingInit(logging.CRITICAL)
}

func TestCheckIfApp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create the mocks
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	isApp, err := checkIfApp(mockCmdRunner, utils.OsFileReader{}, appFile)

	assert.True(t, isApp, "expected true, got false")
	assert.NoError(t, err, "expected no error, got %v", err)
}

func TestNewGitRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockCmdRunner := mocks.NewMockCmdRunner(ctrl)

	fs := afero.NewMemMapFs()

	repo, err := NewGitRepo(fs, mockCmdRunner)

	assert.NoError(t, err)
	assert.NotNil(t, repo.Repo)
	assert.IsType(t, fs, repo.FsType)
	assert.IsType(t, mockCmdRunner, repo.CmdRunner)
}

func TestGetChangedFiles(t *testing.T) {
	// Create temporary directory for cloning
	tempDir, err := os.MkdirTemp("", "gitTest")
	assert.NoError(t, err)

	defer os.RemoveAll(tempDir) // clean up

	dir, err := os.Getwd()
	if err != nil {
		t.Errorf("Error getting current working directory: %v", err)
	}
	fmt.Println(dir)

	// Clone the bare repo to our temporary directory
	repo, err := git.PlainClone(tempDir, false, &git.CloneOptions{
		URL:          "../../testdata/repo.git",
		SingleBranch: false,
	})
	require.NoError(t, err, "Failed to clone repository")

	err = repo.Fetch(&git.FetchOptions{
		RefSpecs: []config.RefSpec{"refs/*:refs/*", "HEAD:refs/heads/HEAD"},
	})
	assert.NoError(t, err, "Failed to fetch")

	// Switch to the "feature" branch
	w, err := repo.Worktree()

	require.NoError(t, err, "Failed to get worktree")

	branchRef := plumbing.NewBranchReferenceName("feature-branch")

	err = w.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
	})
	require.NoError(t, err, "Failed to checkout feature branch")

	// Initialize GitRepo with cloned repo and proceed with testing
	target := GitRepo{
		Repo:      repo,
		FsType:    afero.NewOsFs(),
		CmdRunner: &utils.RealCmdRunner{},
	}

	targetBranch = "main"

	changedFiles, err := target.getChangedFiles(utils.OsFileReader{})
	assert.Equal(t, []string{"cluster-state/web/ingress-nginx.yaml"}, changedFiles)
	assert.NoError(t, err, "Failed to get changed files")
}
