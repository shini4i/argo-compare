#!/usr/bin/env bash
# Drive the REAL argo-compare binary over the REAL Gitea repo with the REAL helm
# binary: the feature branch changes a path-based Application's inline values, so
# a correct run renders charts/demo on both branches and reports one changed file.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib.sh
. "${here}/lib.sh"

command -v helm >/dev/null || die "helm is required on PATH"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

build_compare "$work"
clone_gitops "${work}/gitops"
git -C "${work}/gitops" checkout -q "$FEATURE_BRANCH" || die "no ${FEATURE_BRANCH} branch"

out="${work}/out.txt"
(cd "${work}/gitops" && "$COMPARE_BIN" branch "$TARGET_BRANCH") >"$out" 2>&1
code=$?

echo "--- argo-compare output"
cat "$out"
echo "--- end"

if [[ "$code" -eq 0 ]]; then
  ok "exit 0"
else
  bad "exit ${code}, expected 0"
fi

assert_grep "$out" 'Processing changed application: \[apps/demo\.yaml\]' \
  "processed the changed Application"

# The rendered replica count is the assertion a stubbed helm cannot satisfy:
# seeing both sides proves real templating ran against both trees. Read from the
# demo section alone, so another fixture rendering the same chart cannot stand in
# for it and leave a broken standard flow passing.
section_lines "$out" 'apps/demo.yaml' >"${work}/demo.section"
section_diff "$out" 'apps/demo.yaml' >"${work}/demo.diff"
assert_grep "${work}/demo.diff" '^\+replicas: 3$' "rendered the branch's replica count"
assert_grep "${work}/demo.diff" '^-replicas: 1$' "rendered the target branch's replica count"

assert_grep "${work}/demo.section" '1 file would be changed' "reported one changed file"

phase_end smoke
