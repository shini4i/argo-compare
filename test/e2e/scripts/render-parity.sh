#!/usr/bin/env bash
# Validate the comparison RESULT: ArgoCD renders the addon chart at each branch's
# commit, those renders are diffed here, and argo-compare's reported diff must
# agree with it in both directions. The addon manifest is identical on both
# branches, so only chart content can differ. See README.md.
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
[[ "$main_sha" != "$feature_sha" ]] || die "both branches point at the same commit"

argocd_login || die "could not log in to the lab's ArgoCD"

argocd_manifests addon "$main_sha" >"${work}/argo-main.yaml" ||
  die "argocd could not render addon at ${main_sha}"
argocd_manifests addon "$feature_sha" >"${work}/argo-feature.yaml" ||
  die "argocd could not render addon at ${feature_sha}"

# Without this the phase would assert nothing, which is how a parity test rots.
if diff -q "${work}/argo-main.yaml" "${work}/argo-feature.yaml" >/dev/null; then
  die "ArgoCD renders both commits identically, so this phase would assert nothing"
fi

argo_diff="${work}/argo.diff"
diff_body "${work}/argo-main.yaml" "${work}/argo-feature.yaml" >"$argo_diff"

echo "--- ArgoCD's change set"
cat "$argo_diff"

out="${work}/out.txt"
git -C "${work}/gitops" checkout -q "$FEATURE_BRANCH"
(cd "${work}/gitops" && "$COMPARE_BIN" branch "$TARGET_BRANCH") >"$out" 2>&1

echo "--- argo-compare output"
cat "$out"
echo "--- end"

# argo-compare reports every Application in one stream, so the addon section is
# taken alone: the demo Application legitimately changes too.
compare_diff="${work}/compare.diff"
section_diff "$out" addon >"$compare_diff"

while read -r line; do
  [[ -n "$line" ]] || continue
  if grep -qxF -e "$line" "$compare_diff"; then
    ok "argo-compare reports ${line}"
  else
    bad "argo-compare is missing ArgoCD's change: ${line}"
  fi
done <"$argo_diff"

# The reverse direction, so argo-compare cannot invent a change either.
while read -r line; do
  [[ -n "$line" ]] || continue
  if grep -qxF -e "$line" "$argo_diff"; then
    ok "ArgoCD confirms ${line}"
  else
    bad "argo-compare reports a change ArgoCD does not: ${line}"
  fi
done <"$compare_diff"

phase_end render-parity
