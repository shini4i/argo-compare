# Usage

## Comparing a branch

The simplest scenario is to compare every changed Application file in the current branch against a target branch:

```bash
argo-compare branch <target-branch>
```

To restrict the comparison to a single file:

```bash
argo-compare branch <target-branch> --file <file-path>
```

## Output modes

By default, `argo-compare` prints only the diff of changed manifests. To include added or removed manifests in full:

```bash
# Print added manifests in addition to the diff
argo-compare branch <target-branch> --print-added-manifests

# Print removed manifests in addition to the diff
argo-compare branch <target-branch> --print-removed-manifests

# Print added, removed, and changed manifests
argo-compare branch <target-branch> --full-output
```

## External diff tool

Set `EXTERNAL_DIFF_TOOL` to pipe each file diff through a third-party tool such as [`diff-so-fancy`](https://github.com/so-fancy/diff-so-fancy):

```bash
EXTERNAL_DIFF_TOOL=diff-so-fancy argo-compare branch <target-branch>
```

## Helm labels

Helm-injected labels are stripped from rendered manifests before comparison because they add noise without changing the deployed state. Pass `--preserve-helm-labels` to keep them.

## Sensitive data

`argo-compare` masks the rendered contents of Kubernetes `Secret` manifests before they reach stdout logs, external diff tools, or merge request comments. Each secret entry is replaced with a deterministic hash placeholder, allowing reviewers to spot that a value changed without exposing the underlying secret material.

## Where to next

- [Anchored repositories](anchored-repositories.md) — for repos where the PR touches chart content instead of the Application YAML.
- [Manifest validation](manifest-validation.md) — schema-check rendered manifests with `kubeconform`.
- [GitLab integration](gitlab-integration.md) — post the diff as an MR comment.
- [Repository credentials](repository-credentials.md) — authenticate to private chart sources.
