// Command devboost serves the AI Development Boost tools over the Model
// Context Protocol on stdio.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jhoogstraat/ai-development-boost/internal/config"
	"github.com/jhoogstraat/ai-development-boost/internal/git"
	"github.com/jhoogstraat/ai-development-boost/internal/mcp"
	"github.com/jhoogstraat/ai-development-boost/internal/tools"
)

const (
	serverName   = "ai-development-boost"
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
	root, err := git.DiscoverRoot()
	if err != nil {
		return err
	}
	if err := config.LoadEnvFiles(root); err != nil {
		return err
	}
	if err := os.Chdir(root); err != nil {
		return fmt.Errorf("change to repository root: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	info := mcp.Info{Name: serverName, Version: version, Instructions: instructions}
	server := mcp.NewServer(info, tools.All(git.Open(root))...)
	return server.Serve(ctx, os.Stdin, os.Stdout)
}
