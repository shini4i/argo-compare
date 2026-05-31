// Package testfixtures holds small Kubernetes manifest snippets that more
// than one test package needs to share. The goal is to avoid silent drift
// between identical-but-copy-pasted YAML blobs across test files.
//
// Only put fixtures here when the *same* fixture is used by tests in two or
// more packages. Single-package fixtures should stay inline in their _test.go
// file — moving them here costs locality without any dedup benefit.
package testfixtures

// HelmDeploymentWithManagedLabels is a minimal Helm-rendered Deployment that
// includes the standard `app.kubernetes.io/managed-by: Helm` and `helm.sh/chart`
// labels which helpers.StripHelmLabels is expected to remove.
const HelmDeploymentWithManagedLabels = `# for testing purpose we need only limited fields
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    app.kubernetes.io/instance: traefik-web
    app.kubernetes.io/managed-by: Helm
    app.kubernetes.io/name: traefik
    argocd.argoproj.io/instance: traefik
    helm.sh/chart: traefik-23.0.1
  name: traefik
  namespace: web
`

// HelmDeploymentStripped is the expected result of running
// helpers.StripHelmLabels on HelmDeploymentWithManagedLabels: the
// Helm-specific labels are gone, all other content is byte-identical.
const HelmDeploymentStripped = `# for testing purpose we need only limited fields
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    app.kubernetes.io/instance: traefik-web
    app.kubernetes.io/name: traefik
    argocd.argoproj.io/instance: traefik
  name: traefik
  namespace: web
`
