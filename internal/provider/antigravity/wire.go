package antigravity

import (
	"bytes"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
)

type wireField struct {
	fieldNum int
	wireType int
	varint   uint64
	data     []byte
}

func parseMsg(data []byte) []wireField {
	var fields []wireField
	i := 0
	for i < len(data) {
		var tag uint64
		var shift uint
		for {
			if i >= len(data) {
				return fields
			}
			b := data[i]
			i++
			tag |= uint64(b&0x7f) << shift
			shift += 7
			if b&0x80 == 0 {
				break
			}
		}
		fn := int(tag >> 3)
		wt := int(tag & 7)

		if wt == 0 { // varint
			var val uint64
			var s uint
			for {
				if i >= len(data) {
					return fields
				}
				b := data[i]
				i++
				val |= uint64(b&0x7f) << s
				s += 7
				if b&0x80 == 0 {
					break
				}
			}
			fields = append(fields, wireField{fieldNum: fn, wireType: wt, varint: val})
		} else if wt == 2 { // length-delimited
			var length uint64
			var s uint
			for {
				if i >= len(data) {
					return fields
				}
				b := data[i]
				i++
				length |= uint64(b&0x7f) << s
				s += 7
				if b&0x80 == 0 {
					break
				}
			}
			if length > uint64(len(data)-i) {
				break
			}
			sub := data[i : i+int(length)]
			i += int(length)
			fields = append(fields, wireField{fieldNum: fn, wireType: wt, data: sub})
		} else if wt == 1 { // 64-bit
			i += 8
		} else if wt == 5 { // 32-bit
			i += 4
		} else {
			break
		}
	}
	return fields
}

func encodeVarint(val uint64) []byte {
	var res []byte
	for {
		b := byte(val & 0x7f)
		val >>= 7
		if val > 0 {
			res = append(res, b|0x80)
		} else {
			res = append(res, b)
			break
		}
	}
	return res
}

func encodeFieldVarint(fn int, val uint64) []byte {
	tag := fn << 3
	return append(encodeVarint(uint64(tag)), encodeVarint(val)...)
}

func encodeFieldBytes(fn int, data []byte) []byte {
	tag := (fn << 3) | 2
	out := encodeVarint(uint64(tag))
	out = append(out, encodeVarint(uint64(len(data)))...)
	out = append(out, data...)
	return out
}

var stripTitleRegexp = regexp.MustCompile(`^[#*\-=>\s]+`)

func cleanTitleText(text string) string {
	if text == "" {
		return ""
	}
	// Replace literal escaped newlines and tabs from JSON/serialized strings
	text = strings.ReplaceAll(text, `\r\n`, "\n")
	text = strings.ReplaceAll(text, `\n`, "\n")
	text = strings.ReplaceAll(text, `\t`, " ")
	text = strings.ReplaceAll(text, `\"`, "\"")

	lines := strings.Split(text, "\n")
	var candidate string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		cleaned := stripTitleRegexp.ReplaceAllString(trimmed, "")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned != "" && !strings.HasPrefix(cleaned, "file://") {
			candidate = cleaned
			break
		}
	}
	if candidate == "" && len(lines) > 0 {
		candidate = stripTitleRegexp.ReplaceAllString(strings.TrimSpace(lines[0]), "")
	}
	candidate = strings.TrimSpace(candidate)
	if len(candidate) > 80 {
		candidate = candidate[:80]
	}
	return candidate
}

type ConvoMeta struct {
	ID           string
	Title        string
	StepCount    int
	CreatedTS    int64
	ModifiedTS   int64
	TrajectoryID string
	WorkspaceURI string
	MetaBlob     []byte
}

func buildSummaryEntry(info ConvoMeta) ([]byte, error) {
	cidBytes := []byte(info.ID)
	titleBytes := []byte(info.Title)
	trajBytes := []byte(info.TrajectoryID)
	wsBytes := []byte(info.WorkspaceURI)

	createdTS := append(encodeFieldVarint(1, uint64(info.CreatedTS)), encodeFieldVarint(2, 0)...)
	modifiedTS := append(encodeFieldVarint(1, uint64(info.ModifiedTS)), encodeFieldVarint(2, 0)...)
	wsEntry := append(encodeFieldBytes(1, wsBytes), encodeFieldBytes(2, wsBytes)...)

	var inner bytes.Buffer
	inner.Write(encodeFieldBytes(1, titleBytes))
	inner.Write(encodeFieldVarint(2, uint64(info.StepCount)))
	inner.Write(encodeFieldBytes(3, modifiedTS))
	inner.Write(encodeFieldBytes(4, trajBytes))
	inner.Write(encodeFieldVarint(5, 1)) // status IDLE
	inner.Write(encodeFieldBytes(7, createdTS))
	inner.Write(encodeFieldBytes(9, wsEntry))
	inner.Write(encodeFieldBytes(10, modifiedTS))
	inner.Write(encodeFieldBytes(15, []byte{}))
	subStep := info.StepCount - 1
	if subStep < 0 {
		subStep = 0
	}
	inner.Write(encodeFieldVarint(16, uint64(subStep)))
	if len(info.MetaBlob) > 0 {
		inner.Write(encodeFieldBytes(17, info.MetaBlob))
	}
	inner.Write(encodeFieldVarint(22, 4))

	innerB64 := []byte(base64.StdEncoding.EncodeToString(inner.Bytes()))
	valPB := encodeFieldBytes(1, innerB64)
	entryPB := append(encodeFieldBytes(1, cidBytes), encodeFieldBytes(2, valPB)...)
	return encodeFieldBytes(1, entryPB), nil
}

func extractMapEntries(raw []byte) (map[string][]byte, error) {
	entries := make(map[string][]byte)
	fields := parseMsg(raw)
	for _, f := range fields {
		if f.fieldNum == 1 && f.wireType == 2 {
			subFields := parseMsg(f.data)
			var cid string
			for _, sf := range subFields {
				if sf.fieldNum == 1 && sf.wireType == 2 {
					cid = string(sf.data)
					break
				}
			}
			if cid != "" {
				entries[cid] = f.data
			}
		}
	}
	if len(entries) == 0 && len(raw) > 0 {
		return entries, errors.New("no map entries parsed")
	}
	return entries, nil
}
