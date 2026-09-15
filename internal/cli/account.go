package cli

import (
	"fmt"
	"strings"

	"agentcli.local/ai/internal/provider"
	"agentcli.local/ai/internal/provider/antigravity"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func accountCommand(o *options) *cobra.Command {
	var targetProvider string
	cmd := &cobra.Command{
		Use:     "account",
		Aliases: []string{"accounts", "profile", "profiles"},
		Short:   "Manage and inspect user accounts, sessions, and switchable profiles across agents",
		Long: `Displays active accounts currently configured/logged in across all providers,
as well as inactive saved profiles that can be switched to.

Antigravity profiles can be switched using 'ai account switch <name>'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listAccounts(o, cmd, targetProvider)
		},
	}

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all active accounts and saved agent profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			return listAccounts(o, cmd, targetProvider)
		},
	}

	saveCmd := &cobra.Command{
		Use:   "save [profile-name]",
		Short: "Capture current active session tokens and create/update desktop shortcut",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			selected := targetProvider
			if selected == "" {
				selected = o.provider
			}
			if selected != "" {
				p, err := provider.Get(selected)
				if err != nil {
					return err
				}
				if p.Name() != "antigravity" {
					return fmt.Errorf("account mutation is only supported for antigravity")
				}
			}

			profileName := args[0]
			if err := antigravity.SaveProfile(profileName); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[+] Saved active session as profile %q and updated desktop shortcut.\n", profileName)
			return nil
		},
	}

	switchCmd := &cobra.Command{
		Use:   "switch [profile-name]",
		Short: "Switch to a saved profile and restart the agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			selected := targetProvider
			if selected == "" {
				selected = o.provider
			}
			if selected != "" {
				p, err := provider.Get(selected)
				if err != nil {
					return err
				}
				if p.Name() != "antigravity" {
					return fmt.Errorf("account mutation is only supported for antigravity")
				}
			}

			profileName := args[0]
			fmt.Fprintf(cmd.OutOrStdout(), "[*] Switching to profile %q...\n", profileName)
			if err := antigravity.SwitchProfile(profileName); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[+] Successfully switched to profile %q and launched Antigravity IDE.\n", profileName)
			return nil
		},
	}

	freshCmd := &cobra.Command{
		Use:   "fresh",
		Short: "Clear active session keys from DB and launch a clean, unauthenticated session",
		RunE: func(cmd *cobra.Command, args []string) error {
			selected := targetProvider
			if selected == "" {
				selected = o.provider
			}
			if selected != "" {
				p, err := provider.Get(selected)
				if err != nil {
					return err
				}
				if p.Name() != "antigravity" {
					return fmt.Errorf("account mutation is only supported for antigravity")
				}
			}

			fmt.Fprintln(cmd.OutOrStdout(), "[*] Clearing session keys and launching fresh instance...")
			if err := antigravity.FreshSession(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "[+] Launched clean session.")
			return nil
		},
	}

	cmd.PersistentFlags().StringVarP(&targetProvider, "provider", "p", "", "Filter accounts by agent provider (antigravity, claude, codex)")
	cmd.AddCommand(listCmd, saveCmd, switchCmd, freshCmd)
	return cmd
}

func listAccounts(o *options, cmd *cobra.Command, targetProvider string) error {
	var provList []provider.Provider
	filter := targetProvider
	if filter == "" {
		filter = o.provider
	}

	if filter != "" {
		p, err := provider.Get(filter)
		if err != nil {
			return err
		}
		provList = []provider.Provider{p}
	} else {
		provList = provider.All()
	}

	var allAccounts []provider.AccountInfo
	for _, p := range provList {
		accs, err := p.GetAccounts()
		if err == nil {
			allAccounts = append(allAccounts, accs...)
		}
	}

	if o.isJSON() {
		return o.printJSON(cmd.OutOrStdout(), allAccounts)
	}

	out := cmd.OutOrStdout()
	if len(allAccounts) == 0 {
		fmt.Fprintln(out, "No accounts or saved profiles found.")
		return nil
	}

	t := o.newTable(out)
	t.AppendHeader(table.Row{"Provider", "Account / Profile", "Email", "Plan", "Active In", "Status"})
	// Fixed: provider(~12) + account(~26) + email(~26) + plan(~16) + active_in(~18) + status(~12) = ~110
	t.SetColumnConfigs([]table.ColumnConfig{o.flexColConfig(out, 2, 110)})

	for _, a := range allAccounts {
		nameCell := a.DisplayName
		idStr := a.ID
		if len(idStr) > 12 {
			idStr = idStr[:8]
		}
		if idStr != "" && idStr != "active" && !strings.Contains(a.DisplayName, idStr) {
			nameCell = fmt.Sprintf("%s (%s)", a.DisplayName, idStr)
		}

		emailCell := a.Email
		if emailCell == "" {
			emailCell = "-"
		}

		planCell := a.Plan
		if planCell == "" {
			planCell = "-"
		}

		activeInCell := a.ActiveIn
		if activeInCell == "" {
			activeInCell = "-"
		}

		statusStr := "ACTIVE"
		if !a.IsActive {
			statusStr = "AVAILABLE"
		}

		t.AppendRow(table.Row{
			a.Provider,
			nameCell,
			emailCell,
			planCell,
			activeInCell,
			colorStatus(statusStr),
		})
	}

	fmt.Fprintln(out, o.renderTable(t))
	fmt.Fprintln(out, faint("  Switch profile: ai account switch <name>  |  Save current: ai account save <name>  |  Fresh: ai account fresh"))
	return nil
}
