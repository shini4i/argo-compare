<div align="center">

# Argo Compare

A comparison tool for displaying the differences between applications in different Git branches

![GitHub Workflow Status](https://img.shields.io/github/actions/workflow/status/shini4i/argo-compare/run-tests.yml?branch=main)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/shini4i/argo-compare)
![GitHub release (latest by date)](https://img.shields.io/github/v/release/shini4i/argo-compare)
[![codecov](https://codecov.io/gh/shini4i/argo-compare/branch/main/graph/badge.svg?token=48E1OZHLPY)](https://codecov.io/gh/shini4i/argo-compare)
[![Go Report Card](https://goreportcard.com/badge/github.com/shini4i/argo-compare)](https://goreportcard.com/report/github.com/shini4i/argo-compare)
![GitHub](https://img.shields.io/github/license/shini4i/argo-compare)

<img src="https://raw.githubusercontent.com/shini4i/assets/main/src/argo-compare/demo.png" alt="Showcase" height="441" width="620">

Example output of `argo-compare` with `diff-so-fancy`
</div>

## General information

This tool will show what would be changed in the manifests rendered by helm after changes to the specific Application
are merged into the target branch.

### How to install

Download the binary from the [Releases](https://github.com/shini4i/argo-compare/releases) page, or pull the Docker image:

```bash
docker pull ghcr.io/shini4i/argo-compare:<version>
```

### How to use

The simplest usage scenario is to compare all changed files in the current branch with the target branch:

```bash
argo-compare branch <target-branch>
```

If you want to compare only specific file, you can use the `--file` flag:

```bash
argo-compare branch <target-branch> --file <file-path>
```

By default, argo-compare will print only changed files content, but if this behavior is not desired, you can use one of the following flags:
```bash
# In addition to the changed files, it will print all added manifests
argo-compare branch <target-branch> --print-added-manifests
# In addition to the changed files, it will print all removed manifests
argo-compare branch <target-branch> --print-removed-manifests
# Print all changed, added and removed manifests
argo-compare branch <target-branch> --full-output
```

To use an external diff tool, you can set `EXTERNAL_DIFF_TOOL` environment variable. Each file diff will be passed in a pipe to the external tool.
```bash
EXTERNAL_DIFF_TOOL=diff-so-fancy argo-compare branch <target-branch>
```

Additionally, you can try this tool using docker container:
```bash
docker run -it --mount type=bind,source="$(pwd)",target=/apps --env EXTERNAL_DIFF_TOOL=diff-so-fancy --workdir /apps ghcr.io/shini4i/argo-compare:<version> branch <target-branch> --full-output
```

To post the comparison as a comment to a GitLab Merge Request, provide the GitLab provider and credentials either with flags or environment variables:

```bash
ARGO_COMPARE_COMMENT_PROVIDER=gitlab \
ARGO_COMPARE_GITLAB_URL=https://gitlab.com \
ARGO_COMPARE_GITLAB_TOKEN=$GITLAB_TOKEN \
ARGO_COMPARE_GITLAB_PROJECT_ID=12345 \
ARGO_COMPARE_GITLAB_MR_IID=10 \
argo-compare branch <target-branch>
```

Equivalent CLI flags are available:

```bash
argo-compare branch <target-branch> \
  --comment-provider gitlab \
  --gitlab-url https://gitlab.com \
  --gitlab-token "$GITLAB_TOKEN" \
  --gitlab-project-id 12345 \
  --gitlab-merge-request-iid 10
```

When running inside GitLab CI, most settings are detected automatically:

- `--comment-provider` defaults to `gitlab` when `GITLAB_CI` and `CI_MERGE_REQUEST_IID` are present.
- `--gitlab-url` falls back to `CI_SERVER_URL`.
- `--gitlab-project-id` falls back to `CI_PROJECT_ID`.
- `--gitlab-merge-request-iid` falls back to `CI_MERGE_REQUEST_IID`.
- `--gitlab-token` falls back to `CI_JOB_TOKEN` if no explicit token is provided (ensure the token has the necessary scope to post notes).

### Manifest validation

`argo-compare` can validate rendered manifests against Kubernetes OpenAPI schemas using [kubeconform](https://github.com/yannh/kubeconform). Validation runs after rendering, helping catch schema violations before deployment. Results are included in stdout output and (when configured) GitLab MR comments.

**Scope:** validation runs only against the source branch (the post-merge state — what will land on the target branch if the change is merged). All rendered manifests are validated, not just the ones that differ between branches — a value change can break a manifest the diff doesn't touch, and the validator's job is to confirm the whole result is deployable. The target branch is not validated; pre-existing breakage there is not the responsibility of the current change.

Validation is **opt-in**. The comparison always runs to completion (diff is printed and any configured MR comment is posted) — but if any resource fails schema validation, or the validator itself cannot run, `argo-compare` exits with a non-zero status so CI can gate the merge.

```bash
argo-compare branch <target-branch> --validate-manifests
```

Optional flags:

- `--kubeconform-path <path>` — Override the kubeconform binary location (defaults to `kubeconform` resolved via `PATH`).
- `--skip-validation-kinds <Kind1,Kind2>` — Comma-separated list of resource kinds to skip (useful for custom resources without published schemas, e.g. `ServiceMonitor,ArgoApplication`).
- `--schema-location <value>` — Extra kubeconform [`-schema-location`](https://github.com/yannh/kubeconform#overriding-schemas-location---air-gapped-environment) values appended after the built-in `default` registry. Repeat the flag (or pass a comma-separated list) to extend validation to Custom Resources. The most common case is pointing at the community [`datreeio/CRDs-catalog`](https://github.com/datreeio/CRDs-catalog) which ships JSON schemas for KEDA, Kyverno, and many other operators:
  ```
  --schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
  ```

Equivalent environment variables (CLI flags take precedence when both are set):

```bash
ARGO_COMPARE_VALIDATE_MANIFESTS=true \
ARGO_COMPARE_KUBECONFORM_PATH=/usr/local/bin/kubeconform \
ARGO_COMPARE_SKIP_VALIDATION_KINDS=ServiceMonitor,ArgoApplication \
ARGO_COMPARE_KUBECONFORM_SCHEMA_LOCATIONS='https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
argo-compare branch <target-branch>
```

`kubeconform` must be available in the runtime environment when validation is enabled. The published Docker image (`ghcr.io/shini4i/argo-compare`) bundles it; for standalone binary installs, install [kubeconform](https://github.com/yannh/kubeconform) separately.

### Sensitive data handling

`argo-compare` masks the rendered contents of Kubernetes `Secret` manifests before they reach stdout logs, external diff tools, or merge request comments. Each secret entry is replaced with a deterministic hash placeholder, allowing reviewers to spot that a value changed without exposing the underlying secret material.

#### Password Protected Repositories
Using password protected repositories is a bit more challenging. To make it work, we need to expose JSON as an environment variable.
The JSON should contain the following fields:

```json
{
  "url": "https://charts.example.com",
  "username": "username",
  "password": "password"
}
```
How to properly expose it depends on the specific use case.

A bash example:
```bash
export REPO_CREDS_EXAMPLE={\"url\":\"https://charts.example.com\",\"username\":\"username\",\"password\":\"password\"}
```

Where `EXAMPLE` is an identifier that is not used by the application.

Argo Compare will look for all `REPO_CREDS_*` environment variables and use them if `url` will match the `repoURL` from Application manifest.

#### OCI Registries

Argo Compare supports charts hosted in OCI registries. Following the ArgoCD convention for Helm charts, the `repoURL` field should contain the bare registry hostname without the `oci://` scheme prefix:

```yaml
source:
  chart: my-chart
  repoURL: registry-1.docker.io/randomcharts
  targetRevision: 15.9.0
```

For **public OCI registries** (e.g., `ghcr.io`), no additional configuration is required.

For **private OCI registries**, credentials can be provided via `REPO_CREDS_*` environment variables (same format as above), or resolved automatically in the case of AWS ECR.

#### AWS ECR

Charts hosted in AWS ECR are authenticated automatically using the standard AWS credential chain (environment variables, IRSA, instance profiles, shared config). No manual credential configuration is needed — Argo Compare detects ECR registry URLs, extracts the region, and calls `ecr:GetAuthorizationToken` to obtain a short-lived token.

Tokens are cached for the duration of the comparison run to avoid redundant API calls when multiple charts are hosted in the same registry.

If AWS credentials are not available (e.g., running locally without AWS access), ECR authentication is skipped gracefully — public ECR charts will still work, and private charts will produce a clear error from Helm.


## How it works

1) First, this tool checks which Application files the source branch has modified since it diverged from the target branch (the merge-base is the baseline, so commits made only on the target branch after divergence are ignored).
2) It will get the content of the changed Application files from the target branch.
3) It will render manifests using the helm template using source and target branch values.
4) It will get rid of helm related labels as they are not important for the comparison. (It can be skipped by providing `--preserve-helm-labels` flag)
5) Optionally, when `--validate-manifests` is enabled, all source-branch rendered manifests (not just changed ones) are validated against Kubernetes schemas via `kubeconform`.
6) As the last step, it will compare rendered manifest from the source and destination branches and print the
   difference.

## Anchored repositories (path-based sources)

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

Two layouts work out of the box:

- **Same-repo / all-in-one** — Application and chart live in the same repo. Drop a `.argo-compare.yml` next to the chart, omit `repo`, and point `path` at the Application YAML inside this repo.
- **Manifest-only repo** — chart content lives here, Application lives elsewhere. Drop a `.argo-compare.yml` next to the chart, set `repo` to the apps repo, and point `path` at the Application YAML inside it.

A worked example lives under [`examples/anchor/`](./examples/anchor).

### Limits in this version

- Only Helm sources are supported (`spec.source.chart` or `spec.source.path`). Kustomize / plain-YAML sources are not handled.
- For path-based Applications, `spec.source.repoURL` must identify the local repository — chart sources living in a _third_ repo are out of scope.
- A multi-source Application must use one kind consistently; mixing `chart` and `path` entries is rejected.
- The anchored Application is read at the configured branch tip; commit-pinning is not supported.
- Cross-repo Application reads use your local Git environment (SSH agent, `~/.gitconfig`). The `REPO_CREDS_*` mechanism remains Helm-only.
- Any failure to fetch, parse, or validate an anchored Application is a hard error — partial output would silently hide a broken configuration.

### Configuration

- `--anchor-file <name>` (or `ARGO_COMPARE_ANCHOR_FILE`) overrides the anchor file name. Default: `.argo-compare.yml`. Pass an empty string to suppress discovery.

## Current limitations

- The default change-detection flow looks for Application YAMLs in the diff. Repos that store chart content separately from their Application files should use [Anchored repositories](#anchored-repositories-path-based-sources) above.
- <s>Does not support password protected repositories.</s>

## Roadmap

- [x] Add support for Application using git as a source of helm chart
- [x] Add support for providing credentials for password protected helm repositories
- [x] Add support for OCI registries (including AWS ECR with automatic authentication)
- [x] Add support for posting diff as a comment to MR (GitLab)
- [ ] Add support for posting diff as a comment to PR (GitHub)
- [x] Add manifest validation via kubeconform

## Contributing
Pull requests are welcome. For major changes, please open an issue first to discuss what you would like to change.
