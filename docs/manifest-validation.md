# Manifest validation

`argo-compare` can validate rendered manifests against Kubernetes OpenAPI schemas using [kubeconform](https://github.com/yannh/kubeconform). Validation runs after rendering, helping catch schema violations before deployment. Results are included in stdout output and (when configured) GitLab MR comments.

## Scope

Validation runs only against the **source branch** (the post-merge state — what will land on the target branch if the change is merged). All rendered manifests are validated, not just the ones that differ between branches — a value change can break a manifest the diff doesn't touch, and the validator's job is to confirm the whole result is deployable. The target branch is not validated; pre-existing breakage there is not the responsibility of the current change.

## Behavior

Validation is **opt-in**. The comparison always runs to completion (diff is printed and any configured MR comment is posted) — but if any resource fails schema validation, or the validator itself cannot run, `argo-compare` exits with a non-zero status so CI can gate the merge.

```bash
argo-compare branch <target-branch> --validate-manifests
```

## Flags

- `--kubeconform-path <path>` — Override the kubeconform binary location (defaults to `kubeconform` resolved via `PATH`).
- `--skip-validation-kinds <Kind1,Kind2>` — Comma-separated list of resource kinds to skip (useful for custom resources without published schemas, e.g. `ServiceMonitor,ArgoApplication`).
- `--schema-location <value>` — Extra kubeconform [`-schema-location`](https://github.com/yannh/kubeconform#overriding-schemas-location---air-gapped-environment) values appended after the built-in `default` registry. Repeat the flag (or pass a comma-separated list) to extend validation to Custom Resources. The most common case is pointing at the community [`datreeio/CRDs-catalog`](https://github.com/datreeio/CRDs-catalog) which ships JSON schemas for KEDA, Kyverno, and many other operators:

  ```
  --schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
  ```

## Environment variables

CLI flags take precedence when both are set:

```bash
ARGO_COMPARE_VALIDATE_MANIFESTS=true \
ARGO_COMPARE_KUBECONFORM_PATH=/usr/local/bin/kubeconform \
ARGO_COMPARE_SKIP_VALIDATION_KINDS=ServiceMonitor,ArgoApplication \
ARGO_COMPARE_KUBECONFORM_SCHEMA_LOCATIONS='https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
argo-compare branch <target-branch>
```

## Requirements

`kubeconform` must be available in the runtime environment when validation is enabled. The published Docker image (`ghcr.io/shini4i/argo-compare`) bundles it; for standalone binary installs, install [kubeconform](https://github.com/yannh/kubeconform) separately.
