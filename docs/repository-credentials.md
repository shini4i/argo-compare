# Repository credentials

Public Helm repositories work with no configuration. For private sources, `argo-compare` reads credentials from environment variables matched against the `repoURL` of each Application.

## Password-protected Helm repositories

Expose credentials as a JSON object in an environment variable named `REPO_CREDS_*`, where the suffix is any identifier you choose:

```json
{
  "url": "https://charts.example.com",
  "username": "username",
  "password": "password"
}
```

Bash example:

```bash
export REPO_CREDS_EXAMPLE='{"url":"https://charts.example.com","username":"username","password":"password"}'
```

`argo-compare` scans every `REPO_CREDS_*` variable and applies the entry whose `url` matches the `repoURL` of the Application being rendered.

## OCI registries

`argo-compare` supports charts hosted in OCI registries. Following the ArgoCD convention for Helm charts, the `repoURL` field contains the bare registry hostname without the `oci://` scheme prefix:

```yaml
source:
  chart: my-chart
  repoURL: registry-1.docker.io/randomcharts
  targetRevision: 15.9.0
```

- **Public OCI registries** (e.g. `ghcr.io`) — no additional configuration required.
- **Private OCI registries** — provide credentials via the same `REPO_CREDS_*` mechanism described above, or use the automatic AWS ECR flow below.

## AWS ECR

Charts hosted in AWS ECR are authenticated automatically using the standard AWS credential chain (environment variables, IRSA, instance profiles, shared config). No manual credential configuration is needed — `argo-compare` detects ECR registry URLs, extracts the region, and calls `ecr:GetAuthorizationToken` to obtain a short-lived token.

Tokens are cached for the duration of the comparison run to avoid redundant API calls when multiple charts are hosted in the same registry.

If AWS credentials are not available (e.g. running locally without AWS access), ECR authentication is skipped gracefully — public ECR charts still work, and private charts produce a clear error from Helm.
