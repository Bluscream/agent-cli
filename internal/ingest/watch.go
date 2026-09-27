package ingest

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"agentcli.local/ai/internal/provider"
	"github.com/fsnotify/fsnotify"
)

// WatchRoots returns the transcript directories of every selected provider that
// can name them. Providers without the capability are skipped.
func WatchRoots(providerName string) ([]string, error) {
	providers, err := selectProviders(providerName)
	if err != nil {
		return nil, err
	}

	var roots []string
	for _, p := range providers {
		rooter, ok := p.(provider.TranscriptRooter)
		if !ok {
			continue
		}
		roots = append(roots, rooter.TranscriptRoots()...)
	}
	return roots, nil
}

// Watcher publishes transcripts as they change.
type Watcher struct {
	cfg      *Config
	opts     Options
	debounce time.Duration
	notify   *fsnotify.Watcher
	// OnPass runs after each ingestion pass, including the initial one.
	OnPass func(*Result, error)
}

// NewWatcher builds a watcher over every transcript directory it can find.
func NewWatcher(cfg *Config, opts Options, debounce time.Duration) (*Watcher, error) {
	if debounce <= 0 {
		return nil, fmt.Errorf("debounce must be positive, got %s", debounce)
	}

	roots, err := WatchRoots(opts.Provider)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("no transcript directories found to watch")
	}

	notify, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("creating file watcher: %w", err)
	}

	w := &Watcher{cfg: cfg, opts: opts, debounce: debounce, notify: notify}
	for _, root := range roots {
		if err := w.addTree(root); err != nil {
			notify.Close()
			return nil, err
		}
	}
	return w, nil
}

// addTree watches a directory and every directory beneath it, because inotify
// reports only the directory it was given.
func (w *Watcher) addTree(root string) error {
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// A directory that vanished mid-walk is not fatal.
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if addErr := w.notify.Add(path); addErr != nil {
			return fmt.Errorf("watching %s: %w", path, addErr)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// Close releases the underlying watcher.
func (w *Watcher) Close() error {
	return w.notify.Close()
}

// Run ingests once, then again whenever transcripts change, until ctx is done.
// Bursts of writes are coalesced into a single pass: a transcript is appended to
// on every turn, and ingesting per write would republish the same conversation
// dozens of times a minute.
func (w *Watcher) Run(ctx context.Context) error {
	w.pass(ctx)

	// A stopped timer that has not fired keeps the select arm inert until an
	// event schedules a pass.
	timer := time.NewTimer(w.debounce)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	pending := false

	for {
		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-w.notify.Events:
			if !ok {
				return nil
			}
			// A new conversation arrives as a new directory; start watching it
			// or its transcript writes would never be seen.
			if event.Has(fsnotify.Create) {
				if entry, err := os.Stat(event.Name); err == nil && entry.IsDir() {
					_ = w.addTree(event.Name)
				}
			}
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
				continue
			}
			if !pending {
				pending = true
			} else if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(w.debounce)

		case <-timer.C:
			pending = false
			w.pass(ctx)

		case err, ok := <-w.notify.Errors:
			if !ok {
				return nil
			}
			if w.OnPass != nil {
				w.OnPass(nil, fmt.Errorf("watch error: %w", err))
			}
		}
	}
}

func (w *Watcher) pass(ctx context.Context) {
	result, err := Run(ctx, w.cfg, w.opts)
	if w.OnPass != nil {
		w.OnPass(result, err)
	}
}
