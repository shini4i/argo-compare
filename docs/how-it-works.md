# How it works

1. `argo-compare` checks which Application files the source branch has modified since it diverged from the target branch (the merge-base is the baseline, so commits made only on the target branch after divergence are ignored).
2. It fetches the content of the changed Application files from the target branch.
3. It renders manifests using `helm template` against both source and target branch values.
4. It strips Helm-injected labels since they are not meaningful for the comparison (skip with `--preserve-helm-labels`).
5. Optionally, when `--validate-manifests` is enabled, all source-branch rendered manifests (not just changed ones) are validated against Kubernetes schemas via `kubeconform`. See [Manifest validation](manifest-validation.md).
6. Finally, it compares the rendered manifests from the source and target branches and prints the difference.

Repositories where the PR touches chart content instead of the Application YAML follow a different entry path; see [Anchored repositories](anchored-repositories.md).
