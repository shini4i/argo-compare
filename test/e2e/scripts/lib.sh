#!/usr/bin/env bash
# Shared helpers for the argo-compare e2e lab. Source it, never run it. Sets no
# shell options on purpose: sourcing must not change the caller's mode.
# shellcheck disable=SC2034  # a library defines variables for its callers

E2E_SCRIPTS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${E2E_SCRIPTS}/.." && pwd)"
E2E_ROOT="$(cd "${E2E_DIR}/../.." && pwd)"

KCTX="${KCTX:-kind-ac-e2e}"
NS_ARGOCD="${NS_ARGOCD:-argocd}"
NS_GITEA="${NS_GITEA:-gitea}"

# Fixed NodePort from kind-config.yaml, so the clone URL survives a pod restart.
GITEA_URL="${GITEA_URL:-http://localhost:30300}"
GITEA_API="${GITEA_URL}/api/v1"
GITEA_ADMIN="${GITEA_ADMIN:-gitea_admin}"
GITEA_PW="${GITEA_PW:-gitea_admin_pw1}"
GITEA_ORG="${GITEA_ORG:-e2e}"
GITEA_REPO="${GITEA_REPO:-gitops}"

# Two URLs for one repo, and the difference is load-bearing: CLONE_URL is
# host-reachable, ORIGIN_URL is what ArgoCD resolves in-cluster and what the
# manifests carry. See README.md.
CLONE_URL="${CLONE_URL:-http://${GITEA_ADMIN}:${GITEA_PW}@localhost:30300/${GITEA_ORG}/${GITEA_REPO}.git}"
ORIGIN_URL="${ORIGIN_URL:-http://gitea-http.${NS_GITEA}.svc.cluster.local:3000/${GITEA_ORG}/${GITEA_REPO}.git}"

TARGET_BRANCH="${TARGET_BRANCH:-main}"
FEATURE_BRANCH="${FEATURE_BRANCH:-feature}"

kc() {
  kubectl --context "$KCTX" "$@"
  return
}

E2E_FAILS=0

ok() { echo "  OK   $*"; return; }
bad() { echo "  FAIL $*"; E2E_FAILS=$((E2E_FAILS + 1)); return; }
note() { echo "  NOTE $*"; return; }

# assert_grep <file> <pattern> <message>: one reported grep assertion. An
# if/then/else rather than `grep && ok || bad`, where a failing ok() would also
# run bad() and report a passing assertion as failed.
assert_grep() {
  local file="$1" pattern="$2" message="$3"
  if grep -qE "$pattern" "$file"; then
    ok "$message"
  else
    bad "$message"
  fi
  return
}

# assert_no_grep <file> <pattern> <message>: the negative form, for a line that
# must NOT appear — a flag that suppressed output, or a skip that did not happen.
# Only grep's exit 1 counts as absent: exit 2 means it could not answer (missing
# file, malformed pattern) and would otherwise report a silent pass.
assert_no_grep() {
  local file="$1" pattern="$2" message="$3" code=0
  if [[ ! -f "$file" ]]; then
    bad "${message} (no such file: ${file})"
    return
  fi
  grep -qE "$pattern" "$file" || code=$?
  case "$code" in
    0) bad "$message" ;;
    1) ok "$message" ;;
    *) bad "${message} (grep exit ${code}; check the pattern)" ;;
  esac
  return
}

# phase_end <PHASE>: print the verdict, exit non-zero if any bad() fired.
phase_end() {
  local name="$1" code=0
  if [[ "$E2E_FAILS" -eq 0 ]]; then
    echo "${name}: PASS"
  else
    echo "${name}: FAIL (${E2E_FAILS} failed assertion(s))"
    code=1
  fi
  exit "$code"
  # Unreachable: SonarCloud wants a return, shellcheck calls it dead.
  # shellcheck disable=SC2317
  return
}

# die <message...>: abort on a precondition, as opposed to a failed assertion.
# shellcheck disable=SC2317
die() {
  echo "FAIL: $*" >&2
  exit 1
  return
}

# retry <attempts> <delay> <cmd...>: run cmd until it succeeds. Locals are
# underscore-prefixed because cmd runs in THIS shell and may hold the same names.
retry() {
  local _attempts="$1" _delay="$2" _i
  shift 2
  for ((_i = 1; _i <= _attempts; _i++)); do
    "$@" && return 0
    [[ "$_i" -lt "$_attempts" ]] && sleep "$_delay"
  done
  return 1
}

gitea_ready() {
  curl -sf -m 3 -u "${GITEA_ADMIN}:${GITEA_PW}" "${GITEA_API}/version" >/dev/null 2>&1
  return
}

# gapi <curl-args...>: an authenticated Gitea API call.
gapi() {
  curl -sf -m 10 -u "${GITEA_ADMIN}:${GITEA_PW}" -H 'Content-Type: application/json' "$@"
  return
}

# clone_gitops <dir>: clone the seeded repo, pointing origin at the in-cluster
# URL. argo-compare reads trees locally and never fetches, so the rewritten
# remote only has to match what the manifests name.
clone_gitops() {
  local dir="$1"
  git clone -q "$CLONE_URL" "$dir" || die "gitops clone failed"
  git -C "$dir" remote set-url origin "$ORIGIN_URL"
  git -C "$dir" fetch -q "$CLONE_URL" "+refs/heads/*:refs/remotes/origin/*" ||
    die "gitops fetch failed"
  return
}

# build_compare <dir>: build the real argo-compare binary, setting COMPARE_BIN.
build_compare() {
  local dir="$1"
  COMPARE_BIN="${dir}/argo-compare"
  (cd "$E2E_ROOT" && go build -o "$COMPARE_BIN" ./cmd/argo-compare) ||
    die "argo-compare build failed"
  return
}

ARGOCD_URL="${ARGOCD_URL:-localhost:30081}"

# argocd_login: authenticate the CLI against the lab's ArgoCD. Plaintext because
# the lab server runs with server.insecure and is published on loopback only.
# Retried: on a freshly booted lab the API answers before it serves sessions,
# and a one-shot login would fail the phase for startup timing.
argocd_login() {
  local pw
  pw="$(kc -n "$NS_ARGOCD" get secret argocd-initial-admin-secret \
    -o jsonpath='{.data.password}' | base64 -d)"
  [[ -n "$pw" ]] || return 1
  retry 30 2 argocd login "$ARGOCD_URL" --username admin --password "$pw" \
    --plaintext --grpc-web
  return
}

# argocd_manifests <app> <revision>: what ArgoCD itself renders for <app> at
# <revision>. This varies chart content only — the Application spec comes from
# the cluster — so it cannot model a change to the manifest itself. Retried
# because the server may not have observed a just-applied Application yet.
argocd_manifests() {
  local app="$1" revision="$2" out
  out="$(mktemp)"
  if retry 30 2 argocd_manifests_once "$app" "$revision" "$out"; then
    cat "$out"
    rm -f "$out"
    return 0
  fi
  rm -f "$out"
  return 1
}

# argocd_manifests_once: one render attempt, written to <file> so a partial
# failure never reaches the caller as if it were the full output.
argocd_manifests_once() {
  local app="$1" revision="$2" file="$3"
  argocd app manifests "$app" --revision "$revision" \
    --plaintext --grpc-web >"$file" 2>/dev/null || return 1
  [[ -s "$file" ]]
  return
}

# normalize_diff: read changed diff lines, emit them comparable across the two
# renderers. ArgoCD parses rendered manifests into its object model and
# re-serialises them, so `message: "hello"` from helm comes back as
# `message: hello`; the quoting differs without the meaning differing.
normalize_diff() {
  sed -E 's/^([+-])[[:space:]]*/\1/; s/^([+-][^:]*): "(.*)"$/\1: \2/' | sort -u
  return
}

# diff_body <a> <b>: the changed lines of a unified diff, as a comparable set.
diff_body() {
  local a="$1" b="$2"
  diff -u "$a" "$b" | grep -E '^[+-][^+-]' | normalize_diff
  return
}

# section_lines <output> <marker>: every line of one section, diff lines
# included. section_diff keeps only the +/- lines, so an assertion about a
# summary line ("1 file would be changed") needs this instead — grepping the
# whole run would let another Application's identical summary stand in.
section_lines() {
  local output="$1" marker="$2"
  awk -v m="$marker" '
    /^===>/ { inside = index($0, m) ? 1 : 0; next }
    inside { print }
  ' "$output"
  return
}

# section_diff <output> <marker>: the changed lines of one Application's section
# of an argo-compare run, which reports every Application in one stream. Bounded
# by the next `===>` heading, so a section that is not the last one does not
# collect its successors' diffs.
section_diff() {
  local output="$1" marker="$2"
  awk -v m="$marker" '
    /^===>/ { inside = index($0, m) ? 1 : 0; next }
    inside && /^[+-][^+-]/ { print }
  ' "$output" | normalize_diff
  return
}

# appset_apps <appset>: names of the Applications the real controller generated.
# Selected by ownerReference, not by a label: the controller stamps no
# application-set-name label on what it generates, so a label selector silently
# matches nothing and every count built on it reads as zero.
appset_apps() {
  local appset="$1"
  kc -n "$NS_ARGOCD" get applications -o json 2>/dev/null |
    jq -r --arg name "$appset" \
      '.items[]
       | select(.metadata.ownerReferences // []
           | any(.kind == "ApplicationSet" and .name == $name))
       | .metadata.name' |
    sort
  return
}

# appset_count <appset>: how many Applications exist for <appset> right now.
appset_count() {
  local appset="$1"
  appset_apps "$appset" | grep -c . || true
  return
}

# wait_appset <appset> <count> [attempts]: block until the controller has
# generated <count> Applications for <appset>.
wait_appset() {
  local appset="$1" count="$2" attempts="${3:-30}"
  retry "$attempts" 2 test_appset_count "$appset" "$count"
  return
}

test_appset_count() {
  local appset="$1" want="$2"
  [[ "$(appset_count "$appset")" -eq "$want" ]]
  return
}
