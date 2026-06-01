# Anchored repositories (path-based sources)

The default flow assumes the modified file in a PR _is_ the Application YAML. Some repositories split things up: the ArgoCD Application points at a chart directory inside the same repo (`spec.source.path`), or lives in a different repo that consumes a chart from this one. In both cases a PR typically touches chart values rather than the Application itself, so there is no `kind: Application` in the diff and the default flow finds nothing to compare.

To bridge that gap, drop a `.argo-compare.yml` file into any directory where chart content is changed. Argo Compare walks up from each changed file, picks the nearest enclosing anchor, and uses it to find the Application affected by the change:

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

## Supported layouts

Two layouts work out of the box:

- **Same-repo / all-in-one** — Application and chart live in the same repo. Drop a `.argo-compare.yml` next to the chart, omit `repo`, and point `path` at the Application YAML inside this repo.
- **Manifest-only repo** — chart content lives here, Application lives elsewhere. Drop a `.argo-compare.yml` next to the chart, set `repo` to the apps repo, and point `path` at the Application YAML inside it.

A worked example lives under [`examples/anchor/`](../examples/anchor).

## Limits in this version

- Only Helm sources are supported (`spec.source.chart` or `spec.source.path`). Kustomize / plain-YAML sources are not handled.
- For path-based sources, `spec.source.helm.valueFiles`, `spec.source.helm.values`, and `spec.source.helm.valuesObject` are all honoured and applied in the same order ArgoCD uses (valueFiles first, inline values on top). A chart without a `values.yaml` and an Application without inline values are both valid.
- For path-based sources, subchart dependencies declared in `Chart.yaml` are resolved automatically via `helm dependency build` before rendering. Credentials for HTTP(S) dependency repositories are sourced from the same `REPO_CREDS_*` chain used for top-level chart auth.
- `oci://` entries in `Chart.yaml` dependencies are not yet supported for automatic credential injection; helm surfaces its own auth error if the OCI subchart registry is private. Workaround: pre-authenticate via `helm registry login` before running argo-compare.
- For path-based Applications, `spec.source.repoURL` must identify the local repository — chart sources living in a _third_ repo are out of scope.
- A multi-source Application must use one kind consistently; mixing `chart` and `path` entries is rejected.
- The anchored Application is read at the configured branch tip; commit-pinning is not supported.
- Any failure to fetch, parse, or validate an anchored Application is a hard error — partial output would silently hide a broken configuration.

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
