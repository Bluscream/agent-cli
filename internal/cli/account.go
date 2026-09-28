package cli

import (
	"fmt"
	"strings"

	"agentcli.local/ai/internal/idutil"
	"agentcli.local/ai/internal/provider"
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

Antigravity and Claude profiles can be switched using 'ai account switch <name>' (or with '-p claude').`,
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
			return runProfileAction(cmd, o, targetProvider, args[0],
				func(m provider.ProfileManager, name string) error { return m.SaveProfile(name) },
				"Saved active %s session as profile %q and updated the desktop launcher.\n")
		},
	}

	switchCmd := &cobra.Command{
		Use:   "switch [profile-name]",
		Short: "Switch to a saved profile and restart the agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProfileAction(cmd, o, targetProvider, args[0],
				func(m provider.ProfileManager, name string) error { return m.SwitchProfile(name) },
				"Switched %s to profile %q and relaunched it.\n")
		},
	}

	freshCmd := &cobra.Command{
		Use:   "fresh",
		Short: "Clear active session keys from DB and launch a clean, unauthenticated session",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProfileAction(cmd, o, targetProvider, "",
				func(m provider.ProfileManager, _ string) error { return m.FreshSession() },
				"Cleared the %s session and launched a clean instance.%.0s\n")
		},
	}

	cmd.PersistentFlags().StringVarP(&targetProvider, "provider", "p", "", "Filter accounts by agent provider (antigravity, claude, codex)")
	cmd.AddCommand(listCmd, saveCmd, switchCmd, freshCmd)
	return cmd
}

// runProfileAction resolves which provider to act on and applies one profile
// operation to it.
//
// Resolution lives in the provider package: this used to compare provider names
// against a hardcoded pair, reconstruct each provider's on-disk profile layout
// to find which one held a name, and silently default to Antigravity when the
// answer was unclear — including when the user had asked for Codex.
func runProfileAction(
	cmd *cobra.Command,
	o *options,
	targetProvider, profileName string,
	apply func(provider.ProfileManager, string) error,
	successFormat string,
) error {
	name := targetProvider
	if name == "" {
		name = o.provider
	}
	p, manager, err := provider.ResolveProfileManager(name, profileName)
	if err != nil {
		return err
	}
	if err := apply(manager, profileName); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), successFormat, p.DisplayName(), profileName)
	return nil
}
func listAccounts(o *options, cmd *cobra.Command, targetProvider string) error {
	var provList []provider.Provider
	filter := targetProvider
	if filter == "" {
		filter = o.provider
	}

	provList, err := provider.Select(filter)
	if err != nil {
		return err
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
		// One scheme for the whole table. The previous rule printed an id of
		// 9 to 12 characters in full and cut a 13-character one to 8.
		idStr := a.ID
		if idStr != "" && idStr != "active" {
			idStr = idutil.ShortID(a.ID)
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
	fmt.Fprintln(out, faint("  Switch profile: ai account switch <name> [-p claude]  |  Save current: ai account save <name> [-p claude]  |  Fresh: ai account fresh [-p claude]"))
	return nil
}
