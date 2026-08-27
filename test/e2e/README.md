# argo-compare end-to-end lab

A disposable lab that runs **real** ArgoCD and **real** Gitea on a single-node
[kind](https://kind.sigs.k8s.io/) cluster, and drives the **real** `argo-compare`
binary against them with the **real** `helm` binary. Nothing is mocked, which is
the point — once per release, not on every PR.

## Why it exists

The unit and integration suites drive a `stubHelmProcessor` that fabricates
manifests from a release name and chart version. `helm template` never runs
there, so nothing in that suite can tell you the rendering is right.

More importantly, `internal/appset` is a **reimplementation** of ArgoCD's
ApplicationSet controller: `goTemplate` rendering, the `list` / git-directory /
git-file generators, and ArgoCD's template function map. A reimplementation is
only as good as its parity with the original, and no amount of self-consistent
unit testing can establish that. This lab does, by running the real controller
alongside and comparing.

## Phases

Each is one bats test in `phases.bats`, in this order:

| Phase | What it proves |
|---|---|
| `smoke` | The real binary, over a real Gitea clone, renders a real chart with real helm and reports the diff. The assertion is the rendered `replicas` value on both sides — a stub cannot produce it. |
| `appset-parity` | argo-compare's expansion matches what the real ArgoCD ApplicationSet controller generated from the same manifest at the same commit, across all three supported generators. |
| `render-parity` | argo-compare's reported **diff** matches what ArgoCD itself renders, in both directions: every change ArgoCD sees appears in the diff, and the diff invents none. |
| `generated-parity` | the same, for each Application an **ApplicationSet** generates, reached through an anchor. Covers the ApplicationSet flow's own output, which `render-parity` does not. |

`appset-parity` runs its comparison as a Go test
(`internal/app/e2e_parity_test.go`, behind `//go:build e2e`) rather than a shell
driver, so it exercises the **production** `gitTree` adapter. A shell
reimplementation of tree listing would test the reimplementation, not the product.

It compares a projection of each generated Application — name, source
repoURL/path/chart/targetRevision, and the rendered `helm.releaseName` and
`helm.values` — because ArgoCD also stamps ownership, finalizers and status that
argo-compare never produces. Values are re-marshalled before comparison so the
result is about content, not the indentation each side's templating emitted.

## What render-parity can and cannot compare

`argocd app manifests <app> --revision <sha>` renders the chart **at that
revision using the Application spec stored in the cluster**. It therefore cannot
model a change to the Application manifest itself — asked for two revisions that
differ only in the manifest's inline values, ArgoCD returns identical output.

That is why the fixture has two Applications:

- `demo` changes its own manifest between branches. The `smoke` phase covers it,
  and ArgoCD cannot be used as an oracle for it.
- `addon` has an **identical manifest on both branches** and no inline values, so
  the only thing that can change its render is chart content — which is exactly
  what `--revision` varies. That makes the comparison two-directional.

`charts/addon/` carries a `.argo-compare.yml`, because a chart-only change with
no anchor and no manifest change is invisible to argo-compare by design. So this
phase also happens to be the strictest test of the anchor flow.

`generated-parity` applies the same reasoning one level further: `charts/generated`
is anchored to an **ApplicationSet**, so a change to that chart reaches
argo-compare through the anchor and is compared per generated Application. Each
element renders a banner carrying its own name, so the two diffs are distinct and
attributing one Application's diff to the other fails rather than passing on an
identical change.

The two renderers format YAML differently: ArgoCD parses rendered manifests into
its object model and re-serialises them, so helm's `message: "hello"` comes back
as `message: hello`. `normalize_diff` in `lib.sh` strips that quoting before the
sets are compared — the quoting differs without the meaning differing.

## The two repo URLs

`scripts/lib.sh` defines two URLs for one repository, and the difference is
load-bearing:

- `CLONE_URL` reaches Gitea on the host, via the NodePort in `kind-config.yaml`.
- `ORIGIN_URL` is the in-cluster DNS name, which is what ArgoCD's repo-server
  resolves and what the fixture manifests carry.

`clone_gitops` clones over `CLONE_URL` and then rewrites `origin` to
`ORIGIN_URL`. argo-compare checks that a git generator's `repoURL` identifies the
repository it is running in, so without the rewrite it would judge every fixture
generator as belonging to another repo and skip it — and the parity phase would
compare nothing. argo-compare only ever reads trees locally, so the rewritten
remote never has to be fetchable.

## Prerequisites

`kind`, `kubectl` and `helm` on `PATH`, with **podman or docker** as the kind
provider. `go`, `bats`, `shellcheck`, `jq`, `argocd` and `task` come from the devshell
(`nix develop`); the cluster tools deliberately do not, since the lab is a
per-release gate rather than part of the everyday shell.

The lab creates and deletes a kind cluster, which writes to your **kubeconfig**.
Point `KUBECONFIG` at a throwaway file first if your default one holds contexts
you care about.

## Usage

```sh
task e2e     # boot the lab, run every phase, tear it down
```

`task e2e` is `up` → `phases` → `down`. Once a phase fails the remaining ones are
skipped and `down` never runs, so the cluster is left up for debugging; a fully
green run tears it down. A per-phase JUnit report lands in `reports/`.

```sh
task up                  # boot the lab only (idempotent)
task phases              # re-run every phase against a lab already up
task smoke               # one phase directly
task appset-parity
task render-parity
task generated-parity
task lint                # shellcheck, no cluster needed
task down                # destroy the cluster
bats --filter parity phases.bats          # one phase by name
bats --filter-status failed phases.bats   # only what failed last run
```

## In CI

`.github/workflows/e2e.yml` is label-gated: add the `e2e` label to a pull request
to run it, or dispatch it manually. Re-run by removing and re-adding the label.
It is deliberately not on every PR — the run takes tens of minutes.

## Pinned versions

`Taskfile.yml` pins the ArgoCD and Gitea chart versions. The ArgoCD pin matters
most: a parity failure must mean argo-compare drifted, not that ArgoCD changed
under us. Bump it deliberately, and expect the parity phase to be the thing that
tells you the semantics moved.
