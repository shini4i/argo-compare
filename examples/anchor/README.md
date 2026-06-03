# `.argo-compare.yml` anchor examples

Structural reference fixtures for the two repository layouts the anchor
flow supports. These are not runnable — the umbrella `Chart.yaml`
declares a dependency on `https://charts.example.com` which does not
resolve. Replace the placeholder URLs (`example.com`, `group/repo`)
with the values that match your environment before using these files
in a real repository.

## Same-repo / all-in-one

The ArgoCD Application and the chart it consumes live in the same
repository. See [`same-repo/`](./same-repo) for the files.

```
.
├── apps/
│   └── myapp.yaml                  # ArgoCD Application, spec.source.path: charts/myapp
└── charts/
    └── myapp/
        ├── .argo-compare.yml       # anchor pointing back at apps/myapp.yaml
        ├── Chart.yaml
        └── values.yaml
```

## Manifest-only repository

The ArgoCD Application lives in a different repo (an "apps" repo); this
repo just hosts the chart content the Application consumes. See
[`cross-repo/`](./cross-repo) for the files.

```
.
└── charts/
    └── myapp/
        ├── .argo-compare.yml       # anchor pointing at the external apps repo
        ├── Chart.yaml
        └── values.yaml
```

## How the anchor flow uses these files

When a PR touches a non-anchor file under `charts/myapp/`
(changes to `.argo-compare.yml` itself are skipped — the marker is not
chart content), argo-compare walks up to the nearest
`.argo-compare.yml`, fetches the named Application
(reading from the local working tree for same-repo, or via an
in-memory clone for cross-repo), and renders the chart twice — once
against the working tree (post-PR state) and once against the
merge-base (pre-PR state) — before producing the diff.
