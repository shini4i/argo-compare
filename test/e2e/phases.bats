#!/usr/bin/env bats
# The phase suite, run against a lab that is already up (`task up`); `task e2e`
# wraps up -> this -> down. One test per phase, so CI gets a JUnit case each and
# --filter works for reruns. Once a phase fails the rest are SKIPPED and `task
# e2e` stops before `down`, leaving the cluster up for debugging.

bats_require_minimum_version 1.5.0

setup() {
  # BATS_FILE_TMPDIR is shared across tests in this file, so the marker survives
  # between them; each test body runs in its own subshell.
  [[ -f "${BATS_FILE_TMPDIR}/aborted" ]] && skip "an earlier phase failed"
  cd "${BATS_TEST_DIRNAME}" || return 1
}

teardown() {
  [[ "${BATS_TEST_COMPLETED:-}" == 1 ]] || touch "${BATS_FILE_TMPDIR}/aborted"
}

# smoke first: it proves the binary, the clone and real helm all work, so a
# parity failure afterwards cannot be blamed on the plumbing.
@test "smoke: the real binary renders a real chart and reports the diff" {
  run ./scripts/smoke.sh
  echo "$output"
  [ "$status" -eq 0 ]
}

# appset-parity is why the lab exists: argo-compare's expansion is checked
# against what the real ArgoCD controller generated from the same commit.
@test "appset-parity: expansion matches the real ArgoCD controller" {
  run ./scripts/appset-parity.sh
  echo "$output"
  [ "$status" -eq 0 ]
}

# render-parity last: it is the strictest, and reaching it means expansion and
# rendering are already known good.
@test "render-parity: the reported diff matches what ArgoCD renders" {
  run ./scripts/render-parity.sh
  echo "$output"
  [ "$status" -eq 0 ]
}
