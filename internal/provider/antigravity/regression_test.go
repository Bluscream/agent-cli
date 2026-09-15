package antigravity

import (
	"testing"
)

func TestAuditMalformedLength(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("parser panicked: %v", r)
		}
	}()
	parseMsg([]byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01})
}
