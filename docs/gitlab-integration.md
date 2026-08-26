# GitLab integration

`argo-compare` can post the comparison output as a comment on a GitLab Merge Request. Configure it with environment variables:

```bash
ARGO_COMPARE_COMMENT_PROVIDER=gitlab \
ARGO_COMPARE_GITLAB_URL=https://gitlab.com \
ARGO_COMPARE_GITLAB_TOKEN=$GITLAB_TOKEN \
ARGO_COMPARE_GITLAB_PROJECT_ID=12345 \
ARGO_COMPARE_GITLAB_MR_IID=10 \
argo-compare branch <target-branch>
```

Equivalent CLI flags are available:

```bash
argo-compare branch <target-branch> \
  --comment-provider gitlab \
  --gitlab-url https://gitlab.com \
  --gitlab-token "$GITLAB_TOKEN" \
  --gitlab-project-id 12345 \
  --gitlab-merge-request-iid 10
```

## GitLab CI auto-detection

When running inside GitLab CI, most settings are detected automatically:

- `--comment-provider` defaults to `gitlab` when `GITLAB_CI` and `CI_MERGE_REQUEST_IID` are present.
- `--gitlab-url` falls back to `CI_SERVER_URL`.
- `--gitlab-project-id` falls back to `CI_PROJECT_ID`.
- `--gitlab-merge-request-iid` falls back to `CI_MERGE_REQUEST_IID`.
- `--gitlab-token` falls back to `CI_JOB_TOKEN` if no explicit token is provided (ensure the token has the necessary scope to post notes).

## One note per run

A run publishes a single note covering every Application it compared, with a
section each. An ApplicationSet generating thirty Applications therefore posts
one note, not thirty. Applications with no differences are still listed, so the
note accounts for everything that was compared.

The note is split only when it would exceed GitLab's 1 MB limit. Each part is
numbered, and a part that begins partway through an Application repeats its
name, so no diff is ever shown without saying which Application it belongs to.

An Application whose validation output alone would fill a note has that summary
truncated, with a pointer to the job log for the rest — a note over the limit is
rejected outright, which would cost every other Application its results too.
