#!/usr/bin/env bash
# The diff for a GENERATED Application, checked against ArgoCD's own rendering.
# render-parity covers a plain Application; this covers the ApplicationSet flow's
# output, reached through charts/generated's anchor, per generated Application.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib.sh
. "${here}/lib.sh"

command -v argocd >/dev/null || die "argocd is required on PATH (nix develop provides it)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

build_compare "$work"
clone_gitops "${work}/gitops"

main_sha="$(git -C "${work}/gitops" rev-parse "origin/${TARGET_BRANCH}")"
feature_sha="$(git -C "${work}/gitops" rev-parse "origin/${FEATURE_BRANCH}")"

argocd_login || die "could not log in to the lab's ArgoCD"

out="${work}/out.txt"
git -C "${work}/gitops" checkout -q "$FEATURE_BRANCH"
(cd "${work}/gitops" && "$COMPARE_BIN" branch "$TARGET_BRANCH") >"$out" 2>&1

echo "--- argo-compare output"
cat "$out"
echo "--- end"

assert_grep "$out" 'Anchored ApplicationSet' "reached the ApplicationSet through its anchor"

# Each generated Application is compared on its own, so one of them agreeing
# cannot cover for the other — and their renders differ by design.
for app in gen-dev gen-prod; do
  argocd_manifests "$app" "$main_sha" >"${work}/${app}-main.yaml" ||
    die "argocd could not render ${app} at ${main_sha}"
  argocd_manifests "$app" "$feature_sha" >"${work}/${app}-feature.yaml" ||
    die "argocd could not render ${app} at ${feature_sha}"

  if diff -q "${work}/${app}-main.yaml" "${work}/${app}-feature.yaml" >/dev/null; then
    die "ArgoCD renders ${app} identically at both commits, so this asserts nothing"
  fi

  argo_diff="${work}/${app}-argo.diff"
  diff_body "${work}/${app}-main.yaml" "${work}/${app}-feature.yaml" >"$argo_diff"

  compare_diff="${work}/${app}-compare.diff"
  section_diff "$out" "$app" >"$compare_diff"

  if [[ ! -s "$compare_diff" ]]; then
    bad "argo-compare reported no diff for ${app}"
    continue
  fi

  while read -r line; do
    [[ -n "$line" ]] || continue
    if grep -qxF -e "$line" "$compare_diff"; then
      ok "${app}: argo-compare reports ${line}"
    else
      bad "${app}: argo-compare is missing ArgoCD's change: ${line}"
    fi
  done <"$argo_diff"

  while read -r line; do
    [[ -n "$line" ]] || continue
    if grep -qxF -e "$line" "$argo_diff"; then
      ok "${app}: ArgoCD confirms ${line}"
    else
      bad "${app}: argo-compare reports a change ArgoCD does not: ${line}"
    fi
  done <"$compare_diff"
done

# Each element's banner carries its own name, so the two diffs are distinct and
# a run that attributed one Application's diff to the other fails above rather
# than passing on an identical change.
assert_grep "${work}/gen-dev-feature.yaml" 'banner: second-dev' "gen-dev renders its own element"
assert_grep "${work}/gen-prod-feature.yaml" 'banner: second-prod' "gen-prod renders its own element"

phase_end generated-parity
