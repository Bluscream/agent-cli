package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"agentcli.local/ai/internal/provider"
	"agentcli.local/ai/internal/provider/claude"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func pluginsCommand(o *options) *cobra.Command {
	var targetProvider string

	cmd := &cobra.Command{
		Use:     "plugins",
		Aliases: []string{"plugin"},
		Short:   "Manage universal plugin injection, ASAR patching, extensions, and helper macros across agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			return listPluginsCmd(o, targetProvider, cmd)
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all installed plugins and extensions across all agent providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return listPluginsCmd(o, targetProvider, cmd)
		},
	}

	installCmd := &cobra.Command{
		Use:   "install <path-or-dir>",
		Short: "Install a plugin, extension, or script into an agent provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			provName := targetProvider
			if provName == "" {
				provName = o.provider
			}
			if provName == "" {
				return fmt.Errorf("--provider is required for plugin installation (claude, antigravity, codex)")
			}
			p, err := provider.Get(provName)
			if err != nil {
				return err
			}
			if err := p.InstallPlugin(src); err != nil {
				return fmt.Errorf("failed to install plugin into %s: %w", p.DisplayName(), err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[+] Successfully installed plugin %q into %s.\n", filepath.Base(src), p.DisplayName())
			return nil
		},
	}

	uninstallCmd := &cobra.Command{
		Use:   "uninstall <name>",
		Short: "Uninstall/remove a plugin or extension from an agent provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			provName := targetProvider
			if provName == "" {
				provName = o.provider
			}
			if provName == "" {
				return fmt.Errorf("--provider is required for plugin uninstallation (claude, antigravity, codex)")
			}
			p, err := provider.Get(provName)
			if err != nil {
				return err
			}
			if err := p.UninstallPlugin(name); err != nil {
				return fmt.Errorf("failed to uninstall plugin from %s: %w", p.DisplayName(), err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[+] Successfully removed plugin %q from %s.\n", name, p.DisplayName())
			return nil
		},
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show plugin loader injection status and helper macro activity",
		RunE: func(cmd *cobra.Command, args []string) error {
			return showPluginsStatus(o, cmd)
		},
	}

	var appImagePath string
	patchCmd := &cobra.Command{
		Use:   "patch",
		Short: "Inject the universal plugin loader hook into Claude Desktop's AppImage ASAR bundle",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "[*] Launching Claude Desktop AppImage patcher...")
			out, err := claude.RunPatcher(appImagePath)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
	patchCmd.Flags().StringVar(&appImagePath, "appimage", "", "Custom path to Claude Desktop AppImage")

	restoreCmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore Claude Desktop AppImage from its .bak backup",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := claude.RestoreBackup(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "[+] Restored original unpatched Claude Desktop AppImage.")
			return nil
		},
	}

	nudgeCmd := &cobra.Command{
		Use:   "nudge [start|stop|status]",
		Short: "Control the Claude auto-nudge macro background service",
		RunE: func(cmd *cobra.Command, args []string) error {
			action := "status"
			if len(args) > 0 {
				action = args[0]
			}

			switch action {
			case "start":
				if err := claude.StartNudgeMacro(); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "[+] Claude auto-nudge macro started in background.")
			case "stop":
				if err := claude.StopNudgeMacro(); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "[*] Stopped Claude auto-nudge macro.")
			default:
				nst := claude.CheckNudgeStatus()
				if o.isJSON() {
					return o.printJSON(cmd.OutOrStdout(), nst)
				}
				dt := o.newDetail(cmd.OutOrStdout())
				detailRows(dt,
					kv("Script Path", nst.ScriptPath),
					kv("Installed", colorBool(nst.Exists)),
					kv("Running", colorBool(nst.Running)),
					kv("PID(s)", fmt.Sprintf("%v", nst.PIDs)),
				)
				fmt.Fprintln(cmd.OutOrStdout(), o.renderTable(dt))
			}
			return nil
		},
	}

	cmd.PersistentFlags().StringVarP(&targetProvider, "provider", "p", "", "Target provider (antigravity, claude, codex)")
	cmd.AddCommand(listCmd, installCmd, uninstallCmd, statusCmd, patchCmd, restoreCmd, nudgeCmd)
	return cmd
}

func listPluginsCmd(o *options, targetProvider string, cmd *cobra.Command) error {
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

	var allPlugins []provider.PluginItem
	for _, p := range provList {
		plugins, err := p.ListPlugins()
		if err == nil {
			allPlugins = append(allPlugins, plugins...)
		}
	}

	if o.isJSON() {
		return o.printJSON(cmd.OutOrStdout(), allPlugins)
	}

	if len(allPlugins) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No plugins or extensions found.")
		return nil
	}

	out := cmd.OutOrStdout()
	t := o.newTable(out)
	t.AppendHeader(table.Row{"Provider", "Plugin Name", "Type", "Status", "Path", "Description"})
	// Fixed columns overhead: Provider (~13), Type (~21), Status (~11), 7 borders (7),
	// plus 2-space padding for the 3 flexible columns (6).
	// Total fixed overhead: 58.
	t.SetColumnConfigs(o.distributeFlexCols(out, 58,
		FlexColSpec{Number: 2, MinWidth: 15, Ratio: 2}, // Plugin Name
		FlexColSpec{Number: 5, MinWidth: 20, Ratio: 3}, // Path
		FlexColSpec{Number: 6, MinWidth: 20, Ratio: 4}, // Description
	))

	for _, p := range allPlugins {
		t.AppendRow(table.Row{
			p.Provider,
			p.Name,
			p.Type,
			colorStatus(p.Status),
			p.Path,
			p.Description,
		})
	}

	fmt.Fprintln(cmd.OutOrStdout(), o.renderTable(t))
	return nil
}

func showPluginsStatus(o *options, cmd *cobra.Command) error {
	pst := claude.CheckPatchStatus()
	nst := claude.CheckNudgeStatus()

	home, _ := os.UserHomeDir()
	codexPluginsDir := filepath.Join(home, ".codex/plugins")
	antigravityPluginsDir := filepath.Join(home, ".gemini/config/plugins")

	codexCount := 0
	if entries, err := os.ReadDir(codexPluginsDir); err == nil {
		codexCount = len(entries)
	}

	antigravityCount := 0
	if entries, err := os.ReadDir(antigravityPluginsDir); err == nil {
		antigravityCount = len(entries)
	}

	statusObj := map[string]any{
		"claude_patch":        pst,
		"claude_nudge":        nst,
		"codex_plugins_count": codexCount,
		"antigravity_plugins": antigravityCount,
	}

	if o.isJSON() {
		return o.printJSON(cmd.OutOrStdout(), statusObj)
	}

	t := o.newTable(cmd.OutOrStdout())
	t.AppendHeader(table.Row{"Component", "Status", "Path", "Notes"})
	// Component (~20) + Status (~15) + Notes (~30) + table borders/padding (~15) = ~80
	t.SetColumnConfigs([]table.ColumnConfig{
		o.flexColConfig(cmd.OutOrStdout(), 3, 80),
	})

	patchStatusStr := colorStatus("not installed")
	if pst.IsPatched {
		patchStatusStr = colorStatus("installed")
	}

	nudgeStatusStr := colorStatus("stopped")
	if nst.Running {
		nudgeStatusStr = colorStatus("running")
	}

	t.AppendRow(table.Row{
		"Claude ASAR Hook",
		patchStatusStr,
		pst.AppImagePath,
		fmt.Sprintf("Backup exists: %v", pst.HasBackup),
	})
	t.AppendRow(table.Row{
		"Claude Plugins Dir",
		colorStatus("ok"),
		pst.PluginsDir,
		fmt.Sprintf("Loader script exists: %v", pst.LoaderExists),
	})
	t.AppendRow(table.Row{
		"Claude Auto-Nudge",
		nudgeStatusStr,
		nst.ScriptPath,
		fmt.Sprintf("PIDs: %v", nst.PIDs),
	})
	t.AppendRow(table.Row{
		"Codex Plugins",
		colorStatus("ok"),
		codexPluginsDir,
		fmt.Sprintf("%d extensions/plugins", codexCount),
	})
	t.AppendRow(table.Row{
		"Antigravity Plugins",
		colorStatus("ok"),
		antigravityPluginsDir,
		fmt.Sprintf("%d extensions/plugins", antigravityCount),
	})

	fmt.Fprintln(cmd.OutOrStdout(), o.renderTable(t))
	return nil
}
