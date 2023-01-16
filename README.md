<div align="center">

# Argo Compare

A tool for showing difference between Application in a different git branches

![GitHub Workflow Status](https://img.shields.io/github/actions/workflow/status/shini4i/argo-compare/run-tests.yml?branch=main)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/shini4i/argo-compare)
![GitHub release (latest by date)](https://img.shields.io/github/v/release/shini4i/argo-compare)
[![Go Report Card](https://goreportcard.com/badge/github.com/shini4i/argo-compare)](https://goreportcard.com/report/github.com/shini4i/argo-compare)
![GitHub](https://img.shields.io/github/license/shini4i/argo-compare)

<img src="https://raw.githubusercontent.com/shini4i/assets/main/src/argo-compare/demo.png" alt="Showcase" height="441" width="620">
</div>

## General information

This tool will show what would be changed in the manifests rendered by helm after changes to the specific Application
are merged into the target branch.

### How to install

The binary can be installed using homebrew:

```bash
brew install shini4i/tap/argo-compare
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

If you want to use a custom diff tool, you can use the following approach:

```bash
ARGO_COMPARE_DIFF_COMMAND="/usr/bin/diff %s %s" argo-compare branch <target-branch>
```

Additionally, you can try it with docker:
```bash
docker run -it --mount type=bind,source="$(pwd)",target=/apps ghcr.io/shini4i/argo-compare:<version> branch <target-branch>
```

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


### How it works

1) First, this tool will check which files are changed compared to the files in the target branch.
2) It will get the content of the changed Application files from the target branch.
3) It will render manifests using the helm template using source and target branch values.
4) It will get rid of helm related labels as they are not important for the comparison. (It can be skipped by providing `--preserve-helm-labels` flag)
5) As the last step, it will compare rendered manifest from the source and destination branches and print the
   difference.

## Current limitations

- Works only with Applications that are using helm repositories and helm values present in the Application yaml.
- Does not support password protected repositories.

## Roadmap

- [ ] Add support for Application using git as a source of helm chart
- [x] Add support for providing credentials for password protected helm repositories
- [ ] Add support for posting diff as a comment to PR (GitHub)/MR(GitLab)
