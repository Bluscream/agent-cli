package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func memoryCommand(o *options) *cobra.Command {
	var targetProvider string
	var backupFlag bool
	var purgeFlag bool
	var syncFlag bool
	var backupDir string

	cmd := &cobra.Command{
		Use:     "memory [id]",
		Aliases: []string{"memories"},
		Short:   "Inspect, backup, purge, or synchronize saved agent memories and knowledge stores",
		RunE: func(cmd *cobra.Command, args []string) error {
			provName := targetProvider
			if provName == "" {
				provName = o.provider
			}

			var provList []provider.Provider
			if provName != "" {
				p, err := provider.Get(provName)
				if err != nil {
					return err
				}
				provList = []provider.Provider{p}
			} else {
				provList = provider.All()
			}

			// Handle --backup
			if backupFlag {
				if backupDir == "" {
					home, _ := os.UserHomeDir()
					backupDir = filepath.Join(home, ".local/share/agent-cli/backups")
				}
				for _, p := range provList {
					outPath, err := p.BackupMemories(backupDir)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "Failed to backup %s memories: %v\n", p.DisplayName(), err)
					} else if outPath != "" {
						fmt.Fprintf(cmd.OutOrStdout(), "[+] Backed up %s memories to: %s\n", p.DisplayName(), outPath)
					}
				}
				return nil
			}

			// Handle --purge
			if purgeFlag {
				for _, p := range provList {
					count, err := p.PurgeMemories()
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "Failed to purge %s memories: %v\n", p.DisplayName(), err)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "[*] Purged %d memory entries from %s\n", count, p.DisplayName())
					}
				}
				return nil
			}

			// Handle --sync
			if syncFlag {
				// 1. Gather all memories from all providers
				var allMems []provider.MemoryItem
				for _, p := range provider.All() {
					items, err := p.ListMemories()
					if err == nil {
						allMems = append(allMems, items...)
					}
				}
				if len(allMems) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No memories found across providers to synchronize.")
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "[*] Found %d total memory items to synchronize across agents.\n", len(allMems))
				for _, target := range provList {
					imported := 0
					for _, item := range allMems {
						if item.Provider == target.Name() {
							continue
						}
						if err := target.ImportMemory(item); err == nil {
							imported++
						}
					}
					fmt.Fprintf(cmd.OutOrStdout(), "[+] Synchronized %d memories into %s\n", imported, target.DisplayName())
				}
				return nil
			}

			// List memories
			var allItems []provider.MemoryItem
			for _, p := range provList {
				items, err := p.ListMemories()
				if err == nil {
					allItems = append(allItems, items...)
				}
			}

			// Check if single memory ID inspect was requested
			if len(args) > 0 {
				queryID := args[0]
				var found *provider.MemoryItem
				for _, m := range allItems {
					if idutil.Match(queryID, m.ID) {
						found = &m
						break
					}
				}
				if found == nil {
					return fmt.Errorf("memory not found: %s", queryID)
				}
				if o.isJSON() {
					return o.printJSON(cmd.OutOrStdout(), found)
				}
				dt := o.newDetail(cmd.OutOrStdout())
				detailRows(dt,
					kv("Provider", found.Provider),
					kv("Short ID", idutil.ShortID(found.ID)),
					kv("Full Raw ID", found.ID),
					kv("Title", found.Title),
					kv("Source File", found.SourceFile),
					kv("Created", found.CreatedAt.Format("2006-01-02 15:04:05")),
					kv("Last Updated", found.UpdatedAt.Format("2006-01-02 15:04:05")),
				)
				fmt.Fprintln(cmd.OutOrStdout(), o.renderTable(dt))
				fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n\n%s\n", bold.Sprint("CONTENT:"), strings.TrimSpace(found.Content))
				return nil
			}

			if o.isJSON() {
				return o.printJSON(cmd.OutOrStdout(), allItems)
			}

			if len(allItems) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No saved memories or knowledge items found.")
				return nil
			}

			out := cmd.OutOrStdout()
			t := o.newTable(out)
			t.AppendHeader(table.Row{"Provider", "ID", "Title", "Preview", "Updated"})
			// Fixed cols: provider (~8) + short-id (8) + date (~16) + chrome (~14) = ~46.
			// Title (col 3) and Preview (col 4) share remaining space.
			t.SetColumnConfigs([]table.ColumnConfig{
				o.flexColConfig(out, 3, 46+30),
				o.flexColConfig(out, 4, 46+30),
			})

			for _, m := range allItems {
				preview := strings.ReplaceAll(m.Content, "\n", " ")

				t.AppendRow(table.Row{
					m.Provider,
					idutil.ShortID(m.ID),
					m.Title,
					preview,
					m.UpdatedAt.Format("2006-01-02 15:04"),
				})
			}

			fmt.Fprintln(cmd.OutOrStdout(), o.renderTable(t))
			return nil
		},
	}

	cmd.Flags().StringVarP(&targetProvider, "provider", "p", "", "Target provider (antigravity, claude, codex)")
	cmd.Flags().BoolVar(&backupFlag, "backup", false, "Backup saved memories to disk")
	cmd.Flags().BoolVar(&purgeFlag, "purge", false, "Purge saved memories across agents")
	cmd.Flags().BoolVar(&syncFlag, "sync", false, "Synchronize memories across all agent providers")
	cmd.Flags().StringVar(&backupDir, "backup-dir", "", "Custom directory to save memory backups")

	// Also support subcommand 'ai memory sync'
	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Synchronize memories across all agent providers",
		RunE: func(c *cobra.Command, args []string) error {
			syncFlag = true
			return cmd.RunE(cmd, args)
		},
	}
	// Also support subcommand 'ai memory purge'
	purgeCmd := &cobra.Command{
		Use:   "purge",
		Short: "Purge saved memories across agents",
		RunE: func(c *cobra.Command, args []string) error {
			purgeFlag = true
			return cmd.RunE(cmd, args)
		},
	}
	cmd.AddCommand(syncCmd, purgeCmd)

	return cmd
}
