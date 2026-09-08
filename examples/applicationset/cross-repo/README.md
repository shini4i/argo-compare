# Cross-repository ApplicationSet

Two repositories. `apps-repo/` holds the ApplicationSet and the per-cluster
files its git generator reads. `gitops-repo/` is the one `argo-compare` runs
in: it holds the chart and the per-cluster values the ApplicationSet renders.
The walkthrough is [Setting up ApplicationSets](../../../docs/applicationset-setup.md#2-applicationset-in-another-repository).

These files are a structural reference, not a runnable pair of repositories.
The chart has no templates and every URL is a placeholder: replace
`https://git.example.com/platform/apps.git` with the apps repository and
`https://git.example.com/platform/gitops.git` with `git remote get-url origin`
in the gitops repository.

## Layout

```
apps-repo/                          # anchored repository
├── appsets/platform.yaml           # git generator over clusters/*/config.yaml
└── clusters/
    ├── dev/config.yaml
    └── prod/config.yaml

gitops-repo/                        # compared repository
├── charts/platform/
│   ├── .argo-compare.yml           # repo: apps-repo, path: appsets/platform.yaml
│   ├── Chart.yaml
│   └── values.yaml
└── values/
    ├── .argo-compare.yml           # same anchor
    ├── dev.yaml
    └── prod.yaml
```

## What happens on a pull request to gitops-repo

A change under `charts/platform/` or `values/` walks up to the nearest
`.argo-compare.yml`, which fetches `appsets/platform.yaml` from the apps
repository at its `main` tip. The git generator reads that same clone, so both
legs generate `platform-dev` and `platform-prod`. Each is rendered from the
chart at `charts/platform` with `$values/values/<cluster>.yaml` on top, once
from the working tree and once from the merge-base, and the two renders are
diffed.

Adding `clusters/staging/` in the apps repository is not visible from here: the
generator's tree is read once. The anchored ApplicationSet's own Applications
are the only ones a change to the gitops repository can affect.

In CI, set `ARGO_COMPARE_GIT_TOKEN` so the anchor's `https://` clone
authenticates. See
[Authenticating cross-repo clones](../../../docs/anchored-repositories.md#authenticating-cross-repo-clones).
