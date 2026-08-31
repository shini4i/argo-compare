#!/usr/bin/env bash
# Seed Gitea with the lab's gitops repo: fixtures/gitops on `main`, plus a
# `feature` branch carrying the change argo-compare is asked to compare.
# Idempotent — the repo is force-pushed, so a re-run resets it.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib.sh
. "${here}/lib.sh"

retry 60 2 gitea_ready || die "gitea API never became ready on ${GITEA_URL}"

gapi -X POST "${GITEA_API}/orgs" -d "{\"username\":\"${GITEA_ORG}\"}" >/dev/null 2>&1 || true
gapi -X POST "${GITEA_API}/orgs/${GITEA_ORG}/repos" \
  -d "{\"name\":\"${GITEA_REPO}\",\"private\":false,\"auto_init\":false}" >/dev/null 2>&1 || true

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

git -C "$work" init -q -b "$TARGET_BRANCH"
git -C "$work" config user.email e2e@example.com
git -C "$work" config user.name e2e

cp -r "${E2E_DIR}/fixtures/gitops/." "${work}/"

# The Application manifests name the in-cluster repo URL, which is what ArgoCD
# resolves and what the clone's origin is rewritten to.
mkdir -p "${work}/apps"
for app in demo addon; do
  sed "s|REPO_URL|${ORIGIN_URL}|g" "${E2E_DIR}/fixtures/app-${app}.yaml" \
    >"${work}/apps/${app}.yaml"
done

# The anchored ApplicationSet has to live in the repo as well as in the cluster:
# charts/generated's anchor resolves it by repo path, and a missing file there
# surfaces as the confusing "file is empty".
sed "s|REPO_URL|${ORIGIN_URL}|g" "${E2E_DIR}/fixtures/appset-generated.yaml" \
  >"${work}/apps/generated-appset.yaml"

git -C "$work" add -A
git -C "$work" commit -qm "seed gitops fixtures"

# The feature branch is what a pull request would propose: the Application's
# inline values change, so the standard flow has a real diff to render, and a new
# cluster directory appears, so a git generator gains an Application.
git -C "$work" checkout -q -b "$FEATURE_BRANCH"
# Verified rather than assumed: a sed that silently matches nothing would leave
# the branches identical and every phase asserting on the diff would pass
# vacuously.
sed -i -E 's/^([[:space:]]*)replicaCount: 1$/\1replicaCount: 3/' "${work}/apps/demo.yaml"
grep -qE '^[[:space:]]*replicaCount: 3$' "${work}/apps/demo.yaml" ||
  die "the feature-branch replica bump did not apply to apps/demo.yaml"

# The addon chart's own content changes while apps/addon.yaml stays identical,
# which is what makes the render-parity comparison two-directional.
sed -i -E 's/^message: hello$/message: world/' "${work}/charts/addon/values.yaml"
grep -qx 'message: world' "${work}/charts/addon/values.yaml" ||
  die "the feature-branch addon chart change did not apply"

# The anchored ApplicationSet's chart changes while its manifest stays put, so
# the change reaches argo-compare only through charts/generated's anchor.
sed -i -E 's/^banner: first$/banner: second/' "${work}/charts/generated/values.yaml"
grep -qx 'banner: second' "${work}/charts/generated/values.yaml" ||
  die "the feature-branch generated chart change did not apply"

mkdir -p "${work}/clusters/prod"
printf 'cluster: prod\nreplicas: 4\n' >"${work}/clusters/prod/config.yaml"
git -C "$work" add -A
git -C "$work" commit -qm "bump demo replicas and add the prod cluster"

git -C "$work" push -q --force "$CLONE_URL" \
  "refs/heads/${TARGET_BRANCH}:refs/heads/${TARGET_BRANCH}" \
  "refs/heads/${FEATURE_BRANCH}:refs/heads/${FEATURE_BRANCH}"

echo "seeded ${GITEA_ORG}/${GITEA_REPO}: ${TARGET_BRANCH} + ${FEATURE_BRANCH}"
