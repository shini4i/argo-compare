# Review context for argo-compare

These are deliberate design contracts, not oversights. Findings that ask for
them to be reversed have been raised and declined before.

## What the tool compares

`argo-compare` reports **what a pull request proposes**, not the current live
state. `GetChangedFiles` diffs `merge-base..HEAD`:

- the source leg is the branch's HEAD — the proposed post-merge state
- the target leg is the merge base — the state before the branch diverged
- commits made only on the target branch after divergence are excluded on
  purpose, because they are not what the branch is proposing

Nothing the tool prints is live yet. A reported addition means "this is what
merging will change", so judging output against the target branch's current tip
misreads the contract.

This is why an ApplicationSet git generator pinned to the branch being compared
against (`revision: main` when the PR targets `main`) is accepted. After merge,
that branch contains the change, so ArgoCD reading it does generate the new
Application. A revision pinning a tag or an unrelated branch is rejected,
because merging would not change the tree it resolves to.

## Rendering cost is proportional by design

An ApplicationSet generating N Applications produces N comparisons and 2N Helm
renders. That is the feature working, not unbounded work:

- ArgoCD really does deploy N Applications for that manifest
- a cap or budget would emit a diff that looks complete while silently omitting
  Applications, which is worse than a slow job because nothing in the output
  reveals the omission
- the same linear cost already exists for a PR touching N Application files,
  and there has never been a per-run render budget

Each comparison creates its temporary workspace inside the loop and removes it
in a deferred call on the same iteration, so temporary storage does not grow
with the number of generated Applications.

## ApplicationSet scope boundaries

Supported: `goTemplate: true` with the `list` generator and the `git`
generator's `directories`, for a `repoURL` naming the repository under
comparison.

Everything else — legacy fasttemplate substitution, other generator kinds, git
`files`/`values`/`pathParamPrefix`, generator-level `template` overrides — is
**skipped with the reason named at warning level**. It does not fail the run,
and other manifests in the same diff are still compared. This is the
established pattern for anything the tool cannot reproduce faithfully.

The template function map is deliberately identical to ArgoCD's: Sprig minus
`env`, `expandenv` and `getHostByName`. Matching ArgoCD is what makes the
rendered diff trustworthy, so do not propose removing further functions.
Allocation-heavy helpers such as `repeat` stay: `helm template` already runs on
pull-request-controlled charts with the same helpers available, so removing
them here would break fidelity without closing anything. Rendered output is
capped at 1 MiB, which bounds the document every later stage carries.

## The repository scan tolerates what it cannot use

Discovery walks the repository for ApplicationSets with git generators, because
adding or deleting a directory changes what a generator produces while leaving
the manifest untouched.

That scan visits every YAML in the repository, so a single unreadable file or
directory must not fail a run it has nothing to do with. Files that name
`kind: ApplicationSet` and still cannot be loaded are reported at warning
level; files that do not name the kind are skipped silently, because they are
simply not manifests this scan wants.

## Conventions

- New code must not add golangci-lint findings. Pre-existing ones are
  grandfathered and are not fixed opportunistically.
- Comment runs are capped at four lines, and comments carry the reasoning the
  code cannot state itself rather than restating it.
