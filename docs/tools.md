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

Without `build_url`, the build is located through the GitLab commit status of
the selected worktree's checked out commit named by `JENKINS_BUILD_STATUS_NAME` (default `build`),
and the response also carries `branch`, `commit`, `merge_request`,
`merge_request_lookup`, and `gitlabStatus`. With an explicit `build_url` those
fields are `null`, because the build need not belong to the current commit.

```json
{
  "repository": {},
  "branch": "feature/example",
  "commit": "abc123",
  "merge_request": {},
  "merge_request_lookup": { "project": "acme/example", "source_branch": "…", "selection": "open_preferred", "all_matches": 2, "open_matches": 1, "selected_state": "opened", "reason": "open_merge_request", "related_merge_requests": [] },
  "gitlabStatus": { "status": "failed", "target_url": "…", "created_at": "…" },
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

```json
{
  "failedConditions": [{ "status": "ERROR", "metricKey": "new_branch_coverage", "comparator": "LT", "errorThreshold": "70", "actualValue": "50.0" }],
  "coverageFiles": [{ "path": "src/Example.java", "uncoveredLines": [], "partiallyCoveredLines": [{ "line": 42, "conditions": 2, "coveredConditions": 1, "code": "if (enabled) {" }] }],
  "issues": [{ "severity": "CRITICAL", "rule": "go:S123", "component": "…", "message": "…", "lineRange": [4, 6] }]
}
```

`coverageFiles` is only populated when a coverage condition actually failed,
and lists new-code lines only. `issues` covers the new code period and is
limited to issues in the `CONFIRMED` or `OPEN` state.
