# deboai

[![CI](https://github.com/jhoogstraat/deboai/actions/workflows/ci.yml/badge.svg)](https://github.com/jhoogstraat/deboai/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/jhoogstraat/deboai.svg)](https://pkg.go.dev/github.com/jhoogstraat/deboai)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE.md)

*dev + boost + ai.*

An opinionated [Model Context Protocol](https://modelcontextprotocol.io) server that gives a coding assistant fast, compact access to the development tools around a Git repository: GitLab merge request reviews, Jenkins build failures, Jira tickets, and SonarQube quality gates.

Every tool answers one question in one call and returns compact JSON — only the parts that are actionable, so the assistant does not spend its context on API envelopes. Every tool accepts an optional `worktree_path`; omit it to inspect the current working directory, or pass another Git worktree for that call.

## Tools

| Tool | Arguments | Returns |
| --- | --- | --- |
| `repository_context` | `worktree_path` (optional) | Local Git repository and checkout context. |
| `code_review_context` | `worktree_path` (optional) | Matching GitLab merge request and latest actionable review comment, when available. |
| `jenkins_status` | `build_url` (optional), `worktree_path` (optional) | Build result, failed and skipped stages, failing tests, and console highlights. Without `build_url` the build of the selected worktree's current commit is located through its GitLab commit status. |
| `jira_ticket` | `ticket` (required), `worktree_path` (optional) | Compact issue fields, comments, links, and attachments. Image attachments are downloaded into the selected worktree. |
| `sonar_issues` | `branch` (optional), `worktree_path` (optional) | Failed quality gate conditions, uncovered new-code lines, and confirmed or open issues. Defaults to the selected worktree's current branch. |

See [docs/tools.md](docs/tools.md) for the response shapes.

## Install

```sh
go install github.com/jhoogstraat/deboai/cmd/deboai@latest
```

Or build from a checkout — the Go toolchain is pinned with [mise](https://mise.jdx.dev):

```sh
mise install
go build -o bin/deboai ./cmd/deboai
```

## Configure

The server reads its credentials from the environment, falling back to `.env` and `debo.env` in the repository it inspects. Variables already set in the environment always win. Copy [configs/example.env](configs/example.env) as a starting point:

```sh
cp configs/example.env /path/to/your/repo/.env
```

Each integration is configured independently, and only fails the tool that needs it:

| Integration | Required | Optional |
| --- | --- | --- |
| GitLab | `GITLAB_API_URL`, `GITLAB_TOKEN` | `GITLAB_IGNORED_REVIEW_AUTHORS`, `GITLAB_PROJECT_ID` |
| Jenkins | `JENKINS_URL`, `JENKINS_USER`, `JENKINS_API_TOKEN` | `JENKINS_BUILD_STATUS_NAME` |
| Jira | `JIRA_URL`, `JIRA_API_TOKEN` | `JIRA_API_PATH`, `JIRA_BROWSE_PATH`, `JIRA_COOKIE`, `JIRA_ATTACHMENT_DIR`, `JIRA_STATUS_NAMES` |
| SonarQube | `SONAR_HOST_URL`, `SONAR_TOKEN`, `SONAR_PROJECT_KEY` | `SONAR_BRANCH_PREFIX` |

`SONARQUBE_CLI_SERVER` and `SONARQUBE_CLI_TOKEN` are accepted as aliases for the SonarQube host and token.

Two settings control the server itself: `DEBOAI_REPOSITORY_ROOT` pins the default repository to inspect (otherwise it is discovered from the working directory), and `DEBOAI_ENV_FILE` overrides which environment files are loaded.

## Run

The server speaks JSON-RPC over stdio and must start inside the repository it reports on. It defaults to MCP protocol revision `2026-07-28`, negotiating down to `2025-06-18`, `2025-03-26`, or `2024-11-05` for clients that request one of those. Register it with your assistant, for example in `.mcp.json`:

```json
{
  "mcpServers": {
    "deboai": {
      "command": "deboai"
    }
  }
}
```

To try it by hand:

```sh
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | deboai
```

## Layout

The repository follows the [Go project layout](https://github.com/golang-standards/project-layout):

```
cmd/deboai        the server binary
internal/mcp        the stdio MCP server: protocol, argument validation, dispatch
internal/tools      wires the clients into the exposed tools
internal/gitlab     merge requests, review discussions, commit statuses
internal/jenkins    build reports, stage and test failures
internal/jira       compact issue context and attachment downloads
internal/sonar      quality gates, new-code coverage, issues
internal/git        repository context
internal/config     environment and env-file handling
internal/httpx      HTTP and JSON conveniences
configs             example configuration
docs                tool response reference
```

## Develop

```sh
go test ./...
go vet ./...
golangci-lint run ./...
```

Adding a tool means adding an `mcp.Tool` in `internal/tools`. The server validates arguments against the declared input schema, so handlers only see arguments that exist, are strings, and are non-empty when required.

## License

[MIT](LICENSE.md)
