// Package ingest publishes conversation transcripts from every configured
// provider into a Qdrant collection so they stay searchable off this machine.
package ingest

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Environment variables that configure ingestion. Only EnvQdrantURL is
// required: without it the command is inert, which is what keeps `ai` usable
// on a machine that has no Qdrant to publish to.
const (
	EnvQdrantURL    = "AI_INGEST_QDRANT_URL"
	EnvQdrantAPIKey = "AI_INGEST_QDRANT_API_KEY"
	EnvCollection   = "AI_INGEST_COLLECTION"
	EnvHostname     = "AI_INGEST_HOSTNAME"
	EnvOffsetsFile  = "AI_INGEST_OFFSETS"
)

// DefaultCollection deliberately differs from the `ai_history` collection
// written by the retired TypeScript daemon: that one declares 1536-dimension
// vectors that were only ever filled with zeros, and this package writes
// payload-only points that such a collection would reject.
const DefaultCollection = "ai_history_v2"

// ErrNotConfigured reports that ingestion has no destination configured.
var ErrNotConfigured = errors.New("ingestion is not configured")

// Config is the resolved ingestion destination.
type Config struct {
	QdrantURL   *url.URL
	APIKey      string
	Collection  string
	Hostname    string
	OffsetsFile string
}

// Configured reports whether a destination has been set, without validating it.
func Configured() bool {
	return strings.TrimSpace(os.Getenv(EnvQdrantURL)) != ""
}

// LoadConfig resolves the ingestion configuration from the environment.
// It returns ErrNotConfigured when no destination is set, so callers can tell
// "switched off" apart from "misconfigured".
func LoadConfig() (*Config, error) {
	raw := strings.TrimSpace(os.Getenv(EnvQdrantURL))
	if raw == "" {
		return nil, ErrNotConfigured
	}

	endpoint, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", EnvQdrantURL, err)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("%s must be an http or https URL, got %q", EnvQdrantURL, raw)
	}
	if endpoint.Host == "" {
		return nil, fmt.Errorf("%s has no host: %q", EnvQdrantURL, raw)
	}

	collection := strings.TrimSpace(os.Getenv(EnvCollection))
	if collection == "" {
		collection = DefaultCollection
	}
	if strings.ContainsAny(collection, "/?#") {
		return nil, fmt.Errorf("%s must be a plain collection name, got %q", EnvCollection, collection)
	}

	hostname := strings.TrimSpace(os.Getenv(EnvHostname))
	if hostname == "" {
		hostname, err = os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("resolving hostname: %w", err)
		}
	}

	offsets := strings.TrimSpace(os.Getenv(EnvOffsetsFile))
	if offsets == "" {
		offsets, err = defaultOffsetsFile()
		if err != nil {
			return nil, err
		}
	}

	return &Config{
		QdrantURL:   endpoint,
		APIKey:      strings.TrimSpace(os.Getenv(EnvQdrantAPIKey)),
		Collection:  collection,
		Hostname:    hostname,
		OffsetsFile: offsets,
	}, nil
}

func defaultOffsetsFile() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving cache directory: %w", err)
	}
	return filepath.Join(cacheDir, "agent-cli", "ingest-offsets.json"), nil
}
