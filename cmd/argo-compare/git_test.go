package main

import (
	"testing"

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
