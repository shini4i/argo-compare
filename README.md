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

## About

`argo-compare` shows what would change in the helm-rendered manifests of an ArgoCD Application once a pull request is merged into the target branch. It renders both branches with `helm template`, strips Helm-injected noise, and prints the diff.

Optional features layer on top of the core flow — manifest schema validation, posting the diff as a Merge Request comment, anchored discovery for chart-only repos, and credential handling for private chart sources. See the [Documentation](#documentation) index below.

## Quick start

```bash
# Install via Docker
docker pull ghcr.io/shini4i/argo-compare:<version>

# Or download a binary from the Releases page, then:
argo-compare branch <target-branch>
```

See [Installation](docs/installation.md) and [Usage](docs/usage.md) for the full setup.

## Documentation

- [Installation](docs/installation.md) — binary downloads and Docker image.
- [Usage](docs/usage.md) — CLI flags, output modes, external diff tools.
- [How it works](docs/how-it-works.md) — the comparison pipeline.
- [Architecture](docs/architecture.md) — package map, dependency direction, entry flows.
- [Anchored repositories](docs/anchored-repositories.md) — path-based sources and chart-only repos via `.argo-compare.yml`.
- [Manifest validation](docs/manifest-validation.md) — schema validation with `kubeconform`.
- [Repository credentials](docs/repository-credentials.md) — private Helm repos, OCI registries, AWS ECR.
- [GitLab integration](docs/gitlab-integration.md) — posting the diff as a Merge Request comment.

## Current limitations

- The default change-detection flow looks for Application YAMLs in the diff. Repos that store chart content separately from their Application files should use [Anchored repositories](docs/anchored-repositories.md).

## Roadmap

- [x] Add support for Application using git as a source of helm chart
- [x] Add support for providing credentials for password protected helm repositories
- [x] Add support for OCI registries (including AWS ECR with automatic authentication)
- [x] Add support for posting diff as a comment to MR (GitLab)
- [ ] Add support for posting diff as a comment to PR (GitHub)
- [x] Add manifest validation via kubeconform

## Contributing

Pull requests are welcome. For major changes, please open an issue first to discuss what you would like to change.
