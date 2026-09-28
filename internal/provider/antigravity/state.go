package antigravity

import (
	"encoding/base64"
	"fmt"
	"strings"

	"agentcli.local/ai/internal/sqlite"
)

// readVscdbB64Proto reads a key from the Antigravity IDE state.vscdb and
// returns the outer protobuf bytes (already base64-decoded). This is the only
// reader for that table: ListConversations and Recover both go through it
// rather than issuing the same select again.
func readVscdbB64Proto(dbPath, key string) ([]byte, error) {
	rows, err := sqlite.Scalar(dbPath, "SELECT value FROM ItemTable WHERE key = ?;", key)
	if err != nil {
		return nil, err
	}
	var b64 string
	if len(rows) > 0 {
		b64 = strings.TrimSpace(rows[0])
	}
	if b64 == "" {
		return nil, fmt.Errorf("key %q not found in %s", key, dbPath)
	}
	return base64.StdEncoding.DecodeString(b64)
}

// trajectorySummariesKey holds the map of conversation id to trajectory
// summary. Named once so the three readers of it cannot drift.
const trajectorySummariesKey = "antigravityUnifiedStateSync.trajectorySummaries"

// extractSentinelMap extracts a map[key]→inner-proto-bytes from the
// antigravityUnifiedStateSync.* style protobuf format:
//
//	outer: field1 = repeated { field1=string_key, field2=bytes{ field1=b64_value } }
//
// The b64_value is itself decoded and returned as raw bytes.
func extractSentinelMap(outerBytes []byte) map[string][]byte {
	result := make(map[string][]byte)
	for _, f := range parseMsg(outerBytes) {
		if f.fieldNum != 1 || f.wireType != 2 {
			continue
		}
		var key string
		var valBytes []byte
		for _, sf := range parseMsg(f.data) {
			switch {
			case sf.fieldNum == 1 && sf.wireType == 2:
				key = string(sf.data)
			case sf.fieldNum == 2 && sf.wireType == 2:
				// inner value: field1 = b64-encoded proto
				for _, vf := range parseMsg(sf.data) {
					if vf.fieldNum == 1 && vf.wireType == 2 {
						decoded, err := base64.StdEncoding.DecodeString(string(vf.data))
						if err == nil {
							valBytes = decoded
						}
					}
				}
			}
		}
		if key != "" {
			result[key] = valBytes
		}
	}
	return result
}
