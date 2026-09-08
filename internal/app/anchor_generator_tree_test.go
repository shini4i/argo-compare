package app

import (
	"sort"
	"testing"

	"github.com/shini4i/argo-compare/internal/anchor"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testValuesRepo = "https://git.example.com/org/values.git"
	testGitOpsRepo = "https://git.example.com/org/gitops.git"
)

// stubRepoTree is a ports.RepoTree over a fixed file set, standing in for the
// clone a cross-repo fetch produces.
type stubRepoTree struct {
	files map[string]string
}

func (s stubRepoTree) Files() ([]string, error) {
	names := make([]string, 0, len(s.files))
	for name := range s.files {
		names = append(names, name)
	}
	// gitTree sorts, so expansion order stays deterministic.
	sort.Strings(names)
	return names, nil
}

func (s stubRepoTree) Directories() ([]string, error) {
	files, err := s.Files()
	if err != nil {
		return nil, err
	}
	return directoriesOf(files), nil
}

func (s stubRepoTree) ReadFile(path string) ([]byte, error) {
	content, ok := s.files[path]
	if !ok {
		return nil, assert.AnError
	}
	return []byte(content), nil
}

// anchoredGeneratorsAppSet builds an ApplicationSet with one git directories
// generator per supplied repoURL/revision pair.
func anchoredGeneratorsAppSet(generators ...[2]string) *models.ApplicationSet {
	appSet := &models.ApplicationSet{}
	for _, generator := range generators {
		appSet.Spec.Generators = append(appSet.Spec.Generators, models.Generator{
			Git: &models.GitGenerator{
				RepoURL:     generator[0],
				Revision:    generator[1],
				Directories: []models.GitDirectory{{Path: "envs/*"}},
			},
		})
	}
	return appSet
}

// crossRepoAnchor points at an ApplicationSet stored in the gitops repository.
func crossRepoAnchor() anchor.ApplicationRef {
	return anchor.ApplicationRef{Repo: testGitOpsRepo, Path: "apps/demo.yaml"}
}

// TestAnchoredGeneratorTreeNoGitGenerator leaves the local branch legs in
// charge: a list generator reads no tree at all.
func TestAnchoredGeneratorTreeNoGitGenerator(t *testing.T) {
	manifest := ports.AnchoredManifest{ApplicationSet: &models.ApplicationSet{}}

	tree, err := anchoredGeneratorTree(manifest, crossRepoAnchor(), testValuesRepo, "main")

	require.NoError(t, err)
	assert.Nil(t, tree, "without a git generator each leg expands against its own tree")
}

// TestAnchoredGeneratorTreeLocalGenerator keeps a generator reading this
// repository on the per-leg path, so an added directory is still reported.
func TestAnchoredGeneratorTreeLocalGenerator(t *testing.T) {
	manifest := ports.AnchoredManifest{ApplicationSet: anchoredGeneratorsAppSet([2]string{testValuesRepo, "HEAD"})}

	tree, err := anchoredGeneratorTree(manifest, crossRepoAnchor(), testValuesRepo, "main")

	require.NoError(t, err)
	assert.Nil(t, tree)
}

// TestAnchoredGeneratorTreeAnchorRepoGenerator is the gap this change closes:
// the generator reads the repository the anchor fetched the manifest from, so
// the fetcher's clone is what it expands against.
func TestAnchoredGeneratorTreeAnchorRepoGenerator(t *testing.T) {
	clone := stubRepoTree{files: map[string]string{"envs/dev/config.yaml": "a: 1\n"}}
	manifest := ports.AnchoredManifest{
		ApplicationSet: anchoredGeneratorsAppSet([2]string{testGitOpsRepo, ""}),
		Tree:           clone,
		TreeRevision:   "main",
	}

	tree, err := anchoredGeneratorTree(manifest, crossRepoAnchor(), testValuesRepo, "main")

	require.NoError(t, err)
	assert.Equal(t, clone, tree, "the anchored repository's clone must expand the generator")
}

// TestAnchoredGeneratorTreeAnchorRepoRevisionMatchesClone accepts a generator
// naming the exact revision the fetcher cloned.
func TestAnchoredGeneratorTreeAnchorRepoRevisionMatchesClone(t *testing.T) {
	clone := stubRepoTree{}
	manifest := ports.AnchoredManifest{
		ApplicationSet: anchoredGeneratorsAppSet([2]string{testGitOpsRepo, "release"}),
		Tree:           clone,
		TreeRevision:   "release",
	}

	tree, err := anchoredGeneratorTree(manifest, anchor.ApplicationRef{Repo: testGitOpsRepo, Path: "apps/demo.yaml", Branch: "release"}, testValuesRepo, "main")

	require.NoError(t, err)
	assert.Equal(t, clone, tree)
}

// TestAnchoredGeneratorTreeAnchorRepoRevisionMismatch refuses a generator
// pinned to a revision the fetcher did not clone: listing the cloned tree
// would generate Applications from the wrong directories.
func TestAnchoredGeneratorTreeAnchorRepoRevisionMismatch(t *testing.T) {
	manifest := ports.AnchoredManifest{
		ApplicationSet: anchoredGeneratorsAppSet([2]string{testGitOpsRepo, "v1.2.3"}),
		Tree:           stubRepoTree{},
		TreeRevision:   "main",
	}

	_, err := anchoredGeneratorTree(manifest, crossRepoAnchor(), testValuesRepo, "main")

	require.ErrorIs(t, err, models.ErrUnsupportedAppConfiguration)
	assert.Contains(t, err.Error(), "v1.2.3")
	assert.Contains(t, err.Error(), "main")
}

// TestAnchoredGeneratorTreeAnchorRepoWithoutTree reports the missing clone
// rather than falling back to a local tree that holds none of those
// directories, which would generate nothing and read as "no changes".
func TestAnchoredGeneratorTreeAnchorRepoWithoutTree(t *testing.T) {
	manifest := ports.AnchoredManifest{ApplicationSet: anchoredGeneratorsAppSet([2]string{testGitOpsRepo, ""})}

	_, err := anchoredGeneratorTree(manifest, crossRepoAnchor(), testValuesRepo, "main")

	require.ErrorIs(t, err, models.ErrUnsupportedAppConfiguration)
	assert.Contains(t, err.Error(), "no tree")
}

// TestAnchoredGeneratorTreeThirdRepo refuses a generator naming a repository
// that is neither this one nor the anchored one — nothing holds its tree.
func TestAnchoredGeneratorTreeThirdRepo(t *testing.T) {
	manifest := ports.AnchoredManifest{
		ApplicationSet: anchoredGeneratorsAppSet([2]string{"https://git.example.com/org/other.git", ""}),
		Tree:           stubRepoTree{},
		TreeRevision:   "main",
	}

	_, err := anchoredGeneratorTree(manifest, crossRepoAnchor(), testValuesRepo, "main")

	require.ErrorIs(t, err, models.ErrUnsupportedAppConfiguration)
	assert.Contains(t, err.Error(), "other.git")
}

// TestAnchoredGeneratorTreeMixedRepos refuses an ApplicationSet whose
// generators read both repositories: expansion takes one tree, so serving both
// would silently drop one generator's Applications.
func TestAnchoredGeneratorTreeMixedRepos(t *testing.T) {
	manifest := ports.AnchoredManifest{
		ApplicationSet: anchoredGeneratorsAppSet(
			[2]string{testValuesRepo, "HEAD"},
			[2]string{testGitOpsRepo, ""},
		),
		Tree:         stubRepoTree{},
		TreeRevision: "main",
	}

	_, err := anchoredGeneratorTree(manifest, crossRepoAnchor(), testValuesRepo, "main")

	require.ErrorIs(t, err, models.ErrUnsupportedAppConfiguration)
	assert.Contains(t, err.Error(), "both")
}

// TestAnchoredGeneratorTreeLocalRevisionPinned preserves the existing refusal
// of a local generator pinned away from the compared branch.
func TestAnchoredGeneratorTreeLocalRevisionPinned(t *testing.T) {
	manifest := ports.AnchoredManifest{ApplicationSet: anchoredGeneratorsAppSet([2]string{testValuesRepo, "v9"})}

	_, err := anchoredGeneratorTree(manifest, crossRepoAnchor(), testValuesRepo, "main")

	require.ErrorIs(t, err, models.ErrUnsupportedAppConfiguration)
	assert.Contains(t, err.Error(), "v9")
}

// TestAnchoredGeneratorTreeSameRepoAnchor treats a same-repo anchor as before:
// there is no clone, so only a local generator is comparable.
func TestAnchoredGeneratorTreeSameRepoAnchor(t *testing.T) {
	localAnchor := anchor.ApplicationRef{Path: "apps/demo.yaml"}

	tree, err := anchoredGeneratorTree(
		ports.AnchoredManifest{ApplicationSet: anchoredGeneratorsAppSet([2]string{testValuesRepo, "HEAD"})},
		localAnchor, testValuesRepo, "main")
	require.NoError(t, err)
	assert.Nil(t, tree)

	_, err = anchoredGeneratorTree(
		ports.AnchoredManifest{ApplicationSet: anchoredGeneratorsAppSet([2]string{testGitOpsRepo, ""})},
		localAnchor, testValuesRepo, "main")
	require.ErrorIs(t, err, models.ErrUnsupportedAppConfiguration)
}

// TestAnchoredGeneratorTreeListPlusAnchoredGit keeps a non-git generator out of
// the repository count, so pairing one with an anchored git generator is not
// mistaken for reading both repositories.
func TestAnchoredGeneratorTreeListPlusAnchoredGit(t *testing.T) {
	clone := stubRepoTree{}
	appSet := anchoredGeneratorsAppSet([2]string{testGitOpsRepo, ""})
	appSet.Spec.Generators = append(appSet.Spec.Generators, models.Generator{
		List: &models.ListGenerator{Elements: []map[string]any{{"cluster": "dev"}}},
	})

	tree, err := anchoredGeneratorTree(
		ports.AnchoredManifest{ApplicationSet: appSet, Tree: clone, TreeRevision: "main"},
		crossRepoAnchor(), testValuesRepo, "main")

	require.NoError(t, err)
	assert.Equal(t, clone, tree)
}

// TestAnchoredGeneratorTreeSelfReferentialAnchor keeps the branch legs when the
// anchor names this repository: a shared tree would report no directory the
// change adds or drops.
func TestAnchoredGeneratorTreeSelfReferentialAnchor(t *testing.T) {
	selfAnchor := anchor.ApplicationRef{Repo: testValuesRepo, Path: "apps/demo.yaml"}
	manifest := ports.AnchoredManifest{
		ApplicationSet: anchoredGeneratorsAppSet([2]string{testValuesRepo, "HEAD"}),
		Tree:           stubRepoTree{},
		TreeRevision:   "main",
	}

	tree, err := anchoredGeneratorTree(manifest, selfAnchor, testValuesRepo, "main")

	require.NoError(t, err)
	assert.Nil(t, tree, "origin takes precedence, so each leg keeps its own tree")
}
