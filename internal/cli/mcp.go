package cli

import (
	"fmt"
	"strings"

	"agentcli.local/ai/internal/mcp"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
)

func mcpCommand(o *options) *cobra.Command {
	mgr := mcp.NewManager()

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage and synchronize MCP (Model Context Protocol) servers across all IDEs and agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Default to list
			return listMCPServers(o, mgr, cmd)
		},
	}

	// Subcommands: list, add, remove, enable, disable, sync
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all configured MCP servers across all agent config files",
		RunE: func(cmd *cobra.Command, args []string) error {
			return listMCPServers(o, mgr, cmd)
		},
	}

	var addName, addCmd, addURL, addToken string
	var addArgs, addEnv []string

	addCmdObj := &cobra.Command{
		Use:   "add",
		Short: "Add or update an MCP server across all agent configuration files",
		RunE: func(cmd *cobra.Command, args []string) error {
			if addName == "" {
				return fmt.Errorf("--name is required")
			}
			cfg := mcp.ServerConfig{}
			if addURL != "" {
				cfg.URL = addURL
				if addToken != "" {
					cfg.Headers = map[string]string{"Authorization": "Bearer " + addToken}
				}
			} else if addCmd != "" {
				cfg.Command = addCmd
				cfg.Args = addArgs
				if len(addEnv) > 0 {
					cfg.Env = make(map[string]string)
					for _, pair := range addEnv {
						p := strings.SplitN(pair, "=", 2)
						if len(p) == 2 {
							cfg.Env[p[0]] = p[1]
						}
					}
				}
			} else {
				return fmt.Errorf("either --command or --url must be provided")
			}

			updated, err := mgr.Add(addName, cfg)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[+] Added MCP server %q to %d configuration files:\n", addName, len(updated))
			for _, f := range updated {
				fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", f)
			}
			return nil
		},
	}

	addCmdObj.Flags().StringVar(&addName, "name", "", "MCP server name")
	addCmdObj.Flags().StringVar(&addCmd, "command", "", "Executable command (e.g. node, npx, python)")
	addCmdObj.Flags().StringSliceVar(&addArgs, "args", nil, "Command arguments")
	addCmdObj.Flags().StringSliceVar(&addEnv, "env", nil, "Environment variables in KEY=VAL format")
	addCmdObj.Flags().StringVar(&addURL, "url", "", "HTTP/SSE URL")
	addCmdObj.Flags().StringVar(&addToken, "token", "", "Optional Bearer auth token")

	removeCmd := &cobra.Command{
		Use:   "remove [server-name]",
		Short: "Remove an MCP server from all agent configurations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			updated, err := mgr.Remove(name)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[*] Removed MCP server %q from %d files.\n", name, len(updated))
			return nil
		},
	}

	enableCmd := &cobra.Command{
		Use:   "enable [server-name]",
		Short: "Enable an MCP server across all configurations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			updated, err := mgr.SetDisabled(name, false)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[+] Enabled MCP server %q in %d files.\n", name, len(updated))
			return nil
		},
	}

	disableCmd := &cobra.Command{
		Use:   "disable [server-name]",
		Short: "Disable an MCP server across all configurations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			updated, err := mgr.SetDisabled(name, true)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[-] Disabled MCP server %q in %d files.\n", name, len(updated))
			return nil
		},
	}

	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Synchronize the union of all MCP servers across all IDE and agent configuration files",
		RunE: func(cmd *cobra.Command, args []string) error {
			synced, err := mgr.Sync()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[+] Successfully synchronized MCP servers across %d configuration files:\n", len(synced))
			for _, f := range synced {
				fmt.Fprintf(cmd.OutOrStdout(), "  • %s\n", f)
			}
			return nil
		},
	}

	cmd.AddCommand(listCmd, addCmdObj, removeCmd, enableCmd, disableCmd, syncCmd)
	return cmd
}

func listMCPServers(o *options, mgr *mcp.Manager, cmd *cobra.Command) error {
	servers, err := mgr.List()
	if err != nil {
		return err
	}
	o.timer.Step("mcp_configs_loaded")

	if o.isJSON() {
		return o.printJSON(cmd.OutOrStdout(), servers)
	}

	out := cmd.OutOrStdout()
	t := o.newTable(out)
	t.AppendHeader(table.Row{"Server Name", "Status", "Target / Command", "Registered In"})
	// Fixed cols: name (~15) + status (~8) + count (~14) + chrome (~13) = ~50.
	t.SetColumnConfigs([]table.ColumnConfig{o.flexColConfig(out, 3, 50)})

	for _, s := range servers {
		statusStr := colorStatus("enabled")
		if s.Disabled {
			statusStr = colorStatus("disabled")
		}

		target := s.Config.Command
		if len(s.Config.Args) > 0 {
			target += " " + strings.Join(s.Config.Args, " ")
		}
		if s.Config.URL != "" {
			target = s.Config.URL
		}

		t.AppendRow(table.Row{
			s.Name,
			statusStr,
			target,
			len(s.Files),
		})
	}

	fmt.Fprintln(cmd.OutOrStdout(), o.renderTable(t))
	return nil
}
