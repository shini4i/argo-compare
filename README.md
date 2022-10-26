<div align="center">

# argo-compare
A tool for showing difference between Application in a different git branches

![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/shini4i/argo-compare)
![GitHub release (latest by date)](https://img.shields.io/github/v/release/shini4i/argo-compare)
[![Go Report Card](https://goreportcard.com/badge/github.com/shini4i/argo-compare)](https://goreportcard.com/report/github.com/shini4i/argo-compare)
![GitHub](https://img.shields.io/github/license/shini4i/argo-compare)

<img src="https://raw.githubusercontent.com/shini4i/assets/main/src/argo-compare/demo.png" alt="Showcase" height="441" width="620">
</div>

> :warning: Currently, this tool is in PoC mode and is error-prone.

## General information

This tool will show what would be changed in the manifests rendered by helm after changes to the specific Application are merged into the target branch.

### How it works
- First, this tool will check which files are changed compared to the files in the target branch.
- Then it will get the content of the changed Application files from the target branch.
- Then it will render manifests using the helm template using source and target branch values.
- As the last step, it will compare rendered manifest from the source and destination branches and print the difference.

## Current limitations
- Works only with Applications that are using helm repositories and helm values present in the Application yaml.
- Does not support password protected repositories.

## Roadmap
- [ ] Add support for Application using git as a source of helm chart
- [ ] Add support for providing credentials for password protected helm repositories
- [ ] Add support for posting diff as a comment to PR (GitHub)/MR(GitLab)
