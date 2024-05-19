package main

import (
	"os"
	"testing"

	"github.com/shini4i/argo-compare/internal/models"

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

func TestGitInteraction(t *testing.T) {
	// Create temporary directory for cloning
	tempDir, err := os.MkdirTemp("", "gitTest")
	assert.NoError(t, err)

	defer os.RemoveAll(tempDir) // clean up

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

	t.Run("get changed files", func(t *testing.T) {
		changedFiles, err := target.getChangedFiles(utils.OsFileReader{})
		assert.Equal(t, []string{"cluster-state/web/ingress-nginx.yaml"}, changedFiles)
		assert.NoError(t, err, "Failed to get changed files")
	})

	t.Run("get changed file content", func(t *testing.T) {
		fileContent, err := target.getChangedFileContent("main", "cluster-state/web/ingress-nginx.yaml")
		expectedApp := models.Application{
			Kind: "Application",
			Metadata: struct {
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace"`
			}{
				Name:      "ingress-nginx",
				Namespace: "argo-cd",
			},
			Spec: struct {
				Source      *models.Source   `yaml:"source"`
				Sources     []*models.Source `yaml:"sources"`
				MultiSource bool             `yaml:"-"`
			}{
				Source: &models.Source{
					TargetRevision: "4.9.1",
				},
			},
		}
		assert.NoError(t, err)
		assert.Equal(t, expectedApp.Metadata.Name, fileContent.Metadata.Name)
		assert.Equal(t, expectedApp.Spec.Source.TargetRevision, fileContent.Spec.Source.TargetRevision)
	})
}
