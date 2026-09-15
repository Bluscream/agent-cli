package main

import (
	"fmt"
	"os"

	"agentcli.local/ai/internal/cli"
	_ "agentcli.local/ai/internal/provider/antigravity"
	_ "agentcli.local/ai/internal/provider/claude"
	_ "agentcli.local/ai/internal/provider/codex"
)

func main() {
	cmd := cli.New(os.Stdin, os.Stdout, os.Stderr)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
