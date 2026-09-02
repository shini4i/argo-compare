# ApplicationSet examples

A complete repository layout showing what has to be in place for
`argo-compare` to expand an ApplicationSet and compare the Applications it
generates. The reference for the behaviour itself is
[`docs/applicationsets.md`](../../docs/applicationsets.md); this directory is
the shape a repository has to take for that behaviour to fire.

Two generators are supported, and there is one ApplicationSet here for each.
`list` writes its elements into the manifest and reads no tree at all. `git`
derives them from the repository — this example uses its `directories` shape,
one Application per directory; the `files` shape works the same way but takes
each matching file's contents as parameters instead
([docs](../../docs/applicationsets.md#files)).

These files are a structural reference, not a runnable repository. The chart
directories hold only the metadata that makes them charts — bring your own
templates — and the `repoURL` values are placeholders.

## Layout

```
.
├── apps/
│   ├── by-directory-appset.yaml   # git generator, directories + values
│   └── list-appset.yaml           # list generator
├── charts/
│   ├── demo/                      # rendered by by-directory-appset
│   │   ├── .argo-compare.yml      # anchor -> apps/by-directory-appset.yaml
│   │   ├── Chart.yaml
│   │   └── values.yaml
│   └── generated/                 # rendered by list-appset
│       ├── .argo-compare.yml      # anchor -> apps/list-appset.yaml
│       ├── Chart.yaml
│       └── values.yaml
└── by-directory/                  # one directory here, one Application
    ├── app1/config.yaml
    └── app2/config.yaml
```

`apps/` is not a magic name — an ApplicationSet is picked up wherever it lives.
The extension does matter, and it differs by path: a manifest you edit is taken
from the diff only when it is named `*.yaml`, while the repository scan that
catches directory and file changes reads both `.yaml` and `.yml`, skips dot
directories, and skips files over 1 MiB. Name manifests `*.yaml` and the
distinction never bites.

## What has to be true

Three things gate expansion. Miss one and a manifest found in the diff is
skipped with a warning rather than compared. The repository scan is quieter
about some misses than others: a manifest it cannot parse at all — gate 1, or
any unsupported generator field — is reported as an unreadable manifest at
warning level, while one disqualified by its `repoURL` or `revision` (gates 2
and 3) is dropped silently. An *anchored* manifest is different again: an anchor
is an explicit pointer, so a gate it misses fails the run.

1. **`goTemplate: true`** — the legacy fasttemplate engine is not implemented.
   Pair it with `goTemplateOptions: ["missingkey=error"]`.
2. **A git generator's `repoURL` is this repository.** The two branches being
   compared are the two trees the generator reads; one pointing elsewhere would
   read the same external tree twice.
3. **A git generator's `revision` is `HEAD` or the branch you compare against.**
   A tag or an unrelated branch means ArgoCD keeps generating from a fixed tree,
   so a directory your branch adds changes nothing.

Gates 2 and 3 do not apply to a `list` generator, which reads no repository.

The template's `spec.source.repoURL` is a fourth thing to get right, though it
is not checked the same way. A `path`-based source is always rendered from the
local working tree at `spec.source.path`, whatever `repoURL` says — so in the
changed-manifest flow a foreign value is not skipped or warned about, it
silently renders this repository's content under another repository's name.
Under an anchor it *is* checked: a generated Application whose path source
belongs to another repository, or whose source is a registry chart, is dropped
with a log line naming the reason, and the run fails if that leaves nothing to
compare. Both example charts are anchored, so a registry chart in their
templates would fail rather than be ignored.

Every `repoURL` here reads `https://github.com/you/your-gitops-repo.git`.
Replace them with your own origin URL — the value `git remote get-url origin`
prints — or nothing will match.

## What triggers a comparison

Three separate paths reach the ApplicationSet flow. Knowing which one applies
to your change is usually the missing piece:

| Your change | How it is found |
|---|---|
| `apps/list-appset.yaml` edited | It is a changed file in the diff — the ordinary flow. |
| anything under `by-directory/app3/` added or deleted | Repository scan: a git generator whose `directories` pattern covers a touched directory. |
| `charts/demo/values.yaml` edited | The `.argo-compare.yml` anchor in that directory. |

The last row is the one that surprises people. A chart-only change touches no
directory a generator matches and does not touch the manifest, so without an
anchor nothing routes it to the ApplicationSet and the change goes uncompared.
That is why both charts here carry one — and for the `list` generator, which
has no patterns at all, an anchor is the *only* way a chart change is ever
compared. An anchor names exactly one path, so a chart shared by two
ApplicationSets can only be routed to one of them; give each ApplicationSet its
own chart directory, as this example does.

An anchor also works across repositories, which is the only way to reach an
ApplicationSet that does not live in the repository being compared. See
[`docs/anchored-repositories.md`](../../docs/anchored-repositories.md) and
[`examples/anchor/`](../anchor).

## Trying it out

Adopt the layout in a repository of your own — real chart templates included,
`repoURL` pointing at that repository's origin — and the changes below are the
ones worth running first, one per discovery path:

```bash
# A new directory: found by the repository scan, adds dir-app3
mkdir -p by-directory/app3 && printf 'app: app3\n' > by-directory/app3/config.yaml
git add by-directory/app3 && git commit -m 'add app3'
argo-compare branch main --print-added-manifests

# A new list element: found as a changed file, adds list-app3
vim apps/list-appset.yaml     # append an element
git commit -am 'add app3 to the list'
argo-compare branch main --print-added-manifests

# A chart-only change: found through the anchor, diffs both Applications
vim charts/demo/values.yaml   # bump a value
git commit -am 'bump demo values'
argo-compare branch main
```

The first two need `--print-added-manifests` to print anything, because an
Application only one branch generates has nothing to be diffed against.

## Unsupported generators

`matrix`, `merge`, `clusters`, `scmProvider` and `pullRequest` need cluster or
forge access this tool deliberately does not have. A manifest using one is
skipped with a warning naming the reason.
