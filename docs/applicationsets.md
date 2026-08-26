# ApplicationSets

`argo-compare` expands a changed ApplicationSet manifest into the Applications
it generates, on your branch and on the target branch, then compares each
generated Application. No cluster connection is needed and the ArgoCD control
plane is never contacted.

## What is supported

| Feature | Status |
|---|---|
| `goTemplate: true` templating | supported |
| Legacy (fasttemplate) templating | not supported — the manifest is skipped |
| `list` generator | supported |
| `git`, `matrix`, `merge`, `clusters`, `scmProvider`, `pullRequest`, … | not supported — the manifest is skipped |
| `goTemplateOptions` | supported (`missingkey=default\|invalid\|zero\|error`) |
| Generator-level `template` overrides, `elementsYaml` | not supported — the manifest is skipped |

A skipped manifest is reported at warning level with the reason. It never
fails the run, and plain Application manifests in the same diff are still
compared.

## Why only `goTemplate: true`

ArgoCD has two substitution engines. The legacy one uses a flat parameter map,
so `{{path.basename}}` is a literal key rather than a nested lookup, and an
unresolved tag is copied into the output instead of raising an error. Matching
it would mean reproducing the flat-key layout of every generator, and an
unresolved tag would surface much later as an unrelated YAML or Helm failure.

The modern engine is Go `text/template` with a documented parameter shape, so
that is the one implemented. Add `goTemplate: true` to your ApplicationSet —
ArgoCD [recommends it](https://argo-cd.readthedocs.io/en/stable/operator-manual/applicationset/GoTemplate/)
and pairs it with `goTemplateOptions: ["missingkey=error"]`.

## Template functions

The [Sprig](https://masterminds.github.io/sprig/) library is available, plus
`normalize` for turning a value into a valid DNS name.

`env`, `expandenv` and `getHostByName` are deliberately absent, as they are in
ArgoCD. Rendering runs in CI next to repository credentials, and the diff may
be posted as a merge request comment — a template able to read the environment
would turn a pull request into a way to read those secrets.

ArgoCD's `slugify`, `toYaml`, `fromYaml` and `fromYamlArray` are not
implemented yet; a template using one fails with `function "…" not defined`.

## Added and removed Applications

A change to an ApplicationSet can change *which* Applications exist, not only
what they render. Applications generated on both branches are diffed normally.
An Application only your branch generates is reported with
`--print-added-manifests`, and one only the target branch still generates with
`--print-removed-manifests`; without those flags each is skipped with a log
line naming it.

## Example

```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: guestbook
  namespace: argocd
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=error"]
  generators:
    - list:
        elements:
          - cluster: engineering-dev
            revision: 1.0.0
          - cluster: engineering-prod
            revision: 1.1.0
  template:
    metadata:
      name: '{{ .cluster | normalize }}-guestbook'
    spec:
      project: default
      destination:
        server: https://kubernetes.default.svc
        namespace: guestbook
      source:
        repoURL: https://charts.example.com
        chart: guestbook
        targetRevision: '{{ .revision }}'
```

Bumping `revision` for one element produces a diff for that generated
Application only.

## Limits

A rendered Application is capped at 1 MiB. Real Applications are a few
kilobytes, so hitting the cap means a template is producing something
unintended, and expansion fails with a clear error rather than carrying the
oversized document into the diff.

## Application identity

Generated Applications are matched between branches by `metadata.name` alone.
An ApplicationSet whose template produces the same name twice is rejected with
a duplicate-name error, matching ArgoCD, which cannot create two Applications
with one name in the same namespace either.
