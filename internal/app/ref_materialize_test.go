package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testOriginURL = "https://git.example.com/org/gitops.git"

// stubRefTreeResolver stands in for the remote clone, recording the revisions
// it was asked for so the cache can be asserted.
type stubRefTreeResolver struct {
	tree  *object.Tree
	calls []string
	err   error
}

func (s *stubRefTreeResolver) Tree(_ context.Context, repoURL, revision string) (*object.Tree, error) {
	s.calls = append(s.calls, repoURL+"@"+revision)
	if s.err != nil {
		return nil, s.err
	}
	return s.tree, nil
}

// refApp builds a registry-chart Application whose values come from a ref
// source at refRepoURL.
func refApp(t *testing.T, refRepoURL string, valueFiles []string) models.Application {
	t.Helper()
	app := models.Application{Kind: models.KindApplication}
	app.Metadata.Name = "demo"
	app.Spec.Destination = &models.Destination{Namespace: "demo"}
	app.Spec.Sources = []*models.Source{
		{
			RepoURL:        "https://prometheus-community.github.io/helm-charts",
			Chart:          "prometheus",
			TargetRevision: "15.7.1",
			Helm:           models.HelmSource{ValueFiles: valueFiles},
		},
		{RepoURL: refRepoURL, TargetRevision: "dev", Ref: "values"},
	}
	require.NoError(t, app.Validate())
	return app
}

// newRefApp builds an App wired with an in-memory filesystem and the supplied
// remote resolver.
func newRefApp(t *testing.T, fs afero.Fs, resolver refTreeResolver) *App {
	t.Helper()
	return &App{
		cfg:      Config{TargetBranch: "main"},
		fs:       fs,
		refTrees: resolver,
		logger:   logger.New("ref-materialize-test"),
	}
}

func TestMaterializeRefSources_LocalSourceLeg(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "envs/prod"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "envs/prod/values.yaml"), []byte("replicaCount: 7\n"), 0o644))

	fs := afero.NewMemMapFs()
	target := &Target{TmpDir: "/run", Type: TargetTypeSource, App: refApp(t, testOriginURL, []string{"$values/envs/prod/values.yaml"})}
	appInstance := newRefApp(t, fs, nil)

	err := appInstance.materializeRefSources(context.Background(), target, repoRoot, testOriginURL, nil)

	require.NoError(t, err)
	content, readErr := afero.ReadFile(fs, filepath.Join("/run", "refs", TargetTypeSource, "values", "envs/prod/values.yaml"))
	require.NoError(t, readErr)
	assert.Equal(t, "replicaCount: 7\n", string(content), "the source leg must read the working tree")
}

func TestMaterializeRefSources_LocalDestinationLeg(t *testing.T) {
	tree := commitTreeWith(t, map[string]string{"envs/prod/values.yaml": "replicaCount: 1\n"})

	fs := afero.NewMemMapFs()
	target := &Target{TmpDir: "/run", Type: TargetTypeDestination, App: refApp(t, testOriginURL, []string{"$values/envs/prod/values.yaml"})}
	appInstance := newRefApp(t, fs, nil)

	err := appInstance.materializeRefSources(context.Background(), target, t.TempDir(), testOriginURL,
		func() (*object.Tree, error) { return tree, nil })

	require.NoError(t, err)
	content, readErr := afero.ReadFile(fs, filepath.Join("/run", "refs", TargetTypeDestination, "values", "envs/prod/values.yaml"))
	require.NoError(t, readErr)
	assert.Equal(t, "replicaCount: 1\n", string(content), "the destination leg must read the merge-base tree")
}

// TestMaterializeRefSources_SSHOriginMatchesHTTPSRef pins that repo identity,
// not string equality, decides local vs remote.
func TestMaterializeRefSources_SSHOriginMatchesHTTPSRef(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "values.yaml"), []byte("a: 1\n"), 0o644))

	fs := afero.NewMemMapFs()
	target := &Target{TmpDir: "/run", Type: TargetTypeSource, App: refApp(t, testOriginURL, []string{"$values/values.yaml"})}
	resolver := &stubRefTreeResolver{}
	appInstance := newRefApp(t, fs, resolver)

	err := appInstance.materializeRefSources(context.Background(), target, repoRoot,
		"ssh://git@git.example.com:1022/org/gitops.git", nil)

	require.NoError(t, err)
	assert.Empty(t, resolver.calls, "a ref pointing at origin must not be cloned")
}

func TestMaterializeRefSources_RemoteRef(t *testing.T) {
	tree := commitTreeWith(t, map[string]string{"envs/prod/values.yaml": "replicaCount: 3\n"})
	resolver := &stubRefTreeResolver{tree: tree}

	fs := afero.NewMemMapFs()
	app := refApp(t, "https://git.example.com/org/value-files.git", []string{"$values/envs/prod/values.yaml"})
	target := &Target{TmpDir: "/run", Type: TargetTypeSource, App: app}
	appInstance := newRefApp(t, fs, resolver)

	err := appInstance.materializeRefSources(context.Background(), target, t.TempDir(), testOriginURL, nil)

	require.NoError(t, err)
	content, readErr := afero.ReadFile(fs, filepath.Join("/run", "refs", TargetTypeSource, "values", "envs/prod/values.yaml"))
	require.NoError(t, readErr)
	assert.Equal(t, "replicaCount: 3\n", string(content))
	assert.Equal(t, []string{"https://git.example.com/org/value-files.git@dev"}, resolver.calls)
}

// TestMaterializeRefSources_RemoteRefPerLegRevision is the case that makes a
// bumped ref source visible: each leg's own manifest names its revision, so the
// two legs read different content. Leaving the revision alone is the same test
// with one revision, and then the external repository contributes no diff.
func TestMaterializeRefSources_RemoteRefPerLegRevision(t *testing.T) {
	const refRepo = "https://git.example.com/org/value-files.git"
	resolver := &revisionRefTreeResolver{trees: map[string]*object.Tree{
		"v1": commitTreeWith(t, map[string]string{"values.yaml": "replicaCount: 1\n"}),
		"v2": commitTreeWith(t, map[string]string{"values.yaml": "replicaCount: 2\n"}),
	}}
	fs := afero.NewMemMapFs()
	appInstance := newRefApp(t, fs, resolver)

	for leg, revision := range map[string]string{TargetTypeDestination: "v1", TargetTypeSource: "v2"} {
		app := refApp(t, refRepo, []string{"$values/values.yaml"})
		app.Spec.Sources[1].TargetRevision = revision
		target := &Target{TmpDir: "/run", Type: leg, App: app}
		require.NoError(t, appInstance.materializeRefSources(context.Background(), target, t.TempDir(), testOriginURL, nil))
	}

	dst, err := afero.ReadFile(fs, filepath.Join("/run", "refs", TargetTypeDestination, "values", "values.yaml"))
	require.NoError(t, err)
	src, err := afero.ReadFile(fs, filepath.Join("/run", "refs", TargetTypeSource, "values", "values.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "replicaCount: 1\n", string(dst))
	assert.Equal(t, "replicaCount: 2\n", string(src), "each leg reads the revision its own manifest names")
}

// revisionRefTreeResolver serves a different tree per revision, standing in for
// a values repository whose targetRevision the pull request changed.
type revisionRefTreeResolver struct {
	trees map[string]*object.Tree
}

func (r *revisionRefTreeResolver) Tree(_ context.Context, _, revision string) (*object.Tree, error) {
	tree, ok := r.trees[revision]
	if !ok {
		return nil, fmt.Errorf("no tree seeded for revision %q", revision)
	}
	return tree, nil
}

// TestMaterializeRefSources_LocalAndRemoteRefsTogether covers the per-ref
// lookup: each file must come from the source that declares its own ref, not
// from whichever one happens to be first.
func TestMaterializeRefSources_LocalAndRemoteRefsTogether(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "local.yaml"), []byte("from: working-tree\n"), 0o644))

	resolver := &stubRefTreeResolver{tree: commitTreeWith(t, map[string]string{"remote.yaml": "from: clone\n"})}
	fs := afero.NewMemMapFs()

	app := models.Application{Kind: models.KindApplication}
	app.Metadata.Name = "demo"
	app.Spec.Destination = &models.Destination{Namespace: "demo"}
	app.Spec.Sources = []*models.Source{
		{
			RepoURL:        "https://prometheus-community.github.io/helm-charts",
			Chart:          "prometheus",
			TargetRevision: "15.7.1",
			Helm:           models.HelmSource{ValueFiles: []string{"$local/local.yaml", "$remote/remote.yaml"}},
		},
		{RepoURL: testOriginURL, TargetRevision: "main", Ref: "local"},
		{RepoURL: "https://git.example.com/org/value-files.git", TargetRevision: "dev", Ref: "remote"},
	}
	require.NoError(t, app.Validate())
	target := &Target{TmpDir: "/run", Type: TargetTypeSource, App: app}

	require.NoError(t, newRefApp(t, fs, resolver).
		materializeRefSources(context.Background(), target, repoRoot, testOriginURL, nil))

	local, err := afero.ReadFile(fs, filepath.Join("/run", "refs", TargetTypeSource, "local", "local.yaml"))
	require.NoError(t, err)
	remote, err := afero.ReadFile(fs, filepath.Join("/run", "refs", TargetTypeSource, "remote", "remote.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "from: working-tree\n", string(local))
	assert.Equal(t, "from: clone\n", string(remote))
	assert.Equal(t, []string{"https://git.example.com/org/value-files.git@dev"}, resolver.calls,
		"only the ref source naming another repository is cloned")
}

// TestMaterializeRefSources_LocalRefIgnoresTargetRevision pins the documented
// contract: a ref source pointing at this repository follows merge-base-to-HEAD
// like a path-based source, so a revision it pins is never resolved.
func TestMaterializeRefSources_LocalRefIgnoresTargetRevision(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "values.yaml"), []byte("from: working-tree\n"), 0o644))
	baseTree := commitTreeWith(t, map[string]string{"values.yaml": "from: merge-base\n"})

	resolver := &stubRefTreeResolver{}
	fs := afero.NewMemMapFs()
	appInstance := newRefApp(t, fs, resolver)

	for _, leg := range []string{TargetTypeSource, TargetTypeDestination} {
		app := refApp(t, testOriginURL, []string{"$values/values.yaml"})
		app.Spec.Sources[1].TargetRevision = "does-not-exist"
		target := &Target{TmpDir: "/run", Type: leg, App: app}
		require.NoError(t, appInstance.materializeRefSources(context.Background(), target, repoRoot, testOriginURL,
			func() (*object.Tree, error) { return baseTree, nil }))
	}

	src, err := afero.ReadFile(fs, filepath.Join("/run", "refs", TargetTypeSource, "values", "values.yaml"))
	require.NoError(t, err)
	dst, err := afero.ReadFile(fs, filepath.Join("/run", "refs", TargetTypeDestination, "values", "values.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "from: working-tree\n", string(src))
	assert.Equal(t, "from: merge-base\n", string(dst))
	assert.Empty(t, resolver.calls, "a pinned revision on a same-repo ref must not trigger a clone")
}

func TestMaterializeRefSources_MissingFileInWorkingTree(t *testing.T) {
	fs := afero.NewMemMapFs()
	target := &Target{TmpDir: "/run", Type: TargetTypeSource, App: refApp(t, testOriginURL, []string{"$values/envs/prod/values.yaml"})}
	appInstance := newRefApp(t, fs, nil)

	err := appInstance.materializeRefSources(context.Background(), target, t.TempDir(), testOriginURL, nil)

	require.ErrorIs(t, err, ErrRefValueFileMissing)
	assert.Contains(t, err.Error(), "envs/prod/values.yaml")
	assert.Contains(t, err.Error(), "values")
}

func TestMaterializeRefSources_MissingFileInTree(t *testing.T) {
	tree := commitTreeWith(t, map[string]string{"other.yaml": "a: 1\n"})
	fs := afero.NewMemMapFs()
	target := &Target{TmpDir: "/run", Type: TargetTypeDestination, App: refApp(t, testOriginURL, []string{"$values/envs/prod/values.yaml"})}
	appInstance := newRefApp(t, fs, nil)

	err := appInstance.materializeRefSources(context.Background(), target, t.TempDir(), testOriginURL,
		func() (*object.Tree, error) { return tree, nil })

	require.ErrorIs(t, err, ErrRefValueFileMissing)
}

// TestMaterializeRefSources_NoOrigin refuses to guess: without an origin URL a
// ref source cannot be classified as local or remote.
func TestMaterializeRefSources_NoOrigin(t *testing.T) {
	fs := afero.NewMemMapFs()
	target := &Target{TmpDir: "/run", Type: TargetTypeSource, App: refApp(t, testOriginURL, []string{"$values/values.yaml"})}
	appInstance := newRefApp(t, fs, nil)

	err := appInstance.materializeRefSources(context.Background(), target, t.TempDir(), "", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "origin")
}

// TestMaterializeRefSources_NoRefEntriesIsNoop keeps the common single-source
// path free of ref machinery.
func TestMaterializeRefSources_NoRefEntriesIsNoop(t *testing.T) {
	fs := afero.NewMemMapFs()
	app := models.Application{Kind: models.KindApplication}
	app.Metadata.Name = "demo"
	app.Spec.Source = &models.Source{RepoURL: "https://example.com/charts", Chart: "demo", TargetRevision: "1.0.0",
		Helm: models.HelmSource{ValueFiles: []string{"values.yaml"}}}
	require.NoError(t, app.Validate())
	target := &Target{TmpDir: "/run", Type: TargetTypeSource, App: app}
	appInstance := newRefApp(t, fs, nil)

	err := appInstance.materializeRefSources(context.Background(), target, t.TempDir(), "", nil)

	require.NoError(t, err, "an Application without $ref entries must not need an origin")
}

// TestRefCloneAuthOnlyOnOriginHost is the credential boundary: a ref source's
// repoURL comes from a pull-request-author-controlled manifest, so offering the
// Git token to the host it names would hand a CI credential to any repository a
// branch chooses to point at.
func TestRefCloneAuthOnlyOnOriginHost(t *testing.T) {
	tests := []struct {
		name      string
		originURL string
		repoURL   string
		wantAuth  bool
	}{
		{
			name:      "same host",
			originURL: "https://git.example.com/org/gitops.git",
			repoURL:   "https://git.example.com/org/value-files.git",
			wantAuth:  true,
		},
		{
			name:      "same host over ssh origin",
			originURL: "ssh://git@git.example.com:1022/org/gitops.git",
			repoURL:   "https://git.example.com/org/value-files.git",
			wantAuth:  true,
		},
		{
			name:      "foreign host",
			originURL: "https://git.example.com/org/gitops.git",
			repoURL:   "https://attacker.tld/exfiltrate.git",
			wantAuth:  false,
		},
		{
			name:      "lookalike subdomain",
			originURL: "https://git.example.com/org/gitops.git",
			repoURL:   "https://git.example.com.attacker.tld/x.git",
			wantAuth:  false,
		},
		{
			name:      "no origin to compare against",
			originURL: "",
			repoURL:   "https://git.example.com/org/value-files.git",
			wantAuth:  false,
		},
		{
			// go-git's ssh transport rejects a BasicAuth method, so attaching one
			// breaks agent authentication that would otherwise work.
			name:      "ssh ref on the origin host",
			originURL: "https://git.example.com/org/gitops.git",
			repoURL:   "ssh://git@git.example.com/org/value-files.git",
			wantAuth:  false,
		},
		{
			name:      "scp-style ref on the origin host",
			originURL: "https://git.example.com/org/gitops.git",
			repoURL:   "git@git.example.com:org/value-files.git",
			wantAuth:  false,
		},
		{
			// The userinfo names origin's host but net/url splits at the last "@".
			name:      "userinfo spoofing the origin host",
			originURL: "https://git.example.com/org/gitops.git",
			repoURL:   "https://git.example.com@attacker.tld/x.git",
			wantAuth:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fetcher := &gitRefTreeFetcher{username: "ci", token: "s3cret", originURL: tc.originURL}

			opts := fetcher.cloneOptions(tc.repoURL, plumbing.NewBranchReferenceName("main"))

			if tc.wantAuth {
				require.NotNil(t, opts.Auth, "a values repository on the origin host must authenticate")
				return
			}
			assert.Nil(t, opts.Auth, "the Git token must not be sent to %s", tc.repoURL)
		})
	}
}

// TestRefCloneNoTokenNoAuth keeps an unauthenticated run unauthenticated.
func TestRefCloneNoTokenNoAuth(t *testing.T) {
	fetcher := &gitRefTreeFetcher{originURL: "https://git.example.com/org/gitops.git"}

	opts := fetcher.cloneOptions("https://git.example.com/org/value-files.git", plumbing.NewBranchReferenceName("main"))

	assert.Nil(t, opts.Auth)
}

// TestRefTreeCacheKeyKeepsPortsApart guards against the lossy repo identity
// used for origin matching being reused as a cache key: two hosts differing
// only by port are different repositories and must not share a tree.
func TestRefTreeCacheKeyKeepsPortsApart(t *testing.T) {
	first := commitTreeWith(t, map[string]string{"values.yaml": "from: 8080\n"})
	fetcher := &gitRefTreeFetcher{trees: map[string]*object.Tree{}}

	fetcher.trees[refTreeCacheKey("https://git.example.com:8080/org/repo.git", "main")] = first

	_, hit := fetcher.trees[refTreeCacheKey("https://git.example.com:9090/org/repo.git", "main")]

	assert.False(t, hit, "a different port is a different repository")
}

// TestRemoteRefRejectsCommitSHA names the limit instead of failing twice with a
// message that reads as if a tag were expected.
func TestRemoteRefRejectsCommitSHA(t *testing.T) {
	fetcher := &gitRefTreeFetcher{trees: map[string]*object.Tree{}}

	_, err := fetcher.Tree(context.Background(), "https://git.example.com/org/values.git",
		"1234567890abcdef1234567890abcdef12345678")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must name a branch or a tag")
}

// TestMaterializeRefSources_RejectsSymlinkOutOfRepo is the exfiltration a
// lexical path check cannot stop: a symlink committed on the branch resolves
// outside the repository, and the file it names would otherwise be rendered
// into a diff posted as a merge request comment.
func TestMaterializeRefSources_RejectsSymlinkOutOfRepo(t *testing.T) {
	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "credentials.yaml")
	require.NoError(t, os.WriteFile(secret, []byte("token: s3cret\n"), 0o600))

	repoRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "envs/prod"), 0o755))
	require.NoError(t, os.Symlink(secret, filepath.Join(repoRoot, "envs/prod/values.yaml")))

	fs := afero.NewMemMapFs()
	target := &Target{TmpDir: "/run", Type: TargetTypeSource, App: refApp(t, testOriginURL, []string{"$values/envs/prod/values.yaml"})}
	appInstance := newRefApp(t, fs, nil)

	err := appInstance.materializeRefSources(context.Background(), target, repoRoot, testOriginURL, nil)

	require.Error(t, err, "a symlink leaving the repository must not be read")
	assert.NotContains(t, err.Error(), "s3cret")
	written, _ := afero.ReadFile(fs, filepath.Join("/run", "refs", TargetTypeSource, "values", "envs/prod/values.yaml"))
	assert.NotContains(t, string(written), "s3cret", "the secret must never reach the render workspace")
}

// TestMaterializeRefSources_RejectsInRepoSymlink matches the policy
// copyDirOnDisk already applies to chart content: a Git tree records a link's
// target rather than its contents, so the destination leg cannot mirror one and
// the diff would be misleading either way.
func TestMaterializeRefSources_RejectsInRepoSymlink(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "envs/prod"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "base.yaml"), []byte("replicaCount: 1\n"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(repoRoot, "base.yaml"), filepath.Join(repoRoot, "envs/prod/values.yaml")))

	fs := afero.NewMemMapFs()
	target := &Target{TmpDir: "/run", Type: TargetTypeSource, App: refApp(t, testOriginURL, []string{"$values/envs/prod/values.yaml"})}
	appInstance := newRefApp(t, fs, nil)

	err := appInstance.materializeRefSources(context.Background(), target, repoRoot, testOriginURL, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "regular file")
}

// TestValidateRelValueFileRejectsGitDir keeps the repository's own Git metadata
// out of reach: on a CI runner .git/config carries the credential the checkout
// was made with.
func TestValidateRelValueFileRejectsGitDir(t *testing.T) {
	for _, p := range []string{".git/config", ".git/../.git/config", ".GIT/config"} {
		assert.ErrorIs(t, validateRelValueFile(p), ErrInvalidValueFilePath, "expected rejection of %q", p)
	}
	assert.NoError(t, validateRelValueFile(".gitkeep-values.yaml"), "a file merely starting with .git is fine")
}

// TestMaterializeRefSources_RemoteFailureIsRedacted keeps URL userinfo out of
// the error, which is published as a merge request comment. redactRepo strips
// the whole userinfo component, so a username stands in for the credential a
// misconfigured repoURL would carry there.
func TestMaterializeRefSources_RemoteFailureIsRedacted(t *testing.T) {
	resolver := &stubRefTreeResolver{err: errors.New("authentication required")}
	fs := afero.NewMemMapFs()
	app := refApp(t, "https://embedded-userinfo@git.example.com/org/value-files.git", []string{"$values/values.yaml"})
	target := &Target{TmpDir: "/run", Type: TargetTypeSource, App: app}

	err := newRefApp(t, fs, resolver).
		materializeRefSources(context.Background(), target, t.TempDir(), testOriginURL, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication required")
	assert.Contains(t, err.Error(), "git.example.com/org/value-files.git", "the repository is still named")
	assert.NotContains(t, err.Error(), "embedded-userinfo", "userinfo must be redacted")
}

// TestMaterializeRefSources_DestinationLegErrors covers the two ways the
// merge-base tree can be unavailable.
func TestMaterializeRefSources_DestinationLegErrors(t *testing.T) {
	target := func() *Target {
		return &Target{TmpDir: "/run", Type: TargetTypeDestination,
			App: refApp(t, testOriginURL, []string{"$values/values.yaml"})}
	}

	t.Run("no tree supplied", func(t *testing.T) {
		err := newRefApp(t, afero.NewMemMapFs(), nil).
			materializeRefSources(context.Background(), target(), t.TempDir(), testOriginURL, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "merge-base tree")
	})

	t.Run("tree resolution fails", func(t *testing.T) {
		sentinel := errors.New("no common ancestor")

		err := newRefApp(t, afero.NewMemMapFs(), nil).
			materializeRefSources(context.Background(), target(), t.TempDir(), testOriginURL,
				func() (*object.Tree, error) { return nil, sentinel })

		require.ErrorIs(t, err, sentinel)
	})
}

// TestMaterializeRefSources_UnknownLeg guards the switch's default arm.
func TestMaterializeRefSources_UnknownLeg(t *testing.T) {
	target := &Target{TmpDir: "/run", Type: "sideways", App: refApp(t, testOriginURL, []string{"$values/values.yaml"})}

	err := newRefApp(t, afero.NewMemMapFs(), nil).
		materializeRefSources(context.Background(), target, t.TempDir(), testOriginURL, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown render leg")
}

// TestMaterializeRefSources_ContextCancelled stops before writing: an
// ApplicationSet can ask for many files, each potentially a clone.
func TestMaterializeRefSources_ContextCancelled(t *testing.T) {
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "values.yaml"), []byte("a: 1\n"), 0o644))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fs := afero.NewMemMapFs()
	target := &Target{TmpDir: "/run", Type: TargetTypeSource, App: refApp(t, testOriginURL, []string{"$values/values.yaml"})}

	err := newRefApp(t, fs, nil).materializeRefSources(ctx, target, repoRoot, testOriginURL, nil)

	require.ErrorIs(t, err, context.Canceled)
	exists, _ := afero.Exists(fs, filepath.Join("/run", "refs", TargetTypeSource, "values", "values.yaml"))
	assert.False(t, exists, "a cancelled run must not write into the workspace")
}
