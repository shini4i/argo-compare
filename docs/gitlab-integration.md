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
