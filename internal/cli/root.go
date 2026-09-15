package cli

import (
	"errors"
	"io"

	"github.com/spf13/cobra"
)

var Version = "0.1.0-dev"

type options struct {
	format       string
	provider     string
	color        string
	withHeader   bool
	last         bool
	debug        bool
	maxColLength int
	timer        *Timer
}

func New(in io.Reader, out, errOut io.Writer) *cobra.Command {
	o := &options{
		format:       "auto",
		color:        "auto",
		withHeader:   true,
		debug:        IsDebugBuild,
		maxColLength: 100,
	}

	r := &cobra.Command{
		Use:           "ai",
		Short:         "Agent CLI: Universal toolkit for managing desktop AI agents (Claude, Antigravity, Codex)",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	r.SetIn(in)
	r.SetOut(out)
	r.SetErr(errOut)

	f := r.PersistentFlags()
	f.StringVarP(&o.format, "output", "o", "auto", "Output format: auto, table, json, compact, csv")
	f.StringVarP(&o.provider, "provider", "p", "", "Target agent provider (antigravity, claude, codex)")
	f.StringVar(&o.color, "color", "auto", "Colour output: auto, always, never")
	f.BoolVar(&o.withHeader, "with-header", true, "Include header row in tabular/CSV output")
	f.BoolVar(&o.last, "last", false, "Target only the most recent conversation")
	f.BoolVar(&o.debug, "debug", IsDebugBuild, "Enable debug execution mode and performance timing metrics")
	f.IntVar(&o.maxColLength, "max-column-length", 100, "Maximum width for table columns (-1 for uncapped)")

	r.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		o.timer = NewTimer(o.debug)
		o.timer.Step("startup_flags_parsed")

		switch o.color {
		case "auto", "always", "never":
		default:
			return errors.New("--color must be auto, always, or never")
		}

		switch o.format {
		case "auto", "table", "json", "compact", "csv":
			return nil
		default:
			return errors.New("--output must be auto, table, json, compact, or csv")
		}
	}

	r.PersistentPostRun = func(cmd *cobra.Command, args []string) {
		if o.timer != nil && o.timer.IsEnabled() {
			o.timer.Step("command_complete")
			o.timer.RenderFooter(cmd.OutOrStdout(), o)
		}
	}

	// Register subcommands
	r.AddCommand(
		statusCommand(o),
		historyCommand(o),
		conversationCommand(o),
		lastsCommand(o),
		logCommand(o),
		idCommand(o),
		handoffCommand(o),
		memoryCommand(o),
		skillCommand(o),
		mcpCommand(o),
		pluginsCommand(o),
		accountCommand(o),
		limitsCommand(o),
		modelsCommand(o),
		searchCommand(o),
	)

	return r
}
