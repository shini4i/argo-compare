#!/usr/bin/env bats
# Offline unit tests for the text helpers in scripts/lib.sh. No cluster: these
# are pure functions, and the phase that uses them is a per-release gate, so a
# boundary bug in them would otherwise only surface there.

bats_require_minimum_version 1.5.0

setup() {
  cd "${BATS_TEST_DIRNAME}" || return 1
  # shellcheck source=./scripts/lib.sh
  . ./scripts/lib.sh
}

# normalize_diff is a filter, so each case pipes into it through this helper
# rather than `run bash -c`: a function sourced by setup() does not cross into a
# new shell, and the tests then pass on a command-not-found message instead.
normalized() {
  printf -- "$1" | normalize_diff
  return
}

@test "normalize_diff strips leading whitespace and sorts" {
  run normalized "+  b: 2\n-  a: 1\n"
  [ "$status" -eq 0 ]
  [ "${#lines[@]}" -eq 2 ]
  [ "${lines[0]}" = "-a: 1" ]
  [ "${lines[1]}" = "+b: 2" ]
}

# ArgoCD re-serialises through its object model, so the same value arrives
# quoted from helm and unquoted from ArgoCD.
@test "normalize_diff unquotes scalar values" {
  run normalized "+  message: \"world\"\n"
  [ "$status" -eq 0 ]
  [ "$output" = "+message: world" ]
}

@test "normalize_diff leaves an unquoted value alone" {
  run normalized "+message: world\n"
  [ "$status" -eq 0 ]
  [ "$output" = "+message: world" ]
}

@test "normalize_diff deduplicates identical lines" {
  run normalized "+a: 1\n+a: 1\n"
  [ "$status" -eq 0 ]
  [ "${#lines[@]}" -eq 1 ]
  [ "$output" = "+a: 1" ]
}

@test "section_diff takes only the named section" {
  cat >"${BATS_TEST_TMPDIR}/out.txt" <<'EOF'
===> Processing changed application: [apps/demo.yaml]
-  replicas: 1
+  replicas: 3
===> Processing anchored chart in [/tmp/x/charts/addon]
-  message: "hello"
+  message: "world"
EOF
  run section_diff "${BATS_TEST_TMPDIR}/out.txt" addon
  [ "$status" -eq 0 ]
  [ "${#lines[@]}" -eq 2 ]
  [ "${lines[0]}" = "-message: hello" ]
  [ "${lines[1]}" = "+message: world" ]
}

# The bug a mutation found: an unbounded section swallowed its successors.
@test "section_diff stops at the next section" {
  cat >"${BATS_TEST_TMPDIR}/out.txt" <<'EOF'
===> Processing anchored chart in [/tmp/x/charts/addon]
-  message: "hello"
===> Processing changed application: [apps/demo.yaml]
-  replicas: 1
EOF
  run section_diff "${BATS_TEST_TMPDIR}/out.txt" addon
  [ "${#lines[@]}" -eq 1 ]
  [ "${lines[0]}" = "-message: hello" ]
}

@test "section_diff returns nothing for a section that is absent" {
  cat >"${BATS_TEST_TMPDIR}/out.txt" <<'EOF'
===> Processing changed application: [apps/demo.yaml]
-  replicas: 1
EOF
  run section_diff "${BATS_TEST_TMPDIR}/out.txt" addon
  [ "$output" = "" ]
}

@test "section_diff ignores diff context and file headers" {
  cat >"${BATS_TEST_TMPDIR}/out.txt" <<'EOF'
===> Processing anchored chart in [/tmp/x/charts/addon]
--- /tmp/src/configmap.yaml
+++ /tmp/dst/configmap.yaml
@@ -6,4 +6,4 @@
   name: addon-config
-  message: "hello"
EOF
  run section_diff "${BATS_TEST_TMPDIR}/out.txt" addon
  [ "${#lines[@]}" -eq 1 ]
  [ "${lines[0]}" = "-message: hello" ]
}

@test "diff_body reports only what differs between two files" {
  printf 'a: 1\nb: 2\n' >"${BATS_TEST_TMPDIR}/one.yaml"
  printf 'a: 1\nb: 3\n' >"${BATS_TEST_TMPDIR}/two.yaml"

  run diff_body "${BATS_TEST_TMPDIR}/one.yaml" "${BATS_TEST_TMPDIR}/two.yaml"
  [ "${#lines[@]}" -eq 2 ]
  [ "${lines[0]}" = "-b: 2" ]
  [ "${lines[1]}" = "+b: 3" ]
}

@test "diff_body reports nothing for identical files" {
  printf 'a: 1\n' >"${BATS_TEST_TMPDIR}/one.yaml"
  printf 'a: 1\n' >"${BATS_TEST_TMPDIR}/two.yaml"

  run diff_body "${BATS_TEST_TMPDIR}/one.yaml" "${BATS_TEST_TMPDIR}/two.yaml"
  [ "$output" = "" ]
}
