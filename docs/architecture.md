# Architecture

This document describes the code structure of `argo-compare` — package
responsibilities, dependency direction, and where the two entry flows live.
For the user-facing pipeline (what the tool *does*, step by step), see
[How it works](how-it-works.md).

## Package map

```
cmd/argo-compare/
├── main.go               # CLI entrypoint: wires dependencies, parses args
├── command/              # cobra command definitions; calls internal/app
└── utils/                # adapters: real implementations of internal/ports
                          # (CmdRunner, HelmChartsProcessor, Globber, ...)

internal/
├── app/                  # orchestrator: end-to-end comparison workflow
│                         # holds Config, App, the two entry flows, and the
│                         # comment/diff strategies
├── anchor/               # .argo-compare.yml schema + loader
├── comment/              # Poster interface
│   └── gitlab/           # GitLab MR comment adapter
├── helpers/              # env vars, Helm label stripping, retry, fs utils
├── models/               # ArgoCD Application and related YAML structs
├── ports/                # interface contracts the adapters in cmd/.../utils
│   └── portstest/        # shared no-op fakes for tests (NoopCmdRunner etc.)
├── sanitizer/            # KubernetesSecretMasker — redacts Secret data
│                         # before manifests are diffed
├── testfixtures/         # shared manifest snippets used across test packages
└── ui/                   # terminal color helpers
```

## Dependency direction

```
cmd/argo-compare/main.go
        │
        ▼
cmd/argo-compare/command          (cobra wiring)
        │
        ▼
internal/app  ──────────────►  internal/{anchor, models, sanitizer, comment, ui, helpers}
        │                                      │
        │                                      ▼
        └────────► internal/ports ◄────── cmd/argo-compare/utils
                       ▲                  (adapter implementations:
                       │                   RealCmdRunner, OsFileReader,
                       │                   RealHelmChartProcessor, ...)
                       │
                       └──────────────── internal/sanitizer
                                         (also implements a port)
```

Only `cmd/argo-compare/utils` (and a few specific packages like `sanitizer`)
implement the interfaces declared in `internal/ports`. Everything that needs
to shell out, touch the filesystem, glob, or render Helm goes through a port
— which keeps `internal/app` testable with the fakes in
`internal/ports/portstest`.

## Two entry flows

`internal/app` selects between two entry paths based on what the PR diff
contains:

1. **Standard flow** — the PR modifies ArgoCD Application YAML files
   directly. Driver code lives in `internal/app/app.go`,
   `application_fetcher.go`, `git.go`, `target.go`, `compare.go`.

2. **Anchor flow** — the PR modifies chart content (e.g. Helm values)
   rather than an Application YAML. `argo-compare` walks up to the
   nearest `.argo-compare.yml`, resolves the referenced Application, and
   renders the chart twice (working tree vs. merge-base). Driver code
   lives in `internal/app/anchor_discovery.go`, `anchor_flow.go`,
   `tree_materialize.go`. The anchor schema itself is in
   `internal/anchor`.

Both flows converge on the same comparison + comment publication path in
`internal/app/compare.go` and `comment_strategy.go`.

## Side-effects and where they live

| Side-effect            | Port (interface)                     | Default adapter                           |
|------------------------|--------------------------------------|-------------------------------------------|
| Shell commands         | `ports.CmdRunner`                    | `cmd/argo-compare/utils.RealCmdRunner`    |
| Filesystem reads       | `ports.FileReader`                   | `cmd/argo-compare/utils.OsFileReader`     |
| Helm template / pull   | `ports.HelmChartsProcessor`          | `cmd/argo-compare/utils.RealHelmChartProcessor` |
| Glob expansion         | `ports.Globber`                      | `cmd/argo-compare/utils.CustomGlobber`    |
| Manifest validation    | `ports.ManifestValidator`            | `internal/app` `KubeconformValidator` (opt-in) |
| Secret masking         | `ports.SensitiveDataMasker`          | `internal/sanitizer.KubernetesSecretMasker` |
| Credential resolution  | `ports.CredentialProvider`           | ECR provider + static `REPO_CREDS_*` fallback |
| Application fetching   | `ports.ApplicationFetcher`           | `internal/app.RealApplicationFetcher`     |
| Comment publishing     | `internal/comment.Poster`            | `internal/comment/gitlab` (when configured) |

Tests substitute the corresponding `portstest` fake or a hand-rolled stub
rather than mocking concrete types.
