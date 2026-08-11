# Tool responses

Every tool returns a single-line JSON document as its text content. Empty
values are omitted where noted, so a response only carries what is actionable.

Every MCP tool accepts an optional `worktree_path`; CLI commands expose the
same selection as `--worktree`. When it is omitted, the Git worktree containing
the process's current working directory is used. When it is
provided, that path is resolved to its Git worktree root for that call.

## `repository_context`

Takes an optional `worktree_path` and reports that worktree's checkout. It
does not require any remote-service configuration.

```json
{
  "project": "acme/example",
  "remote": { "host": "gitlab.example", "project": "acme/example" },
  "worktree": { "root": "…", "cwd": "…", "cwdIsRoot": true },
  "branch": "feature/example",
  "upstream": "origin/feature/example",
  "commit": "abc123",
  "dirty": false,
  "detached": false
}
```

## `code_review_context`

When the selected worktree's current branch has an open GitLab merge request,
it reports that merge request and its latest actionable review comment. It
fails when several open merge requests exist for the branch.

```json
{
  "merge_request": { "iid": 7, "title": "…", "state": "opened", "draft": false, "source_branch": "…", "target_branch": "…", "sha": "…", "web_url": "…" },
  "review": { "id": 3, "author": "reviewer", "created_at": "…", "body": "…", "path": "internal/app.go", "line": 12, "position_type": "text", "base_sha": "…", "head_sha": "…", "resolvable": true, "resolved": false }
}
```

When HEAD is detached or the branch has no open merge request, both
`merge_request` and `review` are `null`.

`review` is `null` when no unresolved comment is left by anyone other than the
authenticated user and the accounts listed in `GITLAB_IGNORED_REVIEW_AUTHORS`.
Comments anchored to a diff position are preferred over general discussion, and
the newest one wins.

## `jenkins_status`

Without `build_url`, the build is located through the GitLab commit status
named by `JENKINS_BUILD_STATUS_NAME` (default `build`) on the selected merge
request's current head SHA. When no merge request is selected, it falls back to
the worktree's checked out commit. This makes the external Jenkins build
discoverable even when the local checkout is stale. The response also carries
`branch`, the commit used for lookup, `checkout_commit`, `merge_request`,
`merge_request_lookup`, and `gitlabStatus`. With an explicit `build_url` those
fields are `null`, because the build need not belong to the current commit.

```json
{
  "repository": {},
  "branch": "feature/example",
  "commit": "abc123",
  "checkout_commit": "def456",
  "merge_request": {},
  "merge_request_lookup": { "project": "acme/example", "source_branch": "…", "selection": "open_preferred", "all_matches": 2, "open_matches": 1, "selected_state": "opened", "reason": "open_merge_request", "related_merge_requests": [] },
  "gitlabStatus": { "name": "build", "sha": "abc123", "status": "failed", "target_url": "…", "pipeline_id": 42, "created_at": "…" },
  "build": { "number": 42, "pipelineId": 42, "result": "FAILURE", "building": false, "timestamp": "…", "durationMs": 1000, "url": "…", "description": null },
  "stages": { "failed": [], "notExecuted": [] },
  "tests": { "passCount": 1, "failCount": 1, "skipCount": 0 },
  "issues": [{ "kind": "stage" }, { "kind": "test" }, { "kind": "log" }]
}
```

`issues` is the flattened, ordered list of everything worth acting on: failed
stages, failing test cases, and matched console lines. Console output is only
fetched for builds that are running or did not succeed.

When Jenkins has discarded the build record, the response reduces to a
`REMOVED` build and a single `build` issue explaining that the pipeline needs a
rerun.

`merge_request_lookup.reason` is one of `open_merge_request`,
`matching_non_open_merge_request`, `no_selectable_merge_request`,
`no_matching_merge_request`, or `detached_head`.

## `ci_gate_runs`

Returns the latest status for every CI gate published through GitLab's
commit-status API for the selected merge request's current head SHA; it falls
back to the worktree commit when no merge request is selected. Superseded
status attempts are omitted. Each record contains the gate name, exact
commit SHA, current state, run URL, timestamps, and—when GitLab provides
them—the status ID, pipeline ID, and author. This is the canonical structured
route for external Jenkins, SonarQube, or other CI systems; merge-request
comments are intentionally not used to decide CI state.

```json
{
  "repository": {},
  "branch": "feature/example",
  "commit": "abc123",
  "checkout_commit": "def456",
  "merge_request": {},
  "merge_request_lookup": {},
  "gates": [{ "source": "gitlab_commit_status", "gate": "build", "commit_sha": "abc123", "state": "failed", "url": "…", "pipeline_id": 42, "created_at": "…" }]
}
```

## `jira_ticket`

Requires `ticket`, an issue key such as `ABC-123`.

```json
{
  "meta": { "key": "ABC-123", "id": "1", "url": "…", "summary": "…", "issueType": {}, "status": {}, "priority": {}, "project": {}, "assignee": {}, "reporter": {}, "creator": {}, "created": "…", "updated": "…", "dueDate": "…", "labels": [], "components": [], "fixVersions": [], "versions": [] },
  "content": { "description": "…", "environment": "…", "comments": [], "links": [], "attachments": [] }
}
```

Markup is stripped and long text is truncated. Empty fields are dropped from
both objects. Image attachments are downloaded below
`JIRA_ATTACHMENT_DIR/<ticket>/attachments/` (default `ticket-analysis`) and
reported with a repository-relative `localPath`; other attachments are listed
with their URL only. Downloads are capped at 20 MB, must be served from the
Jira host, and cannot escape the repository root.

## `sonar_issues`

Takes an optional `branch`, defaulting to the selected worktree's current branch. The name is
prefixed with `SONAR_BRANCH_PREFIX` (default `origin/`) unless it already
carries it, and must be a branch SonarQube has analysed.

`SONAR_PROJECT_KEY` is preferred. If it is not configured, the tool examines
GitLab statuses for the selected merge request's current head SHA and accepts a
project key only from the `id` parameter of a URL on the configured SonarQube
host. It refuses missing or ambiguous candidates rather than selecting a bot
comment or an arbitrary external link.

```json
{
  "projectKey": "acme:example",
  "projectKeySource": "environment",
  "failedConditions": [{ "status": "ERROR", "metricKey": "new_branch_coverage", "comparator": "LT", "errorThreshold": "70", "actualValue": "50.0" }],
  "coverageFiles": [{ "path": "src/Example.java", "uncoveredLines": [], "partiallyCoveredLines": [{ "line": 42, "conditions": 2, "coveredConditions": 1, "code": "if (enabled) {" }] }],
  "issues": [{ "severity": "CRITICAL", "rule": "go:S123", "component": "…", "message": "…", "lineRange": [4, 6] }]
}
```

When inferred, `projectKeySource` is `gitlab_commit_status` and the response
also includes the compact `gitlabStatus` that carried the verified same-host
SonarQube URL.

`coverageFiles` is only populated when a coverage condition actually failed,
and lists new-code lines only. `issues` covers the new code period and is
limited to issues in the `CONFIRMED` or `OPEN` state.
