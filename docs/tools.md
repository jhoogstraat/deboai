# Tool responses

Every tool returns a single-line JSON document as its text content. Empty
values are omitted where noted, so a response only carries what is actionable.

Every MCP tool accepts an optional `worktree_path`; CLI commands expose the
same selection as `--worktree`. When it is omitted, the Git worktree containing
the process's current working directory is used. When it is
provided, that path is resolved to its Git worktree root for that call.

## `repository`

Takes an optional `worktree_path` and reports that worktree's checkout. It
does not require any remote-service configuration.

```json
{
  "remote": { "host": "gitlab.example", "project": "acme/example" },
  "branch": "feature/example",
  "upstream": "origin/feature/example",
  "commit": "abc123",
  "dirty": false
}
```

`worktree` is included only when the checkout is a linked Git worktree
(not the main one), and carries its name, for example `"worktree": "feature"`.
A missing `branch` means HEAD is detached.

## `review`

When the selected worktree's current branch has an open GitLab merge request,
it reports that merge request and its latest actionable review comment. It
fails when several open merge requests exist for the branch.

```json
{
  "mr": { "iid": 7, "title": "…", "state": "opened", "draft": false, "source_branch": "…", "target_branch": "…", "sha": "…", "web_url": "…" },
  "review": { "id": 3, "author": "reviewer", "created_at": "…", "body": "…", "path": "internal/app.go", "line": 12, "position_type": "text", "base_sha": "…", "head_sha": "…", "resolvable": true, "resolved": false }
}
```

When HEAD is detached or the branch has no open merge request, both
`mr` and `review` are `null`.

`review` is `null` when no unresolved comment is left by anyone other than the
authenticated user and the accounts listed in `GITLAB_IGNORED_REVIEW_AUTHORS`.
Comments anchored to a diff position are preferred over general discussion, and
the newest one wins.

## `jenkins`

Without `build_url`, the build is located through the GitLab commit status
named by `JENKINS_BUILD_STATUS_NAME` (default `build`) on the open merge
request's current head SHA. When the branch has no open merge request, it
falls back to the worktree's checked out commit. This makes the external Jenkins build
discoverable even when the local checkout is stale. The top-level `commit`
records which commit the pipeline ran on; `mr` carries the merge request IID
when that commit came from one. With an explicit `build_url`, both are omitted
because the build need not belong to the current worktree.

```json
{
  "commit": "abc123",
  "mr": 7,
  "build": { "number": 42, "result": "FAILURE", "timestamp": "…", "durationMs": 1000, "url": "…", "description": null },
  "tests": { "passCount": 1, "failCount": 1, "skipCount": 0 },
  "issues": [{ "kind": "stage", "name": "Test", "status": "FAILED" }, { "kind": "stage", "name": "Deploy", "status": "NOT_EXECUTED" }, { "kind": "test" }, { "kind": "log" }]
}
```

`issues` is the flattened, ordered list of everything worth acting on: failed
or unexecuted stages, failing test cases, and matched console lines. Console
output is only fetched for builds that are running or did not succeed.

When Jenkins has discarded the build record, the response reduces to a
`REMOVED` build and a single `build` issue explaining that the pipeline needs a
rerun.

## `ci`

Returns the latest status for every CI gate published through GitLab's
commit-status API for the open merge request's current head SHA; it falls
back to the worktree commit when the branch has no open merge request. Superseded
status attempts are omitted. Each record contains the gate name, exact
commit SHA, current state, run URL, timestamps, and—when GitLab provides
them—the status ID, pipeline ID, and author. This is the canonical structured
route for external Jenkins, SonarQube, or other CI systems; merge-request
comments are intentionally not used to decide CI state. `commit` is the
commit the gates ran on; `mr` carries the merge request IID when that
commit came from one.

```json
{
  "commit": "abc123",
  "mr": 7,
  "gates": [{ "gate": "build", "state": "failed", "url": "…", "pipeline_id": 42, "created_at": "…" }]
}
```

## `jira`

Requires `ticket`, an issue key such as `ABC-123`. Optional `attachment`
selects one attachment by ID or exact filename for download.

```json
{
  "meta": { "key": "ABC-123", "id": "1", "url": "…", "summary": "…", "issueType": {}, "status": {}, "priority": {}, "project": {}, "assignee": {}, "reporter": {}, "creator": {}, "created": "…", "updated": "…", "dueDate": "…", "labels": [], "components": [], "fixVersions": [], "versions": [] },
  "content": { "description": "…", "environment": "…", "comments": [], "links": [], "attachments": [{ "id": "10", "filename": "notes.txt", "mimeType": "text/plain", "size": 42, "url": "…", "localPath": "…" }] }
}
```

Markup is stripped and long text is truncated. Empty fields are dropped from
both objects. Image attachments and the explicitly selected attachment are downloaded below
`JIRA_ATTACHMENT_DIR/<ticket>/attachments/` (default `ticket-analysis`) and
reported with a repository-relative `localPath`; other attachments are listed
with their URL only. Downloads are capped at 20 MB, must be served from the
Jira host, and cannot escape the repository root. Duplicate filenames must be
selected by ID.

## `confluence`

Requires `page`, a Confluence page ID or supported same-host page URL. Optional
`attachment` selects one page attachment by ID or exact filename for download. URLs
with a `pageId` query or numeric `/pages/<id>` or `/content/<id>` path use one
content request with the body expanded. Legacy `/display/<space>/<title>` URLs
are resolved with one title lookup. `CONFLUENCE_URL` and
`CONFLUENCE_API_TOKEN` are required and never inherited from Jira. Set
`CONFLUENCE_USER` for Cloud basic authentication; without it, the token is
sent as a bearer token. `CONFLUENCE_API_PATH`, `CONFLUENCE_COOKIE`, and
`CONFLUENCE_ATTACHMENT_DIR` are optional.

```json
{
  "meta": { "id": "123", "type": "page", "status": "current", "title": "Runbook", "url": "…", "space": { "key": "OPS", "name": "Operations" }, "version": { "number": 4, "when": "…" } },
  "content": { "body": "Deploy and verify …", "attachment": { "id": "att1", "filename": "diagram.pdf", "mimeType": "application/pdf", "size": 42, "url": "…", "localPath": "…" } }
}
```

Markup is stripped and the page body is truncated to keep linked
documentation compact. Selected attachments are downloaded below
`CONFLUENCE_ATTACHMENT_DIR/<page-id>/attachments/` (default
`confluence-analysis`). Downloads are capped at 20 MB, must be served from the
Confluence host, and cannot escape the repository root. Duplicate filenames
must be selected by ID.

## `sonar`

Takes an optional `branch`. When given, it is used verbatim — no prefix is
added — and must name a branch SonarQube has analysed.

Without `branch`, it prefers SonarQube's pull-request analysis of the open
GitLab merge request for the current branch, since that is the same gate
GitLab's own merge-request widget shows and its new-code scope matches the
MR diff. When there is no open merge request, it falls back to the current
branch, used verbatim. Either way, the resolved target is reported as
top-level `mr` (the merge request IID) or `branch`.

`SONAR_PROJECT_KEY` is preferred for the project key. If it is not
configured, the tool examines GitLab statuses for the selected merge
request's current head SHA and accepts a project key only from the `id`
parameter of a URL on the configured SonarQube host. It refuses missing or
ambiguous candidates rather than selecting a bot comment or an arbitrary
external link.

```json
{
  "mr": 7,
  "projectKey": "acme:example",
  "projectKeySource": "environment",
  "failedConditions": [{ "status": "ERROR", "metricKey": "new_branch_coverage", "comparator": "LT", "errorThreshold": "70", "actualValue": "50.0" }],
  "coverageFiles": [{ "path": "src/Example.java", "uncoveredLines": [], "partiallyCoveredLines": [{ "line": 42, "conditions": 2, "coveredConditions": 1, "code": "if (enabled) {" }] }],
  "issues": [{ "severity": "CRITICAL", "rule": "go:S123", "component": "…", "message": "…", "lineRange": [4, 6] }]
}
```

`mr` is replaced by `branch` when there is no open merge request. When the
project key is inferred, `projectKeySource` is `gitlab_commit_status`.

`coverageFiles` is only populated when a coverage condition actually failed,
and lists new-code lines only. `issues` covers the new code period and is
limited to issues in the `CONFIRMED` or `OPEN` state.
