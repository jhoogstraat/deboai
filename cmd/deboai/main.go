// Command deboai serves the AI Development Boost tools over the Model
// Context Protocol on stdio.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jhoogstraat/deboai/internal/mcp"
	"github.com/jhoogstraat/deboai/internal/tools"
)

const (
	serverName   = "deboai"
	instructions = "Repository helper tools. Start this server from the repository you want to inspect."
)

// version is overridden at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	info := mcp.Info{Name: serverName, Version: version, Instructions: instructions}
	server := mcp.NewServer(info, tools.All()...)
	return server.Serve(context.Background(), os.Stdin, os.Stdout)
}
