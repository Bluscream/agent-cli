package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"agentcli.local/ai/internal/ingest"
	"agentcli.local/ai/internal/provider"
)

// purgeFlags are shared by every command that deletes conversations, so
// `ai conversation delete` and a later `ai cleanup` cannot drift in what
// --dry-run or --yes mean.
type purgeFlags struct {
	all        bool
	dryRun     bool
	yes        bool
	keepBackup bool
	remote     bool
	provider   string
}

func (f *purgeFlags) register(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&f.all, "all", false, "Delete every conversation of the selected providers")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Report exactly what would be deleted without deleting anything")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&f.keepBackup, "keep-backup", false, "Keep the database backup taken before editing a provider's conversation index")
	cmd.Flags().BoolVar(&f.remote, "remote", false, "Also delete the conversation's points from the ingestion collection")
	cmd.Flags().StringVarP(&f.provider, "provider", "p", "", "Only delete from one provider (antigravity, claude, codex)")
}

func (f *purgeFlags) options() provider.PurgeOptions {
	return provider.PurgeOptions{DryRun: f.dryRun, KeepBackup: f.keepBackup}
}

// runPurge is the whole delete flow: resolve the target, confirm, purge
// locally, then optionally remove the same conversations from the collection.
// Both `ai conversation delete` and any later cleanup command go through it.
func runPurge(cmd *cobra.Command, o *options, flags *purgeFlags, id string) error {
	if id == "" && !flags.all {
		return errors.New("give a conversation id, or --all to delete every conversation")
	}
	if id != "" && flags.all {
		return errors.New("give either a conversation id or --all, not both")
	}

	providerName := flags.provider
	if providerName == "" {
		providerName = o.provider
	}
	target := provider.PurgeTarget{Provider: providerName, ID: id}

	out := cmd.OutOrStdout()
	if !flags.dryRun && !flags.yes {
		confirmed, err := confirmDestructive(cmd.InOrStdin(), out, target.Describe(), flags.remote)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(out, "Nothing was deleted.")
			return nil
		}
	}

	summary, purgeErr := provider.RunPurge(target, flags.options())
	if summary == nil {
		return purgeErr
	}

	var remote *remotePurgeResult
	if flags.remote {
		result, err := purgeRemote(cmd, summary, flags.dryRun)
		if err != nil {
			// The local delete already happened; reporting it and then the
			// remote failure is more useful than discarding both outcomes.
			fmt.Fprintln(out, red.Sprintf("Remote cleanup failed: %v", err))
		}
		remote = result
	}

	if err := renderPurgeSummary(o, out, summary, remote); err != nil {
		return err
	}
	return purgeErr
}

// confirmDestructive asks before an irreversible delete. It requires a typed
// "yes" rather than accepting a bare Return, because the default for a question
// that erases transcripts must not be "go ahead".
func confirmDestructive(in io.Reader, out io.Writer, what string, alsoRemote bool) (bool, error) {
	scope := what
	if alsoRemote {
		scope += ", locally and from the ingestion collection"
	}
	fmt.Fprintf(out, "This permanently deletes %s.\n", scope)
	fmt.Fprint(out, "This cannot be undone. Type 'yes' to continue: ")

	reader := bufio.NewReader(in)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("reading confirmation: %w", err)
	}
	return strings.EqualFold(strings.TrimSpace(answer), "yes"), nil
}

func renderPurgeSummary(o *options, out io.Writer, summary *provider.PurgeSummary, remote *remotePurgeResult) error {
	if o.isJSON() {
		payload := struct {
			*provider.PurgeSummary
			Remote *remotePurgeResult `json:"remote,omitempty"`
		}{PurgeSummary: summary, Remote: remote}
		return o.printJSON(out, payload)
	}

	if len(summary.Purges) > 0 {
		t := o.newTable(out)
		t.AppendHeader(table.Row{"Provider", "ID", "Title", "Paths", "Rows", "Index", "Freed", "Status"})
		for _, purge := range summary.Purges {
			status := green.Sprint("deleted")
			if purge.DryRun {
				status = yellow.Sprint("would delete")
			}
			if !purge.Complete() {
				status = red.Sprint(strings.Join(purge.Warnings, "; "))
			}
			t.AppendRow(table.Row{
				purge.Provider,
				purge.ID,
				purge.Title,
				len(purge.RemovedPaths),
				purge.RemovedRows,
				purge.RemovedIndexEntries,
				humanBytes(purge.FreedBytes),
				status,
			})
		}
		fmt.Fprintln(out, o.renderTable(t))
	}

	verb := "Deleted"
	if summary.DryRun {
		verb = "Would delete"
	}
	fmt.Fprintf(out, "%s %d conversation(s), freeing %s", verb, summary.Removed, humanBytes(summary.FreedBytes))
	if remote != nil {
		fmt.Fprintf(out, "; %d remote point(s) in %s", remote.Points, remote.Collection)
	}
	fmt.Fprintf(out, " (%s)\n", summary.Duration)

	if summary.Failed > 0 {
		fmt.Fprintln(out, red.Sprintf("%d conversation(s) could not be fully deleted.", summary.Failed))
	}
	for _, name := range summary.Unsupported {
		fmt.Fprintln(out, yellow.Sprintf("%s cannot delete conversations; nothing was removed from it.", name))
	}
	return nil
}

// remotePurgeResult is what a local delete removed from the ingestion
// collection alongside it.
type remotePurgeResult struct {
	Collection string `json:"collection"`
	Endpoint   string `json:"endpoint"`
	Points     int64  `json:"points"`
	DryRun     bool   `json:"dry_run"`
}

// purgeRemote removes the same conversations from the ingestion collection.
//
// The sessions come from the local purge's own results, so exactly what was
// deleted here is what is deleted there — the two cannot be asked to delete
// different sets.
func purgeRemote(cmd *cobra.Command, summary *provider.PurgeSummary, dryRun bool) (*remotePurgeResult, error) {
	cfg, err := ingest.LoadConfig()
	if errors.Is(err, ingest.ErrNotConfigured) {
		return nil, fmt.Errorf("--remote needs %s to be set", ingest.EnvQdrantURL)
	}
	if err != nil {
		return nil, err
	}

	sessions := make([]string, 0, len(summary.Purges))
	for _, purge := range summary.Purges {
		if purge.ID != "" {
			sessions = append(sessions, purge.ID)
		}
	}
	if len(sessions) == 0 {
		return nil, nil
	}

	result, err := ingest.Cleanup(cmd.Context(), cfg, ingest.CleanupOptions{Sessions: sessions, DryRun: dryRun})
	if err != nil {
		return nil, err
	}
	return &remotePurgeResult{
		Collection: result.Collection,
		Endpoint:   result.Endpoint,
		Points:     result.Points,
		DryRun:     result.DryRun,
	}, nil
}

// conversationDeleteCommand is the delete subcommand of `ai conversation`.
// The flow it calls is shared, so a future `ai cleanup` behaves identically.
func conversationDeleteCommand(o *options) *cobra.Command {
	flags := &purgeFlags{}
	cmd := &cobra.Command{
		Use:     "delete [conversation-id]",
		Aliases: []string{"rm", "wipe", "purge"},
		Short:   "Permanently delete a conversation's stored data",
		Long: `Erase a conversation from the agent that stores it.

Everything local goes: the transcript, the provider's own index entry, the
per-conversation database and any artifacts. A conversation deleted without its
index entry would still be listed and open to nothing, so a provider that
cannot remove all of it reports which part it could not.

Without --yes you are asked to confirm, and --dry-run reports exactly what would
be removed without removing it. --remote additionally deletes the same
conversation's points from the ingestion collection.

Examples:
  ai conversation delete 76cd6d92                 # one conversation
  ai conversation delete 76cd6d92 --remote        # and its stored points
  ai conversation delete --all -p codex           # every Codex conversation
  ai conversation delete --all --dry-run          # preview a full wipe
  ai conversation delete --all --remote -y        # wipe everything, no prompt`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) > 0 {
				id = args[0]
			}
			return runPurge(cmd, o, flags, id)
		},
	}
	flags.register(cmd)
	return cmd
}
