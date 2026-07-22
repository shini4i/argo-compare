# Anchored repositories (path-based sources)

The default flow assumes the modified file in a PR _is_ the Application YAML. Some repositories split things up: the ArgoCD Application points at a chart directory inside the same repo (`spec.source.path`), or lives in a different repo that consumes a chart from this one. In both cases a PR typically touches chart values rather than the Application itself, so there is no `kind: Application` in the diff and the default flow finds nothing to compare.

To bridge that gap, drop a `.argo-compare.yml` file into any directory that holds chart content. Argo Compare walks up from each changed file (excluding the anchor file itself), picks the nearest enclosing anchor, and uses it to find the Application affected by the change:

```yaml
# .argo-compare.yml — universal schema
application:
  # Optional. Omit when the Application lives in this same repo.
  # Prefer https:// so the same anchor works in CI (PAT auth — see below).
  repo: https://example.com/group/apps.git
  # Required. Path to the Application YAML inside that repo.
  path: cluster-state/myapp/myapp.yaml
  # Optional. Defaults to the remote's default branch.
  branch: main
```

Changes that only touch `.argo-compare.yml` — adding the marker during onboarding or editing it later — do not trigger a comparison on their own. A comparison runs only when the same PR also changes real chart content in the anchored directory.

## Supported layouts

Two layouts work out of the box:

- **Same-repo / all-in-one** — Application and chart live in the same repo. Drop a `.argo-compare.yml` next to the chart, omit `repo`, and point `path` at the Application YAML inside this repo.
- **Manifest-only repo** — chart content lives here, Application lives elsewhere. Drop a `.argo-compare.yml` next to the chart, set `repo` to the apps repo, and point `path` at the Application YAML inside it.

A worked example lives under [`examples/anchor/`](../examples/anchor).

## Limits in this version

- Only Helm sources are supported (`spec.source.chart` or `spec.source.path`). Kustomize / plain-YAML sources are not handled.
- For path-based sources, `spec.source.helm.valueFiles`, `spec.source.helm.values`, and `spec.source.helm.valuesObject` are all honoured and applied in the same order ArgoCD uses (valueFiles first, inline values on top). A chart without a `values.yaml` and an Application without inline values are both valid.
- For path-based sources, subchart dependencies declared in `Chart.yaml` are resolved automatically via `helm dependency build` before rendering. Credentials for HTTP(S) dependency repositories are sourced from the same `REPO_CREDS_*` chain used for top-level chart auth.
- For both path-based and registry-based sources, `spec.source.helm.parameters` is applied as `--set` / `--set-string` flags when rendering. `.argocd-source.yaml` and `.argocd-source-<appName>.yaml` files committed next to the chart are also read and merged in the same order ArgoCD uses — generic file first, app-specific file on top. This is how argo-watcher and Argo CD Image Updater record image tag bumps via the git write-back method; previously those bumps produced an empty diff.
- `oci://` entries in `Chart.yaml` dependencies are not yet supported for automatic credential injection; helm surfaces its own auth error if the OCI subchart registry is private. Workaround: pre-authenticate via `helm registry login` before running argo-compare.
- For path-based Applications, `spec.source.repoURL` must identify the local repository — chart sources living in a _third_ repo are out of scope.
- A multi-source Application must use one kind consistently; mixing `chart` and `path` entries is rejected.
- The anchored Application is read at the configured branch tip; commit-pinning is not supported.
- **Cross-repo anchors cannot see an unmerged Application change.** For a cross-repo anchor (`repo:` set), the chart is read from the pull request's working tree while the Application — including its `spec.source.helm.valueFiles` list — is read from the anchored repo's branch tip. If the same change set restructures the files the Application points at (for example, splitting one `values.yaml` into several), the two halves are out of sync: the working tree has the new layout, but the Application still references the old one. This is inherent — the corrected `valueFiles` list lives in a separate, not-yet-merged change in the anchored repo, which argo-compare cannot see from this repo's PR. When a referenced values file is missing from the working tree, argo-compare fails with an explicit error naming the mismatch rather than an opaque Helm error. **Workaround:** land the Application's `valueFiles` update in the anchored repo first (or in lockstep), so the branch tip and the chart layout agree. Same-repo anchors are unaffected — they read both the chart and the Application from the working tree.
- If the anchored chart directory does not exist in the target branch (it was added for the first time on the current branch), the Application is treated as new rather than failing: the comparison is skipped, or with `--print-added-manifests` the current branch's manifests render as all-added. This mirrors the standard flow's handling of a newly added Application file.
- Any other failure to fetch, parse, or validate an anchored Application is a hard error — partial output would silently hide a broken configuration.

## Configuration

- `--anchor-file <name>` (or `ARGO_COMPARE_ANCHOR_FILE`) overrides the anchor file name. Default: `.argo-compare.yml`. Pass an empty string to suppress discovery.

## Authenticating cross-repo clones

Same-repo anchors read from the local working tree — no credentials involved.

Cross-repo anchors clone the target repository over its `repo:` URL. There are two auth paths, picked at runtime:

- **Local development** — if no credentials are configured, `argo-compare` lets go-git fall back to its defaults: SSH agent + default keys for `ssh://` URLs, anonymous for `https://` URLs against public repos. This is the only mode that existed before the `ARGO_COMPARE_GIT_*` env vars were introduced; nothing changes for users who rely on it.
- **CI / unattended** — set `ARGO_COMPARE_GIT_TOKEN` (or pass `--git-token`). Every cross-repo clone is sent with HTTP Basic auth. If `ARGO_COMPARE_GIT_USERNAME` is not set it defaults to `x-access-token`, which works for GitHub PATs, GitLab PATs, and Gitea. Set `ARGO_COMPARE_GIT_USERNAME` explicitly only when a different username is required (e.g. `gitlab-ci-token` for GitLab `CI_JOB_TOKEN`, or your account username for Bitbucket app passwords). Only `https://` URLs in the anchor file pick up these credentials — `ssh://` URLs continue to use the SSH agent regardless.

The right username depends on the host (this table covers `https://` URLs only — `ssh://` URLs always use the SSH agent regardless of these env vars):

| Host       | `ARGO_COMPARE_GIT_USERNAME`               | `ARGO_COMPARE_GIT_TOKEN`              |
| ---------- | ----------------------------------------- | ------------------------------------- |
| GitHub     | omit (defaults to `x-access-token`)       | PAT (or `${{ secrets.GITHUB_TOKEN }}` in Actions) |
| GitLab     | omit for PAT; `gitlab-ci-token` for CI_JOB_TOKEN | PAT or `$CI_JOB_TOKEN`          |
| Gitea      | omit (defaults to `x-access-token`)       | PAT                                   |
| Bitbucket  | your Bitbucket account username           | App password                          |

`ARGO_COMPARE_GIT_*` is intentionally separate from `ARGO_COMPARE_GITLAB_TOKEN` even though both can sometimes hold the same value. The GitLab token authenticates the **REST API** (posting MR comments); the git token authenticates **git clone**. Different scopes, different lifetimes, sometimes different hosts. If you want one source of truth in GitLab CI, set both:

```yaml
# .gitlab-ci.yml
variables:
  ARGO_COMPARE_GITLAB_TOKEN: $CI_JOB_TOKEN     # for posting MR comments
  ARGO_COMPARE_GIT_USERNAME: gitlab-ci-token   # required when using CI_JOB_TOKEN for git clones
  ARGO_COMPARE_GIT_TOKEN: $CI_JOB_TOKEN        # for cloning cross-repo anchors
```

For Helm chart credentials (a separate mechanism), see [`repository-credentials.md`](repository-credentials.md).
