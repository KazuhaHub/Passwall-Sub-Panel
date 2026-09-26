package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// GET /api/admin/sub-logs is open to operators and embeds SubLog in its row
// view, so anything SubLog serializes an operator sees. The declared device
// is admin-only: it may reach the wire only through a view that copies it
// out after checking the caller's role.
func TestSubLog_DeviceFieldsNeverSerialize(t *testing.T) {
	l := SubLog{
		ID: 1, UserID: 7, IP: "203.0.113.9", UA: "Happ/3.13.0", ClientType: "mihomo",
		AccessedAt:  time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
		DeviceID:    "0123456789abcdef",
		DeviceLabel: "iOS 17.5 · iPhone15,2",
	}
	raw, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(string(raw))
	for _, leak := range []string{"device", "0123456789abcdef", "iphone15,2"} {
		if strings.Contains(body, leak) {
			t.Fatalf("serialized SubLog contains %q: %s", leak, raw)
		}
	}
}
