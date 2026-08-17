# debo

[![CI](https://github.com/jhoogstraat/deboai/actions/workflows/ci.yml/badge.svg)](https://github.com/jhoogstraat/deboai/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/jhoogstraat/deboai.svg)](https://pkg.go.dev/github.com/jhoogstraat/deboai)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE.md)

*dev + boost + ai.*

`debo` gives developers and coding assistants fast, compact access to the tools around a Git repository: GitLab merge request reviews, Jenkins build failures, Jira tickets, Confluence pages, and SonarQube quality gates.

Every command answers one question and prints compact JSON containing only actionable data. Use the CLI directly, or start its [Model Context Protocol](https://modelcontextprotocol.io) server with `--mcp`.

## Install

```sh
go install github.com/jhoogstraat/deboai/cmd/debo@latest
```

Or build from a checkout — the Go toolchain is pinned with [mise](https://mise.jdx.dev):

```sh
mise install
go build -o bin/debo ./cmd/debo
```

## Commands

Run `debo` or `debo --help` to see the command list.

| Command | Returns |
| --- | --- |
| `debo repository` | Local Git repository and checkout context. |
| `debo review` | Matching GitLab merge request and latest actionable review comment, when available. |
| `debo ci` | Structured GitLab status records for CI gates attached to the selected merge-request head. |
| `debo jenkins [build-url]` | Build result, failed and skipped stages, failing tests, and console highlights. Without a URL, locates the build for the selected merge-request head or current commit through GitLab. |
| `debo jira <ticket> [--attachment ID-OR-NAME]` | Compact Jira issue fields, comments, links, and attachments. Images and an explicitly selected attachment are downloaded into the selected worktree. |
| `debo confluence <page-or-url> [--attachment ID-OR-NAME]` | Compact Confluence page metadata and plain-text body, optionally downloading one attachment into the selected worktree. |
| `debo sonar [branch]` | Failed quality-gate conditions, uncovered new-code lines, and confirmed or open issues. Defaults to the current branch. |

Every command accepts `--worktree PATH` (or `-w PATH`). Without it, `DEBOAI_REPOSITORY_ROOT` or the current working directory is used.

Examples:

```sh
debo repository
debo review --worktree ../other-worktree
debo ci
debo jenkins https://jenkins.example/job/example/42/
debo jira ABC-123
debo jira ABC-123 --attachment notes.txt
debo confluence https://wiki.example/pages/123/Runbook --attachment diagram.pdf
debo sonar feature/example
```

See [docs/tools.md](docs/tools.md) for the response shapes.

## Completion

Generate shell completion with `debo completion bash`, `zsh`, `fish`, or `powershell`. For example:

```sh
# zsh
mkdir -p ~/.zsh/completions
debo completion zsh > ~/.zsh/completions/_debo

# bash
mkdir -p ~/.local/share/bash-completion/completions
debo completion bash > ~/.local/share/bash-completion/completions/debo

# fish
debo completion fish > ~/.config/fish/completions/debo.fish
```

## Configure

For every command or MCP tool call, `debo` reads credentials from the process environment, then from the selected worktree when present, `DEBOAI_REPOSITORY_ROOT` when set, and finally the working directory. Each directory contributes `.env` followed by `debo.env`; earlier values win and env files never modify the process. Copy [configs/example.env](configs/example.env) as a starting point:

```sh
cp configs/example.env /path/to/your/repo/.env
```

Each integration is configured independently and only fails the command that needs it:

| Integration | Required | Optional |
| --- | --- | --- |
| GitLab | `GITLAB_API_URL`, `GITLAB_TOKEN` | `GITLAB_IGNORED_REVIEW_AUTHORS`, `GITLAB_PROJECT_ID` |
| Jenkins | `JENKINS_URL`, `JENKINS_USER`, `JENKINS_API_TOKEN` | `JENKINS_BUILD_STATUS_NAME` |
| Jira | `JIRA_URL`, `JIRA_API_TOKEN` | `JIRA_API_PATH`, `JIRA_BROWSE_PATH`, `JIRA_COOKIE`, `JIRA_ATTACHMENT_DIR`, `JIRA_STATUS_NAMES` |
| Confluence | `CONFLUENCE_URL`, `CONFLUENCE_API_TOKEN` | `CONFLUENCE_USER`, `CONFLUENCE_API_PATH`, `CONFLUENCE_COOKIE`, `CONFLUENCE_ATTACHMENT_DIR` |
| SonarQube | `SONAR_HOST_URL`, `SONAR_TOKEN` | `SONAR_PROJECT_KEY`, `SONAR_BRANCH_PREFIX` |

`SONARQUBE_CLI_SERVER` and `SONARQUBE_CLI_TOKEN` are accepted as aliases for the SonarQube host and token.
When `SONAR_PROJECT_KEY` is omitted, `sonar` can infer it from the `id`
parameter of a same-host SonarQube URL published as a GitLab commit status for
the selected merge request's current head SHA. Set the variable explicitly if
no such status exists or more than one project key is found.

`DEBOAI_ENV_FILE`, when set in the process environment, replaces the default `.env` and `debo.env` filenames.

## MCP mode

MCP mode is opt-in and speaks JSON-RPC over stdio. Register it with your assistant using the required `--mcp` argument:

```json
{
  "mcpServers": {
    "debo": {
      "command": "debo",
      "args": ["--mcp"]
    }
  }
}
```

The server exposes the existing `repository`, `review`, `jenkins`, `ci`, `jira`, `confluence`, and `sonar` tools. Each accepts an optional `worktree_path`, allowing one process to inspect multiple worktrees. The server defaults to MCP protocol revision `2026-07-28`, negotiating down to `2025-06-18`, `2025-03-26`, or `2024-11-05`.

To try it by hand:

```sh
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | debo --mcp
```

Running `debo` without `--mcp` starts the CLI and is intentionally incompatible with older MCP configurations.

## Layout

```text
cmd/debo          CLI entrypoint and MCP mode selection
internal/tools    transport-neutral tool definitions and implementations
internal/mcp      MCP adapter, protocol, validation, and dispatch
internal/gitlab   merge requests, review discussions, commit statuses
internal/jenkins  build reports, stage and test failures
internal/jira     compact issue context and attachment downloads
internal/confluence compact page context, body extraction, attachment downloads
internal/sonar    quality gates, new-code coverage, issues
internal/git      repository context
internal/config   environment and env-file handling
internal/httpx    HTTP and JSON conveniences
configs           example configuration
docs              tool response reference
```

## Develop

```sh
go test ./...
go vet ./...
golangci-lint run ./...
```

Adding a tool means adding a transport-neutral definition in `internal/tools`, then choosing its human-facing CLI command in `cmd/debo`. The MCP adapter derives its strict input schema from the same definition.

## License

[MIT](LICENSE.md)
