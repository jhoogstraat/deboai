// Command debo exposes repository development tools as a CLI or MCP server.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/jhoogstraat/deboai/internal/tools"
)

// version is overridden at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, input io.Reader, output, errorOutput io.Writer) error {
	command := newRootCommand(version, tools.All(), input, output, errorOutput)
	command.SetArgs(arguments)
	return command.ExecuteContext(ctx)
}
