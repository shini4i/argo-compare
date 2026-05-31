# Anchored repositories (path-based sources)

The default flow assumes the modified file in a PR _is_ the Application YAML. Some repositories split things up: the ArgoCD Application points at a chart directory inside the same repo (`spec.source.path`), or lives in a different repo that consumes a chart from this one. In both cases a PR typically touches chart values rather than the Application itself, so there is no `kind: Application` in the diff and the default flow finds nothing to compare.

To bridge that gap, drop a `.argo-compare.yml` file into any directory where chart content is changed. Argo Compare walks up from each changed file, picks the nearest enclosing anchor, and uses it to find the Application affected by the change:

```yaml
# .argo-compare.yml — universal schema
application:
  # Optional. Omit when the Application lives in this same repo.
  repo: ssh://git@example.com/group/apps.git
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
- For path-based Applications, `spec.source.repoURL` must identify the local repository — chart sources living in a _third_ repo are out of scope.
- A multi-source Application must use one kind consistently; mixing `chart` and `path` entries is rejected.
- The anchored Application is read at the configured branch tip; commit-pinning is not supported.
- Cross-repo Application reads use your local Git environment (SSH agent, `~/.gitconfig`). The `REPO_CREDS_*` mechanism remains Helm-only.
- Any failure to fetch, parse, or validate an anchored Application is a hard error — partial output would silently hide a broken configuration.

## Configuration

- `--anchor-file <name>` (or `ARGO_COMPARE_ANCHOR_FILE`) overrides the anchor file name. Default: `.argo-compare.yml`. Pass an empty string to suppress discovery.
