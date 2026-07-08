# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.9.2] - 2026-07-08

### Fixed

- Anchored (path-based) Applications whose chart directory is added for the first time on the current branch no longer fail with `directory not found`. The chart is now treated as a new Application — the comparison is skipped by default, or rendered as all-added with `--print-added-manifests`.
- Helm chart templates (files under a chart's `templates/` directory) are no longer misparsed as ArgoCD Applications, which previously failed the compare job when charts live alongside cluster config in the same repository.

### Security

- Registry credentials are no longer passed to Helm as command-line arguments, keeping them out of the process argument list where they could be observed by other users on the same host.

[Unreleased]: https://github.com/shini4i/argo-compare/compare/v0.9.2...HEAD
[0.9.2]: https://github.com/shini4i/argo-compare/compare/v0.9.1...v0.9.2
