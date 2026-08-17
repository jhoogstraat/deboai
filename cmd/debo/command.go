package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/jhoogstraat/deboai/internal/mcp"
	"github.com/jhoogstraat/deboai/internal/tools"
)

const (
	serverName   = "debo"
	instructions = "Repository helper tools. Start this server from the repository you want to inspect."
)

func newRootCommand(version string, definitions []tools.Definition, input io.Reader, output, errorOutput io.Writer) *cobra.Command {
	index := make(map[string]tools.Definition, len(definitions))
	for _, definition := range definitions {
		index[definition.Name] = definition
	}

	var mcpMode bool
	var worktree string
	root := &cobra.Command{
		Use:           "debo",
		Short:         "Inspect the development context around a Git repository",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(command *cobra.Command, _ []string) error {
			if mcpMode && command != command.Root() {
				return errors.New("--mcp cannot be combined with a command")
			}
			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			if !mcpMode {
				return command.Help()
			}
			if worktree != "" {
				return errors.New("--mcp cannot be combined with --worktree")
			}
			info := mcp.Info{Name: serverName, Version: version, Instructions: instructions}
			server := mcp.NewServer(info, mcp.Adapt(definitions)...)
			return server.Serve(command.Context(), input, output)
		},
	}
	root.SetIn(input)
	root.SetOut(output)
	root.SetErr(errorOutput)
	root.Flags().BoolVar(&mcpMode, "mcp", false, "serve the Model Context Protocol over stdio")
	root.PersistentFlags().StringVarP(&worktree, "worktree", "w", "", "Git worktree to inspect")
	_ = root.MarkPersistentFlagDirname("worktree")

	runTool := func(name string, arguments func([]string) tools.Arguments) func(*cobra.Command, []string) error {
		return func(command *cobra.Command, positionals []string) error {
			definition, ok := index[name]
			if !ok {
				return fmt.Errorf("tool is unavailable: %s", name)
			}
			values := arguments(positionals)
			if worktree != "" {
				values["worktree_path"] = worktree
			}
			result, err := definition.Handler(command.Context(), values)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), result)
			return err
		}
	}
	var jiraAttachment string
	jiraCommand := &cobra.Command{
		Use:     "jira <ticket>",
		Short:   "Show a Jira ticket and download attachments",
		Args:    cobra.ExactArgs(1),
		Example: "  debo jira ABC-123\n  debo jira ABC-123 --attachment notes.txt",
		RunE: runTool("jira", func(arguments []string) tools.Arguments {
			values := tools.Arguments{"ticket": arguments[0]}
			if jiraAttachment != "" {
				values["attachment"] = jiraAttachment
			}
			return values
		}),
	}
	jiraCommand.Flags().StringVar(&jiraAttachment, "attachment", "", "download one attachment by ID or exact filename")

	var confluenceAttachment string
	confluenceCommand := &cobra.Command{
		Use:     "confluence <page-or-url>",
		Short:   "Show a Confluence page and optionally download an attachment",
		Args:    cobra.ExactArgs(1),
		Example: "  debo confluence https://wiki.example/pages/123/Runbook\n  debo confluence 123 --attachment diagram.pdf",
		RunE: runTool("confluence", func(arguments []string) tools.Arguments {
			values := tools.Arguments{"page": arguments[0]}
			if confluenceAttachment != "" {
				values["attachment"] = confluenceAttachment
			}
			return values
		}),
	}
	confluenceCommand.Flags().StringVar(&confluenceAttachment, "attachment", "", "download one attachment by ID or exact filename")

	root.AddCommand(
		&cobra.Command{
			Use:     "repository",
			Short:   "Show local repository and checkout context",
			Args:    cobra.NoArgs,
			Example: "  debo repository\n  debo repository --worktree ../other-worktree",
			RunE:    runTool("repository", noArguments),
		},
		&cobra.Command{
			Use:     "review",
			Short:   "Show the matching GitLab merge request and review",
			Args:    cobra.NoArgs,
			Example: "  debo review",
			RunE:    runTool("review", noArguments),
		},
		&cobra.Command{
			Use:     "ci",
			Short:   "Show CI gate runs for the merge request head",
			Args:    cobra.NoArgs,
			Example: "  debo ci",
			RunE:    runTool("ci", noArguments),
		},
		&cobra.Command{
			Use:     "jenkins [build-url]",
			Short:   "Show Jenkins build failures",
			Args:    cobra.MaximumNArgs(1),
			Example: "  debo jenkins\n  debo jenkins https://jenkins.example/job/example/42/",
			RunE: runTool("jenkins", func(arguments []string) tools.Arguments {
				return optionalArgument("build_url", arguments)
			}),
		},
		jiraCommand,
		confluenceCommand,
		&cobra.Command{
			Use:     "sonar [branch]",
			Short:   "Show SonarQube quality-gate and code issues",
			Args:    cobra.MaximumNArgs(1),
			Example: "  debo sonar\n  debo sonar feature/example",
			RunE: runTool("sonar", func(arguments []string) tools.Arguments {
				return optionalArgument("branch", arguments)
			}),
		},
	)
	return root
}

func noArguments([]string) tools.Arguments {
	return tools.Arguments{}
}

func optionalArgument(name string, arguments []string) tools.Arguments {
	values := tools.Arguments{}
	if len(arguments) == 1 {
		values[name] = arguments[0]
	}
	return values
}
