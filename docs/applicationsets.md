# ApplicationSets

`argo-compare` expands a changed ApplicationSet manifest into the Applications
it generates, on your branch and on the target branch, then compares each
generated Application. No cluster connection is needed and the ArgoCD control
plane is never contacted.

A reference repository layout — which manifests go where, and which change
reaches which ApplicationSet — lives under
[`examples/applicationset/`](../examples/applicationset).

## What is supported

| Feature | Status |
|---|---|
| `goTemplate: true` templating | supported |
| Legacy (fasttemplate) templating | not supported — the manifest is skipped |
| `list` generator | supported |
| `git` generator, `directories` and `files` | supported for this repository — see below |
| git generator `values` | supported |
| `matrix`, `merge`, `clusters`, `scmProvider`, `pullRequest`, … | not supported — the manifest is skipped |
| `goTemplateOptions` | supported (`missingkey=default\|invalid\|zero\|error`) |
| Generator-level `template` overrides, `elementsYaml`, `pathParamPrefix` | not supported — the manifest is skipped |

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

## The git generator

A git generator reads either `directories` or `files`, never both in the same
generator. Declare two generators if you need both.

### directories

One Application per directory matching the patterns:

```yaml
generators:
  - git:
      repoURL: https://github.com/you/your-repo.git
      revision: HEAD
      directories:
        - path: clusters/*
        - path: clusters/donotdeploy
          exclude: true
```

Available parameters are `.path.path`, `.path.segments`, `.path.basename` and
`.path.basenameNormalized`. Exclude entries beat including ones no matter what
order they appear in, and a directory whose name starts with a dot never
generates an Application.

### files

One Application per matching file, with the file's own contents as parameters:

```yaml
generators:
  - git:
      repoURL: https://github.com/you/your-repo.git
      revision: HEAD
      files:
        - path: clusters/**/config.yaml
      values:
        label: '{{ .path.basename }}-addons'
```

A file holding `cluster: {name: dev}` is read as `{{ .cluster.name }}`. A file
holding a *list* generates one Application per element. JSON works too, since
it is a subset of YAML.

`**` recurses here, unlike in a `directories` pattern — ArgoCD matches files
with a different globber, and this follows it. For the same reason a dot
directory is *not* skipped for files: name it in the pattern and its files
generate Applications, where a `directories` pattern would never match one.

A malformed pattern — `clusters/[` — is rejected when the manifest is read,
rather than quietly matching nothing and comparing fewer Applications than you
asked for. The same applies to an `exclude` pattern, where a silent failure
would instead have generated Applications you meant to leave out.

A matched file must parse as a mapping, or as a sequence of them. Anything else
fails the run rather than being skipped, because a comparison that quietly
covers fewer Applications is worse than one that stops. Keep patterns narrow
enough not to sweep in chart templates. Files over 1 MiB are rejected, and a
symlink is never read as generator input.

Alongside the file's contents you get `.path.path`, `.path.segments`,
`.path.basename`, `.path.basenameNormalized`, `.path.filename` and
`.path.filenameNormalized`. Note that `.path.path` is the directory holding the
file, not the file itself — so a file at the repository root reports `.`, whose
normalized basename is the empty string. That is ArgoCD's shape, awkward as it
is; name such Applications from the file rather than the directory.

### values

Both generator shapes accept a `values` block, rendered against the parameters
above and exposed as `.values.<key>`. Entries cannot reference one another —
they are all rendered against the same starting parameters, as in ArgoCD.

### Changes are detected without editing the manifest

**Adding a directory, or editing a config file, is what usually changes the
output**, and that leaves the ApplicationSet manifest untouched. So
`argo-compare` also scans the repository for ApplicationSets with git
generators and compares any whose patterns cover a directory or file your
change touched.

That scan covers only this repository. To reach an ApplicationSet that lives in
another one, point a `.argo-compare.yml` at it — see
[Anchoring an ApplicationSet](anchored-repositories.md#anchoring-an-applicationset).

### repoURL must be this repository

A generator reading another repository is skipped with a warning. Both sides of
the comparison would read the same external tree, so the diff could only ever
reflect edits to the ApplicationSet itself — never the directory changes the
generator exists for.

### revision must be HEAD or the compared branch

Your branch and the target branch are the two revisions, so `revision: HEAD`
(or the name of the branch you are comparing against) needs no further
interpretation and is not consulted.

A revision pinning anything else — a tag, or an unrelated branch — is skipped
with a warning. ArgoCD would keep generating from that fixed tree, so a
directory your branch adds changes nothing it deploys, and reporting a new
Application would be a false positive.

### pathParamPrefix is not supported

It nests the path parameters under a prefix, so a template written for it would
render against the wrong shape. Such a manifest is skipped rather than rendered
incorrectly.

## Template functions

The [Sprig](https://masterminds.github.io/sprig/) library is available, plus the
five ArgoCD adds:

| Function | Does |
|---|---|
| `normalize` | turns a value into a valid DNS name |
| `slugify` | sanitizes and truncates — `slugify`, `slugify 23`, `slugify 50 false`, name last |
| `toYaml` | renders a value as YAML, without a trailing newline |
| `fromYaml` | parses a YAML or JSON mapping |
| `fromYamlArray` | parses a YAML or JSON sequence |

`env`, `expandenv` and `getHostByName` are deliberately absent, as they are in
ArgoCD. Rendering runs in CI next to repository credentials, and the diff may
be posted as a merge request comment — a template able to read the environment
would turn a pull request into a way to read those secrets.

`tpl`, which renders a string as a template of its own, is not implemented; a
template using it fails with `function "tpl" not defined`.

`spec.template` is decoded first and each field rendered afterwards, the same
way ArgoCD does it. A rendered value is therefore only ever a value: quotes,
newlines and indentation inside it cannot change how the manifest parses, so
`{{ toYaml .values }}` needs no `nindent` ceremony, and `nindent` means the
column you wrote rather than one derived from anything internal.

## Merge request comments

Every generated Application appears as a section of a single merge request note,
not one note each. See [GitLab integration](gitlab-integration.md).

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

Each rendered field is capped at 1 MiB. Real Application fields are far
smaller, so hitting the cap means a template is producing something unintended,
and expansion fails with a clear error rather than carrying the oversized value
into the diff.

## Application identity

Generated Applications are matched between branches by `metadata.name` alone.
An ApplicationSet whose template produces the same name twice is rejected with
a duplicate-name error, matching ArgoCD, which cannot create two Applications
with one name in the same namespace either.
