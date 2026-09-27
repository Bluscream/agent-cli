package ingest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// offsetRecord remembers what was last published for one conversation.
// Conversations are keyed by provider and ID rather than by transcript path,
// because not every provider exposes a path and a conversation can move.
type offsetRecord struct {
	UpdatedAt   time.Time `json:"updated_at"`
	SizeBytes   int64     `json:"size_bytes"`
	Points      int       `json:"points"`
	IngestedAt  time.Time `json:"ingested_at"`
	Collection  string    `json:"collection"`
	ContentHash string    `json:"content_hash,omitempty"`
}

// offsetStore is the on-disk record of what has already been published.
type offsetStore struct {
	path    string
	Records map[string]offsetRecord `json:"records"`
}

func offsetKey(providerName, conversationID string) string {
	return providerName + "\x00" + conversationID
}

// loadOffsets reads the store, treating an unreadable or corrupt file as empty
// so a damaged cache costs a re-ingest rather than failing the command.
func loadOffsets(path string) *offsetStore {
	store := &offsetStore{path: path, Records: map[string]offsetRecord{}}

	data, err := os.ReadFile(path)
	if err != nil {
		return store
	}

	var onDisk struct {
		Records map[string]offsetRecord `json:"records"`
	}
	if err := json.Unmarshal(data, &onDisk); err != nil || onDisk.Records == nil {
		return store
	}
	store.Records = onDisk.Records
	return store
}

// upToDate reports whether the conversation has already been published in its
// current state, into this same collection.
func (s *offsetStore) upToDate(key, collection string, updatedAt time.Time, size int64) bool {
	record, ok := s.Records[key]
	if !ok || record.Collection != collection {
		return false
	}
	return record.UpdatedAt.Equal(updatedAt) && record.SizeBytes == size
}

func (s *offsetStore) record(key, collection string, updatedAt time.Time, size int64, points int) {
	s.Records[key] = offsetRecord{
		UpdatedAt:  updatedAt,
		SizeBytes:  size,
		Points:     points,
		IngestedAt: time.Now().UTC(),
		Collection: collection,
	}
}

// save writes the store atomically so an interrupted run cannot leave a
// truncated file that silently forces a full re-ingest.
func (s *offsetStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("creating offset directory: %w", err)
	}

	encoded, err := json.MarshalIndent(struct {
		Records map[string]offsetRecord `json:"records"`
	}{Records: s.Records}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding offsets: %w", err)
	}

	temp, err := os.CreateTemp(filepath.Dir(s.path), ".ingest-offsets-*")
	if err != nil {
		return fmt.Errorf("creating temporary offset file: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		_ = os.Remove(tempName)
	}()

	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return fmt.Errorf("writing offsets: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing offsets: %w", err)
	}
	if err := os.Chmod(tempName, 0o600); err != nil {
		return fmt.Errorf("securing offsets: %w", err)
	}
	if err := os.Rename(tempName, s.path); err != nil {
		return fmt.Errorf("replacing offsets: %w", err)
	}
	return nil
}
