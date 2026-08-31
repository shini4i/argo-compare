#!/usr/bin/env bash
# The phase this lab exists for: argo-compare reimplements ArgoCD's ApplicationSet
# expansion, and nothing else in the suite checks that reimplementation against
# the real controller. The comparison itself is a Go test so it drives the
# production tree adapter rather than a shell reimplementation of it.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib.sh
. "${here}/lib.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

clone_gitops "${work}/gitops"

out="${work}/out.txt"
E2E_REPO_DIR="${work}/gitops" \
  E2E_FIXTURES_DIR="${E2E_DIR}/fixtures" \
  E2E_ORIGIN_URL="${ORIGIN_URL}" \
  E2E_BRANCH="${TARGET_BRANCH}" \
  KCTX="${KCTX}" \
  go test -tags e2e -run TestE2EApplicationSetParity -count=1 -v \
  "${E2E_ROOT}/internal/app/" 2>&1 | tee "$out"

assert_grep "$out" '^ok[[:space:]]' "expansion matches the ArgoCD controller"

# Each ApplicationSet is asserted separately, so one passing subtest cannot stand
# in for the others.
fixtures=(e2e-list e2e-git-dir e2e-git-file e2e-lifecycle e2e-funcs e2e-git-values)
for name in "${fixtures[@]}"; do
  assert_grep "$out" "^[[:space:]]+--- PASS: TestE2EApplicationSetParity/${name}" \
    "${name} parity"
done

# This list is a second copy of the Go test's; without a count, a fixture added
# there but not here would silently stop being asserted.
passed="$(grep -cE '^[[:space:]]+--- PASS: TestE2EApplicationSetParity/' "$out")"
if [[ "$passed" -eq "${#fixtures[@]}" ]]; then
  ok "every fixture the Go test covers is asserted here"
else
  bad "the Go test reported ${passed} subtest(s), this phase asserts ${#fixtures[@]}"
fi

phase_end appset-parity
