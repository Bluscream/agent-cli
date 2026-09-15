// Package sqlite reads SQLite rows without delimiter-based text parsing.
package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func Query(path, query string, dest any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sqlite3", "-readonly", "-json", path, query)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("sqlite query: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if len(data) == 0 {
		data = []byte("[]")
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("decode sqlite rows: %w", err)
	}
	return nil
}
