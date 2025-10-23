package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

func TestGitRepoGetChangedFilesRespectsIgnore(t *testing.T) {
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "repo")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	repo, err := git.PlainInit(workDir, false)
	require.NoError(t, err)

	err = repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main")))
	require.NoError(t, err)

	writeApplication(t, workDir, `1.0.0`, 1)
	writeExtraApplication(t, workDir, "secondary", `1.0.0`, 1)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	_, err = worktree.Add("apps/demo.yaml")
	require.NoError(t, err)
	_, err = worktree.Add("apps/secondary.yaml")
	require.NoError(t, err)

	commitHash, err := worktree.Commit("initial", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	remotePath := filepath.Join(tempDir, "origin.git")
	_, err = git.PlainInit(remotePath, true)
	require.NoError(t, err)

	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remotePath}})
	require.NoError(t, err)

	err = repo.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{"refs/heads/main:refs/heads/main"}})
	require.NoError(t, err)

	err = repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), commitHash))
	require.NoError(t, err)

	err = worktree.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("feature"), Create: true})
	require.NoError(t, err)

	writeApplication(t, workDir, `1.1.0`, 2)
	writeExtraApplication(t, workDir, "secondary", `2.0.0`, 3)

	_, err = worktree.Add("apps/demo.yaml")
	require.NoError(t, err)
	_, err = worktree.Add("apps/secondary.yaml")
	require.NoError(t, err)

	_, err = worktree.Commit("update", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	originalWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(originalWD))
	})

	logger := logging.MustGetLogger("git-test")
	repoInstance, err := NewGitRepo(afero.NewOsFs(), noopCmdRunner{}, utils.OsFileReader{}, logger)
	require.NoError(t, err)

	result, err := repoInstance.GetChangedFiles("main", []string{"apps/secondary.yaml"})
	require.NoError(t, err)

	require.ElementsMatch(t, []string{"apps/demo.yaml"}, result.Applications)
	require.Empty(t, result.Invalid)
}

func TestGitRepoTreeForBranchReturnsTree(t *testing.T) {
	repoInstance, _ := buildGitRepo(t, true)

	tree, err := repoInstance.treeForBranch("main")
	require.NoError(t, err)
	require.NotNil(t, tree)
}

func TestGitRepoTreeForBranchMissingRemote(t *testing.T) {
	repoInstance, _ := buildGitRepo(t, false)

	_, err := repoInstance.treeForBranch("main")
	require.Error(t, err)
}

func TestGitRepoTargetFileContent(t *testing.T) {
	repoInstance, _ := buildGitRepo(t, true)

	tree, err := repoInstance.treeForBranch("main")
	require.NoError(t, err)

	content, err := repoInstance.targetFileContent(tree, "main", "apps/demo.yaml", false)
	require.NoError(t, err)
	require.Contains(t, content, "replicaCount")

	_, err = repoInstance.targetFileContent(tree, "main", "apps/missing.yaml", false)
	require.ErrorIs(t, err, gitFileDoesNotExist)

	content, err = repoInstance.targetFileContent(tree, "main", "apps/missing.yaml", true)
	require.NoError(t, err)
	require.Empty(t, content)
}

func TestGitRepoParseTargetApplication(t *testing.T) {
	repoInstance, _ := buildGitRepo(t, true)

	const appYAML = `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: parsed
  namespace: argocd
spec:
  destination:
    server: https://kubernetes.default.svc
    namespace: demo
  source:
    repoURL: fake.repo/charts
    chart: parsed-chart
    targetRevision: 1.0.0
    helm:
      releaseName: parsed
`

	application, err := repoInstance.parseTargetApplication(appYAML)
	require.NoError(t, err)
	require.Equal(t, "parsed", application.Metadata.Name)
	require.Equal(t, "parsed-chart", application.Spec.Source.Chart)
}

func writeExtraApplication(t *testing.T, repoDir, name, version string, replicas int) {
	t.Helper()
	content := []byte(`apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: ` + name + `
  namespace: argocd
spec:
  destination:
    server: https://kubernetes.default.svc
    namespace: demo
  source:
    repoURL: fake.repo/charts
    chart: ` + name + `-chart
    targetRevision: ` + version + `
    helm:
      releaseName: ` + name + `
      values: |
        replicaCount: ` + fmt.Sprintf("%d", replicas) + `
`)

	appPath := filepath.Join(repoDir, "apps")
	require.NoError(t, os.MkdirAll(appPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(appPath, name+".yaml"), content, 0o644))
}

func buildGitRepo(t *testing.T, includeRemote bool) (*GitRepo, *git.Repository) {
	t.Helper()

	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "repo")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	repo, err := git.PlainInit(workDir, false)
	require.NoError(t, err)

	err = repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main")))
	require.NoError(t, err)

	writeApplication(t, workDir, `1.0.0`, 1)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	_, err = worktree.Add("apps/demo.yaml")
	require.NoError(t, err)

	commitHash, err := worktree.Commit("initial", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	if includeRemote {
		err = repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), commitHash))
		require.NoError(t, err)
	}

	originalWD, err := os.Getwd()
	require.NoError(t, err)

	err = os.Chdir(workDir)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, os.Chdir(originalWD))
	})

	logger := logging.MustGetLogger(fmt.Sprintf("git-test-%s", t.Name()))
	repoInstance, err := NewGitRepo(afero.NewOsFs(), noopCmdRunner{}, utils.OsFileReader{}, logger)
	require.NoError(t, err)

	return repoInstance, repo
}
