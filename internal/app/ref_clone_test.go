package app

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedRefRepo builds a repository on `main` holding values.yaml, tags the first
// commit as v1, then changes the file on main. The clone path can therefore be
// driven offline: a branch and a tag resolve to different content.
func seedRefRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(
		plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))))

	worktree, err := repo.Worktree()
	require.NoError(t, err)

	write := func(content string) plumbing.Hash {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "values.yaml"), []byte(content), 0o644))
		_, addErr := worktree.Add("values.yaml")
		require.NoError(t, addErr)
		hash, commitErr := worktree.Commit(content, &git.CommitOptions{Author: defaultSignature()})
		require.NoError(t, commitErr)
		return hash
	}

	tagged := write("replicaCount: 1\n")
	_, err = repo.CreateTag("v1", tagged, nil)
	require.NoError(t, err)
	write("replicaCount: 9\n")

	return dir
}

// treeFileContent reads one file out of a tree.
func treeFileContent(t *testing.T, tree *object.Tree, path string) string {
	t.Helper()
	file, err := tree.File(path)
	require.NoError(t, err)
	content, err := file.Contents()
	require.NoError(t, err)
	return content
}

func TestRefCloneResolvesBranchTagAndDefault(t *testing.T) {
	repoDir := seedRefRepo(t)

	tests := []struct {
		name     string
		revision string
		want     string
	}{
		{name: "branch", revision: "main", want: "replicaCount: 9\n"},
		{name: "tag", revision: "v1", want: "replicaCount: 1\n"},
		{name: "empty means default branch", revision: "", want: "replicaCount: 9\n"},
		{name: "HEAD means default branch", revision: "HEAD", want: "replicaCount: 9\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fetcher := &gitRefTreeFetcher{trees: map[string]*object.Tree{}}

			tree, err := fetcher.Tree(context.Background(), repoDir, tc.revision)

			require.NoError(t, err)
			assert.Equal(t, tc.want, treeFileContent(t, tree, "values.yaml"))
		})
	}
}

// TestRefCloneCachesPerRevision is the failure mode a repository-only cache key
// would cause: both legs would share the first tree, so a pull request bumping
// the ref source's targetRevision would report no change at all.
func TestRefCloneCachesPerRevision(t *testing.T) {
	repoDir := seedRefRepo(t)
	fetcher := &gitRefTreeFetcher{trees: map[string]*object.Tree{}}

	main, err := fetcher.Tree(context.Background(), repoDir, "main")
	require.NoError(t, err)
	tagged, err := fetcher.Tree(context.Background(), repoDir, "v1")
	require.NoError(t, err)

	assert.Equal(t, "replicaCount: 9\n", treeFileContent(t, main, "values.yaml"))
	assert.Equal(t, "replicaCount: 1\n", treeFileContent(t, tagged, "values.yaml"),
		"a second revision of the same repository must not reuse the first tree")
	assert.Len(t, fetcher.trees, 2)
}

func TestRefCloneReusesOneTreePerRevision(t *testing.T) {
	repoDir := seedRefRepo(t)
	fetcher := &gitRefTreeFetcher{trees: map[string]*object.Tree{}}

	first, err := fetcher.Tree(context.Background(), repoDir, "main")
	require.NoError(t, err)
	second, err := fetcher.Tree(context.Background(), repoDir, "main")
	require.NoError(t, err)

	assert.Same(t, first, second, "one clone per repository and revision per run")
	assert.Len(t, fetcher.trees, 1)
}

func TestRefTreeCacheKeyIncludesRevision(t *testing.T) {
	const url = "https://git.example.com/org/repo.git"

	assert.NotEqual(t, refTreeCacheKey(url, "main"), refTreeCacheKey(url, "v2"),
		"two revisions of one repository are two different trees")
}

func TestRefCloneUnknownRevision(t *testing.T) {
	repoDir := seedRefRepo(t)
	fetcher := &gitRefTreeFetcher{trees: map[string]*object.Tree{}}

	_, err := fetcher.Tree(context.Background(), repoDir, "no-such-thing")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "refs/heads/no-such-thing", "both attempts must be reported")
	assert.Contains(t, err.Error(), "refs/tags/no-such-thing")
}

func TestRefCloneCandidates(t *testing.T) {
	tests := []struct {
		name     string
		revision string
		want     []plumbing.ReferenceName
	}{
		{name: "empty", revision: "", want: []plumbing.ReferenceName{""}},
		{name: "HEAD", revision: "HEAD", want: []plumbing.ReferenceName{""}},
		{
			name:     "named revision tries branch before tag",
			revision: "release",
			want: []plumbing.ReferenceName{
				plumbing.NewBranchReferenceName("release"),
				plumbing.NewTagReferenceName("release"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, refCloneCandidates(tc.revision))
		})
	}
}

// TestRefCloneOptionsStayShallow pins the clone shape: dropping either field
// turns every remote ref source into a full-history clone of an entire
// repository on a CI runner.
func TestRefCloneOptionsStayShallow(t *testing.T) {
	fetcher := &gitRefTreeFetcher{}
	refName := plumbing.NewTagReferenceName("v1")

	opts := fetcher.cloneOptions("https://git.example.com/org/values.git", refName)

	assert.True(t, opts.SingleBranch)
	assert.Equal(t, 1, opts.Depth)
	assert.Equal(t, git.NoTags, opts.Tags)
	assert.Equal(t, refName, opts.ReferenceName)
}

// TestRefCloneDefaultUsername covers the token-only configuration, where the
// username is a placeholder the forges accept.
func TestRefCloneDefaultUsername(t *testing.T) {
	fetcher := &gitRefTreeFetcher{token: "s3cret", originURL: testOriginURL}

	opts := fetcher.cloneOptions("https://git.example.com/org/values.git", plumbing.NewBranchReferenceName("main"))

	require.NotNil(t, opts.Auth)
	assert.Contains(t, opts.Auth.String(), defaultGitUsername)
}

// TestRemoteRefRejectsCommitPins separates a full hash, which is refused
// without any network attempt, from a name that merely looks like one, which is
// still resolved as a branch and a tag before the hint is offered.
func TestRemoteRefRejectsCommitPins(t *testing.T) {
	tests := []struct {
		name         string
		revision     string
		wantAttempts bool
	}{
		{name: "sha-1 is refused outright", revision: "1234567890abcdef1234567890abcdef12345678"},
		{
			name:     "sha-256 is refused outright",
			revision: "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		},
		{
			name:         "forty non-hex characters is a branch name",
			revision:     "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
			wantAttempts: true,
		},
		{name: "short hex is attempted, then hinted", revision: "abc1234", wantAttempts: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fetcher := &gitRefTreeFetcher{trees: map[string]*object.Tree{}}

			_, err := fetcher.Tree(context.Background(), t.TempDir(), tc.revision)

			require.Error(t, err)
			if !tc.wantAttempts {
				assert.Contains(t, err.Error(), "must name a branch or a tag")
				assert.NotContains(t, err.Error(), "refs/heads/", "a full hash must not be cloned for")
				return
			}
			assert.Contains(t, err.Error(), "refs/heads/"+tc.revision, "a name must be resolved before it is judged")
			assert.Contains(t, err.Error(), "refs/tags/"+tc.revision)
		})
	}
}

// TestCommitPinHintForShortSHA keeps the diagnosis on an abbreviated hash,
// which is accepted by ArgoCD but cannot be shallow-cloned here.
func TestCommitPinHintForShortSHA(t *testing.T) {
	assert.Contains(t, commitPinHint("abc1234"), "not a commit")
	assert.Empty(t, commitPinHint("main"))
	assert.Empty(t, commitPinHint("abc12"), "too short to be a commit abbreviation")
}

// TestRefTreeResolverWiring pins what the default fetcher is built with: the
// local origin, not the ref source's own URL. Feeding it the wrong URL would
// either offer the token to a host a pull request named, or drop auth for a
// legitimate same-host values repository.
func TestRefTreeResolverWiring(t *testing.T) {
	appInstance := &App{cfg: Config{GitUsername: "ci", GitToken: "s3cret"}}

	resolver := appInstance.refTreeResolver(testOriginURL)

	fetcher, ok := resolver.(*gitRefTreeFetcher)
	require.True(t, ok, "the default resolver must be the clone fetcher")
	assert.Equal(t, testOriginURL, fetcher.originURL)
	assert.Equal(t, "ci", fetcher.username)
	assert.Equal(t, "s3cret", fetcher.token)

	sameHost := fetcher.cloneOptions("https://git.example.com/org/values.git", plumbing.NewBranchReferenceName("main"))
	assert.NotNil(t, sameHost.Auth, "a values repository on the origin host authenticates")
	foreign := fetcher.cloneOptions("https://attacker.tld/x.git", plumbing.NewBranchReferenceName("main"))
	assert.Nil(t, foreign.Auth, "the token must not reach a host the manifest named")

	assert.Same(t, resolver, appInstance.refTreeResolver(testOriginURL), "one fetcher per run, so its cache is shared")
}

// TestRefTreeResolverHonoursInjection keeps the seam tests rely on.
func TestRefTreeResolverHonoursInjection(t *testing.T) {
	stub := &stubRefTreeResolver{}
	appInstance := &App{refTrees: stub}

	assert.Same(t, stub, appInstance.refTreeResolver(testOriginURL))
}

// TestRefCloneSendsNoTokenToAlternatePort is the attack a reviewer captured
// against the first version of the host check: a ref URL on origin's hostname
// but another port reached a different service, and over plain http it carried
// the CI token in cleartext. Asserted against a real listener, not the guard.
func TestRefCloneSendsNoTokenToAlternatePort(t *testing.T) {
	var mu sync.Mutex
	var authSeen []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authSeen = append(authSeen, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// The listener is on 127.0.0.1 with an arbitrary port; origin claims the same
	// host on the default one, which is exactly the mismatch under test.
	host, _, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)

	fetcher := &gitRefTreeFetcher{
		username:  "ci",
		token:     "s3cret",
		originURL: "https://" + host + "/org/gitops.git",
		trees:     map[string]*object.Tree{},
	}

	_, err = fetcher.Tree(context.Background(), server.URL+"/org/values.git", "main")
	require.Error(t, err, "the clone must fail; what matters is what it sent")

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, authSeen, "the listener saw no request, so the assertion would be vacuous")
	for _, header := range authSeen {
		assert.Empty(t, header, "no Authorization header may reach a non-origin endpoint")
	}
}
