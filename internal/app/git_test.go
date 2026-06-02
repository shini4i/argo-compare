package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils"
	"github.com/shini4i/argo-compare/internal/ports/portstest"
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

	log := logger.New("git-test")
	repoInstance, err := NewGitRepo(afero.NewOsFs(), portstest.NoopCmdRunner{}, utils.OsFileReader{}, log)
	require.NoError(t, err)

	result, err := repoInstance.GetChangedFiles("main", []string{"apps/secondary.yaml"}, "")
	require.NoError(t, err)

	require.ElementsMatch(t, []string{"apps/demo.yaml"}, result.Applications)
	require.Empty(t, result.Invalid)
}

// TestGitRepoGetChangedFilesExcludesDstOnlyChanges verifies that files modified
// only on the destination branch (after src branched off) are NOT reported as
// changed. We want "what src changed since branching off", not the symmetric
// difference between src and dst tips.
func TestGitRepoGetChangedFilesExcludesDstOnlyChanges(t *testing.T) {
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "repo")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	repo, err := git.PlainInit(workDir, false)
	require.NoError(t, err)

	err = repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main")))
	require.NoError(t, err)

	// Base commit on main: both apps at v1.0.0 — this is the merge base.
	writeApplication(t, workDir, `1.0.0`, 1)
	writeExtraApplication(t, workDir, "secondary", `1.0.0`, 1)

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	_, err = worktree.Add("apps/demo.yaml")
	require.NoError(t, err)
	_, err = worktree.Add("apps/secondary.yaml")
	require.NoError(t, err)

	_, err = worktree.Commit("initial", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	// Branch off "feature" from the base commit and modify ONLY demo.yaml.
	err = worktree.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("feature"), Create: true})
	require.NoError(t, err)

	writeApplication(t, workDir, `1.1.0`, 2)
	_, err = worktree.Add("apps/demo.yaml")
	require.NoError(t, err)
	_, err = worktree.Commit("src changes demo", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	// Switch back to main and modify ONLY secondary.yaml (dst-only change).
	err = worktree.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("main")})
	require.NoError(t, err)

	writeExtraApplication(t, workDir, "secondary", `2.0.0`, 3)
	_, err = worktree.Add("apps/secondary.yaml")
	require.NoError(t, err)
	mainHash, err := worktree.Commit("dst changes secondary", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	// origin/main points at the new main tip — which has the dst-only change.
	err = repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), mainHash))
	require.NoError(t, err)

	// Switch back to feature so HEAD reflects src.
	err = worktree.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("feature")})
	require.NoError(t, err)

	originalWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(originalWD))
	})

	log := logger.New("git-test-dst-only")
	repoInstance, err := NewGitRepo(afero.NewOsFs(), portstest.NoopCmdRunner{}, utils.OsFileReader{}, log)
	require.NoError(t, err)

	result, err := repoInstance.GetChangedFiles("main", nil, "")
	require.NoError(t, err)

	// Only the file src actually touched should be reported.
	require.ElementsMatch(t, []string{"apps/demo.yaml"}, result.Applications)
	require.Empty(t, result.Invalid)
}

// TestGitRepoGetChangedFilesUnrelatedHistories verifies that an unrelated
// target branch (no shared history with HEAD) produces ErrNoCommonAncestor
// rather than silently treating every file as changed.
func TestGitRepoGetChangedFilesUnrelatedHistories(t *testing.T) {
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
	_, err = worktree.Commit("initial", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	// Construct an orphan commit (no parents, empty tree) and point
	// origin/main at it — HEAD and origin/main now share no history.
	treeHash := storeEmptyTree(t, repo)
	orphanHash := storeRawCommit(t, repo, treeHash, nil, "orphan")

	err = repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), orphanHash))
	require.NoError(t, err)

	originalWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(originalWD))
	})

	log := logger.New("git-test-unrelated")
	repoInstance, err := NewGitRepo(afero.NewOsFs(), portstest.NoopCmdRunner{}, utils.OsFileReader{}, log)
	require.NoError(t, err)

	_, err = repoInstance.GetChangedFiles("main", nil, "")
	require.ErrorIs(t, err, ErrNoCommonAncestor)
}

// TestGitRepoGetChangedFilesAmbiguousMergeBase verifies that a criss-cross
// merge topology — where HEAD and the target branch have two equally-valid
// best common ancestors — returns ErrAmbiguousMergeBase rather than silently
// picking one.
//
// The topology built here:
//
//	  A---C   (HEAD, "feature": merges B into A's line)
//	 / \ /
//	O   X
//	 \ / \
//	  B---D  (origin/main: merges A into B's line)
//
// Merge bases of C and D are {A, B} — neither is reachable from the other.
func TestGitRepoGetChangedFilesAmbiguousMergeBase(t *testing.T) {
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "repo")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	repo, err := git.PlainInit(workDir, false)
	require.NoError(t, err)

	treeHash := storeEmptyTree(t, repo)
	oHash := storeRawCommit(t, repo, treeHash, nil, "O")
	aHash := storeRawCommit(t, repo, treeHash, []plumbing.Hash{oHash}, "A")
	bHash := storeRawCommit(t, repo, treeHash, []plumbing.Hash{oHash}, "B")
	cHash := storeRawCommit(t, repo, treeHash, []plumbing.Hash{aHash, bHash}, "C: merge B into A")
	dHash := storeRawCommit(t, repo, treeHash, []plumbing.Hash{bHash, aHash}, "D: merge A into B")

	// HEAD → feature → C
	err = repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("feature"), cHash))
	require.NoError(t, err)
	err = repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("feature")))
	require.NoError(t, err)
	// origin/main → D
	err = repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), dHash))
	require.NoError(t, err)

	originalWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(originalWD))
	})

	log := logger.New("git-test-ambiguous")
	repoInstance, err := NewGitRepo(afero.NewOsFs(), portstest.NoopCmdRunner{}, utils.OsFileReader{}, log)
	require.NoError(t, err)

	_, err = repoInstance.GetChangedFiles("main", nil, "")
	require.ErrorIs(t, err, ErrAmbiguousMergeBase)
}

// storeEmptyTree writes an empty Git tree to the repository's object store
// and returns its hash. Used by tests that construct commits directly via the
// storer rather than through a worktree.
func storeEmptyTree(t *testing.T, repo *git.Repository) plumbing.Hash {
	t.Helper()
	tree := &object.Tree{}
	obj := repo.Storer.NewEncodedObject()
	require.NoError(t, tree.Encode(obj))
	hash, err := repo.Storer.SetEncodedObject(obj)
	require.NoError(t, err)
	return hash
}

// storeRawCommit writes a commit object with the given tree, parents, and
// message directly to the repository's object store and returns its hash.
// Lets tests assemble arbitrary commit topologies (orphans, criss-cross
// merges) that the worktree-based API cannot express.
func storeRawCommit(t *testing.T, repo *git.Repository, tree plumbing.Hash, parents []plumbing.Hash, msg string) plumbing.Hash {
	t.Helper()
	commit := &object.Commit{
		Author:       *defaultSignature(),
		Committer:    *defaultSignature(),
		Message:      msg,
		TreeHash:     tree,
		ParentHashes: parents,
	}
	obj := repo.Storer.NewEncodedObject()
	require.NoError(t, commit.Encode(obj))
	hash, err := repo.Storer.SetEncodedObject(obj)
	require.NoError(t, err)
	return hash
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
	require.ErrorIs(t, err, errGitFileDoesNotExist)

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

func TestGitRepoGetChangedFilesPopulatesAnchorGroups(t *testing.T) {
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "repo")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	repo, err := git.PlainInit(workDir, false)
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))))

	chartDir := filepath.Join(workDir, "charts", "foo")
	require.NoError(t, os.MkdirAll(chartDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, ".argo-compare.yml"), []byte("application:\n  path: apps/foo.yaml\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte("apiVersion: v2\nname: foo\nversion: 0.0.1\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte("replicaCount: 1\n"), 0o644))

	worktree, err := repo.Worktree()
	require.NoError(t, err)
	_, err = worktree.Add("charts/foo")
	require.NoError(t, err)
	commitHash, err := worktree.Commit("initial", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	remotePath := filepath.Join(tempDir, "origin.git")
	_, err = git.PlainInit(remotePath, true)
	require.NoError(t, err)
	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remotePath}})
	require.NoError(t, err)
	require.NoError(t, repo.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{"refs/heads/main:refs/heads/main"}}))
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), commitHash)))

	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("feature"), Create: true}))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte("replicaCount: 2\n"), 0o644))
	_, err = worktree.Add(fooValuesYAML)
	require.NoError(t, err)
	_, err = worktree.Commit("bump replicas", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	originalWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(originalWD))
	})

	log := logger.New("git-test-anchor")
	repoInstance, err := NewGitRepo(afero.NewOsFs(), portstest.NoopCmdRunner{}, utils.OsFileReader{}, log)
	require.NoError(t, err)

	result, err := repoInstance.GetChangedFiles("main", nil, ".argo-compare.yml")
	require.NoError(t, err)

	require.Empty(t, result.Applications, "the chart files are not Application manifests")
	require.Len(t, result.AnchorGroups, 1)
	g := result.AnchorGroups[0]
	require.True(t, filepath.IsAbs(g.Dir))
	require.Equal(t, "apps/foo.yaml", g.Anchor.Application.Path)
	require.Equal(t, []string{fooValuesYAML}, g.ChangedFiles)
}

func TestGitRepoGetChangedFilesNoAnchorDiscoveryWhenDisabled(t *testing.T) {
	tempDir := t.TempDir()
	workDir := filepath.Join(tempDir, "repo")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	repo, err := git.PlainInit(workDir, false)
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))))

	chartDir := filepath.Join(workDir, "charts", "foo")
	require.NoError(t, os.MkdirAll(chartDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, ".argo-compare.yml"), []byte("application:\n  path: apps/foo.yaml\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte("replicaCount: 1\n"), 0o644))

	worktree, err := repo.Worktree()
	require.NoError(t, err)
	_, err = worktree.Add("charts/foo")
	require.NoError(t, err)
	commitHash, err := worktree.Commit("initial", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	remotePath := filepath.Join(tempDir, "origin.git")
	_, err = git.PlainInit(remotePath, true)
	require.NoError(t, err)
	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remotePath}})
	require.NoError(t, err)
	require.NoError(t, repo.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{"refs/heads/main:refs/heads/main"}}))
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName("refs/remotes/origin/main"), commitHash)))

	require.NoError(t, worktree.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("feature"), Create: true}))
	require.NoError(t, os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte("replicaCount: 2\n"), 0o644))
	_, err = worktree.Add(fooValuesYAML)
	require.NoError(t, err)
	_, err = worktree.Commit("bump", &git.CommitOptions{Author: defaultSignature()})
	require.NoError(t, err)

	originalWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(originalWD))
	})

	log := logger.New("git-test-anchor-disabled")
	repoInstance, err := NewGitRepo(afero.NewOsFs(), portstest.NoopCmdRunner{}, utils.OsFileReader{}, log)
	require.NoError(t, err)

	result, err := repoInstance.GetChangedFiles("main", nil, "")
	require.NoError(t, err)
	require.Empty(t, result.AnchorGroups, "anchor discovery must be skipped when anchorFileName is empty")
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

	log := logger.New(fmt.Sprintf("git-test-%s", t.Name()))
	repoInstance, err := NewGitRepo(afero.NewOsFs(), portstest.NoopCmdRunner{}, utils.OsFileReader{}, log)
	require.NoError(t, err)

	return repoInstance, repo
}
