package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"agentcli.local/ai/internal/cli"
	_ "agentcli.local/ai/internal/provider/antigravity"
	_ "agentcli.local/ai/internal/provider/claude"
	_ "agentcli.local/ai/internal/provider/codex"
)

func main() {
	// Long-running commands such as `ingest --watch` read this context to stop
	// cleanly on Ctrl-C instead of being killed mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := cli.New(os.Stdin, os.Stdout, os.Stderr)
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
