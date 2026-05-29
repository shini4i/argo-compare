# `.argo-compare.yml` anchor examples

These fixtures illustrate the two layouts that the anchor flow supports.
Replace `example.com` and `group/repo` with the URLs that match your
environment; the files are not exercised by tests.

## Same-repo / all-in-one

The ArgoCD Application and the chart it consumes live in the same repository:

```
.
├── apps/
│   └── myapp.yaml                  # ArgoCD Application, spec.source.path: charts/myapp
└── charts/
    └── myapp/
        ├── .argo-compare.yml       # anchor pointing back at apps/myapp.yaml
        ├── Chart.yaml
        ├── values.yaml
        └── templates/
            └── deployment.yaml
```

See [`same-repo/`](./same-repo) for the file contents.

## Manifest-only repository

The ArgoCD Application lives in a different repo (an "apps" repo); this
repo just hosts the chart content the Application consumes:

```
.
└── charts/
    └── myapp/
        ├── .argo-compare.yml       # anchor pointing at the external apps repo
        ├── Chart.yaml
        ├── values.yaml
        └── templates/
            └── deployment.yaml
```

See [`cross-repo/`](./cross-repo) for the file contents.

## How to test against a fixture

From inside a clone of either repository layout, run:

```bash
argo-compare branch main
```

Argo Compare detects the changed file under `charts/myapp/`, walks up to
the nearest `.argo-compare.yml`, fetches the named Application (locally
or via a clone of the apps repo), and renders the chart twice — once
against the working tree (post-PR state) and once against the merge-base
(pre-PR state) — before producing the diff.
