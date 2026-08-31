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

# The gating phase asserts on lines that must be ABSENT, so the negative
# assertion needs the same reported form as the positive one.
@test "assert_no_grep passes when the pattern is absent" {
  printf 'hello\n' >"${BATS_TEST_TMPDIR}/out.txt"
  run assert_no_grep "${BATS_TEST_TMPDIR}/out.txt" 'goodbye' "no goodbye"
  [ "$status" -eq 0 ]
  [ "$output" = "  OK   no goodbye" ]
}

@test "assert_no_grep reports a failure when the pattern is present" {
  printf 'hello\n' >"${BATS_TEST_TMPDIR}/out.txt"
  run assert_no_grep "${BATS_TEST_TMPDIR}/out.txt" 'hello' "no hello"
  [ "$output" = "  FAIL no hello" ]
}

@test "assert_no_grep reports a failure when the file is missing" {
  run assert_no_grep "${BATS_TEST_TMPDIR}/absent.txt" 'hello' "no hello"
  [[ "$output" == "  FAIL no hello (no such file: "*"/absent.txt)" ]]
}

# grep exits 2 on a pattern it cannot compile. Treating that as "absent" would
# turn a mistyped escape into a negative assertion that can never fail.
@test "assert_no_grep reports a failure when the pattern will not compile" {
  printf 'hello\n' >"${BATS_TEST_TMPDIR}/out.txt"
  run assert_no_grep "${BATS_TEST_TMPDIR}/out.txt" 'a[' "bad pattern"
  # grep also writes its own diagnostic to stderr, which `run` folds into $output.
  [[ "$output" == *"FAIL bad pattern (grep exit "* ]]
}

# phase_end reads only E2E_FAILS, so a bad() that printed but did not count would
# leave every phase reporting failures and still exiting 0.
@test "a failed assertion increments the phase's failure count" {
  printf 'hello\n' >"${BATS_TEST_TMPDIR}/out.txt"
  [ "$E2E_FAILS" -eq 0 ]
  assert_grep "${BATS_TEST_TMPDIR}/out.txt" 'goodbye' "absent" >/dev/null
  [ "$E2E_FAILS" -eq 1 ]
  assert_no_grep "${BATS_TEST_TMPDIR}/out.txt" 'hello' "present" >/dev/null
  [ "$E2E_FAILS" -eq 2 ]
}

@test "assert_grep passes when the pattern is present" {
  printf 'hello\n' >"${BATS_TEST_TMPDIR}/out.txt"
  run assert_grep "${BATS_TEST_TMPDIR}/out.txt" 'hello' "found hello"
  [ "$output" = "  OK   found hello" ]
}

@test "section_lines keeps a section's summary lines, not only its diff" {
  cat >"${BATS_TEST_TMPDIR}/out.txt" <<'EOF'
===> Processing changed application: [apps/demo.yaml]
The following 1 file would be changed:
-  replicas: 1
===> Processing anchored chart in [/tmp/x/charts/addon]
The following 1 file would be changed:
EOF
  run section_lines "${BATS_TEST_TMPDIR}/out.txt" 'apps/demo.yaml'
  [ "${#lines[@]}" -eq 2 ]
  [ "${lines[0]}" = "The following 1 file would be changed:" ]
  [ "${lines[1]}" = "-  replicas: 1" ]
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
