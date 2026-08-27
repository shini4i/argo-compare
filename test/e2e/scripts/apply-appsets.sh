#!/usr/bin/env bash
# Apply the fixture ApplicationSets and wait for the real ArgoCD controller to
# generate from them. The generated Applications are the oracle the parity phase
# compares argo-compare's own expansion against, so this must complete before it.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib.sh
. "${here}/lib.sh"

# Each fixture and the count it must generate from the seeded `main` tree: the
# list generators are fixed at two elements; the git generators see clusters/dev
# and clusters/staging (clusters/prod exists only on the feature branch).
declare -A want=(
  [e2e-list]=2
  [e2e-git-dir]=2
  [e2e-git-file]=2
  [e2e-generated]=2
  # Applied so the controller vouches for the baseline the lifecycle phase uses.
  [e2e-lifecycle]=2
)

for name in "${!want[@]}"; do
  fixture="${E2E_DIR}/fixtures/${name/e2e-/appset-}.yaml"
  [[ -f "$fixture" ]] || die "missing fixture ${fixture}"
  sed "s|REPO_URL|${ORIGIN_URL}|g" "$fixture" | kc apply -f - >/dev/null
done

# The plain Applications too: render-parity asks ArgoCD what `addon` renders, so
# ArgoCD has to know about it.
for app in demo addon; do
  sed "s|REPO_URL|${ORIGIN_URL}|g" "${E2E_DIR}/fixtures/app-${app}.yaml" |
    kc apply -f - >/dev/null
done

for name in "${!want[@]}"; do
  wait_appset "$name" "${want[$name]}" 60 ||
    die "${name} generated $(appset_count "$name") Application(s), expected ${want[$name]}"
  echo "  ${name}: ${want[$name]} Application(s) generated"
done

# A generator that cannot reach the repo reports success with zero Applications,
# so the counts above are the real gate; this only surfaces the reason on failure.
kc -n "$NS_ARGOCD" get applicationsets \
  -o custom-columns='NAME:.metadata.name,STATUS:.status.conditions[?(@.type=="ErrorOccurred")].message'
