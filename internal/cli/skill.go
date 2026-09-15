package cli

import (
	"fmt"

	"agentcli.local/ai/internal/provider"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func skillCommand(o *options) *cobra.Command {
	var targetProvider string
	var purgeFlag bool
	var syncFlag bool

	cmd := &cobra.Command{
		Use:     "skill [name]",
		Aliases: []string{"skills"},
		Short:   "Explore, manage, purge, and synchronize skills across all AI agents",
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

			// Handle --purge
			if purgeFlag {
				for _, p := range provList {
					count, err := p.PurgeSkills()
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "Failed to purge %s skills: %v\n", p.DisplayName(), err)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "[*] Purged %d custom skills from %s\n", count, p.DisplayName())
					}
				}
				return nil
			}

			// Handle --sync
			if syncFlag {
				var allSkills []provider.SkillItem
				for _, p := range provider.All() {
					skills, err := p.ListSkills()
					if err == nil {
						allSkills = append(allSkills, skills...)
					}
				}
				if len(allSkills) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No skills found across providers to synchronize.")
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "[*] Found %d total skills across agents to synchronize.\n", len(allSkills))
				for _, target := range provList {
					imported := 0
					for _, s := range allSkills {
						if s.Provider == target.Name() || s.Path == "" {
							continue
						}
						if err := target.ImportSkill(s.Path); err == nil {
							imported++
						}
					}
					fmt.Fprintf(cmd.OutOrStdout(), "[+] Synchronized %d skills into %s\n", imported, target.DisplayName())
				}
				return nil
			}

			var allSkills []provider.SkillItem
			for _, p := range provList {
				skills, err := p.ListSkills()
				if err == nil {
					allSkills = append(allSkills, skills...)
				}
			}

			if o.isJSON() {
				return o.printJSON(cmd.OutOrStdout(), allSkills)
			}

			if len(allSkills) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No skills detected.")
				return nil
			}

			out := cmd.OutOrStdout()
			t := o.newTable(out)
			t.AppendHeader(table.Row{"Provider", "Skill Name", "Type", "Rules", "Scripts", "Description"})
			// Fixed cols: provider (~12) + skill name (~24) + type (~8) + rules (~5)
			// + scripts (~8) + chrome (~15) = ~72. Description gets the rest.
			t.SetColumnConfigs([]table.ColumnConfig{o.flexColConfig(out, 6, 72)})

			for _, s := range allSkills {
				typeStr := "Custom"
				if s.IsBuiltin {
					typeStr = "Builtin"
				}

				t.AppendRow(table.Row{
					s.Provider,
					s.Name,
					typeStr,
					s.RulesCount,
					s.ScriptsCount,
					s.Description,
				})
			}

			fmt.Fprintln(cmd.OutOrStdout(), o.renderTable(t))
			return nil
		},
	}

	cmd.Flags().StringVarP(&targetProvider, "provider", "p", "", "Target provider (antigravity, claude, codex)")
	cmd.Flags().BoolVar(&purgeFlag, "purge", false, "Purge custom skills across agents")
	cmd.Flags().BoolVar(&syncFlag, "sync", false, "Synchronize custom skills across all agent providers")

	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Synchronize skills across all agent providers",
		RunE: func(c *cobra.Command, args []string) error {
			syncFlag = true
			return cmd.RunE(cmd, args)
		},
	}
	purgeCmd := &cobra.Command{
		Use:   "purge",
		Short: "Purge custom skills across agents",
		RunE: func(c *cobra.Command, args []string) error {
			purgeFlag = true
			return cmd.RunE(cmd, args)
		},
	}
	cmd.AddCommand(syncCmd, purgeCmd)

	return cmd
}
