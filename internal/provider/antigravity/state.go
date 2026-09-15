package antigravity

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
)

// readVscdbB64Proto queries a key from the Antigravity IDE state.vscdb via
// sqlite3 and returns the outer protobuf bytes (already base64-decoded).
func readVscdbB64Proto(dbPath, key string) ([]byte, error) {
	var out bytes.Buffer
	cmd := exec.Command("sqlite3", dbPath, fmt.Sprintf("SELECT value FROM ItemTable WHERE key='%s';", key))
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	b64 := strings.TrimSpace(out.String())
	if b64 == "" {
		return nil, fmt.Errorf("key %q not found", key)
	}
	return base64.StdEncoding.DecodeString(b64)
}

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
