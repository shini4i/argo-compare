# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- A `.argo-compare.yml` anchor may point at an ApplicationSet, not only an Application. Each Application it generates is compared in turn, added and removed ones included. Only the generated Applications rendering a chart from this repository are compared; a registry chart or a source in another repository is skipped with the reason named. This is the only way to reach an ApplicationSet stored in a different repository, which the repository scan cannot see. See [Anchored repositories](docs/anchored-repositories.md).
- The ApplicationSet template functions ArgoCD adds on top of Sprig: `slugify`, `toYaml`, `fromYaml` and `fromYamlArray`, alongside the existing `normalize`. A manifest using one no longer fails with `function "…" not defined`.
- ApplicationSet templates are now rendered field by field, the way ArgoCD does it, instead of over the manifest as a whole. A rendered value can therefore contain quotes, newlines or indentation without altering how the Application parses; `toYaml` needs no `nindent` ceremony, and `nindent` means the column you wrote.
- ApplicationSet git **file** generator. One Application per file matching the generator's patterns, with the file's own contents as parameters, plus `.path.filename` and `.path.filenameNormalized`. A file holding a list generates one Application per element, and JSON files work as well as YAML. File patterns match with `doublestar`, so `**` recurses — unlike a `directories` pattern. Editing a config file is detected on its own, without the manifest changing.
- The git generator's `values` block, rendered against the path and file parameters and exposed as `.values.<key>`.
- ApplicationSet git directory generator. One Application is generated per directory matching the generator's patterns, with `.path.path`, `.path.segments`, `.path.basename` and `.path.basenameNormalized` available to the template. Adding or deleting a directory is detected on its own: `argo-compare` finds the ApplicationSets whose patterns cover a changed directory, so the manifest itself does not have to be edited. Generators reading another repository are skipped with a warning, as are `files` entries.
- ApplicationSet support. A changed ApplicationSet manifest is expanded into the Applications it generates on both branches, and each generated Application is compared individually, so an Application the change adds or removes is reported as such (gated by `--print-added-manifests` / `--print-removed-manifests`). Covers manifests using `goTemplate: true` with the `list` generator; every other generator and legacy fasttemplate substitution are skipped with a warning naming the reason. See [ApplicationSets](docs/applicationsets.md).

### Changed

- Manifests skipped for an unsupported configuration now log why they were skipped, instead of only naming the file.
- Cross-repo anchored Applications now fail with a clear, actionable error when the pull request restructures a chart's values files (for example splitting one `values.yaml` into several) but the Application — read from the anchored repo's branch tip — still references the old layout. Previously this surfaced as an opaque `helm template` "no such file" error. See `docs/anchored-repositories.md` for the workaround.

### Fixed

- Several chart directories anchored to the same Application are now compared once rather than once per anchor, which previously repeated the whole diff and posted a duplicate merge request comment for it.

## [0.9.2] - 2026-07-08

### Fixed

- Anchored (path-based) Applications whose chart directory is added for the first time on the current branch no longer fail with `directory not found`. The chart is now treated as a new Application — the comparison is skipped by default, or rendered as all-added with `--print-added-manifests`.
- Helm chart templates (files under a chart's `templates/` directory) are no longer misparsed as ArgoCD Applications, which previously failed the compare job when charts live alongside cluster config in the same repository.

### Security

- Registry credentials are no longer passed to Helm as command-line arguments, keeping them out of the process argument list where they could be observed by other users on the same host.
- Updated `go-git` to 5.19.2, which fixes arbitrary file read and write through symbolic link resolution (CVE-2026-71556). `argo-compare` uses `go-git` to read repository trees.
- Updated `golang.org/x/text` and `golang.org/x/net`, fixing denial of service on invalid UTF-8 input and on invalid DNS record parsing.
- Builds now use the Go 1.26.7 toolchain, picking up the standard library fixes released in 1.26.6.

[Unreleased]: https://github.com/shini4i/argo-compare/compare/v0.9.2...HEAD
[0.9.2]: https://github.com/shini4i/argo-compare/compare/v0.9.1...v0.9.2
