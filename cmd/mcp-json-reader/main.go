package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/oovz/mcp-json-reader/v2/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(app.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}
