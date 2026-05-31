# Sensitive data handling

`argo-compare` masks the rendered contents of Kubernetes `Secret` manifests before they reach stdout logs, external diff tools, or merge request comments. Each secret entry is replaced with a deterministic hash placeholder, allowing reviewers to spot that a value changed without exposing the underlying secret material.
