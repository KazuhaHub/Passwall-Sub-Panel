package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// fakeSubLogRepo is an in-memory ports.SubLogRepo: List serves its rows as
// stored, whatever the filter, which is all the handler tests need.
type fakeSubLogRepo struct {
	rows []*domain.SubLog
}

var _ ports.SubLogRepo = (*fakeSubLogRepo)(nil)

func (f *fakeSubLogRepo) Insert(_ context.Context, l *domain.SubLog) error {
	cp := *l
	f.rows = append(f.rows, &cp)
	return nil
}

func (f *fakeSubLogRepo) List(_ context.Context, _ ports.SubLogFilter) ([]*domain.SubLog, int64, error) {
	return f.rows, int64(len(f.rows)), nil
}

func (f *fakeSubLogRepo) Clear(context.Context) error {
	f.rows = nil
	return nil
}

func (f *fakeSubLogRepo) DeleteBefore(_ context.Context, cutoff time.Time) (int64, error) {
	kept := f.rows[:0]
	var n int64
	for _, r := range f.rows {
		if r.AccessedAt.Before(cutoff) {
			n++
			continue
		}
		kept = append(kept, r)
	}
	f.rows = kept
	return n, nil
}

func (f *fakeSubLogRepo) ScanSince(_ context.Context, since time.Time, _ int, fn func([]domain.SubLog) error) error {
	var out []domain.SubLog
	for _, r := range f.rows {
		if !r.AccessedAt.Before(since) {
			out = append(out, *r)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return fn(out)
}

func (f *fakeSubLogRepo) RecentForUsers(_ context.Context, userIDs []int64, since time.Time, limit int) ([]domain.SubLog, error) {
	want := map[int64]bool{}
	for _, id := range userIDs {
		want[id] = true
	}
	var out []domain.SubLog
	for i := len(f.rows) - 1; i >= 0; i-- {
		r := f.rows[i]
		if want[r.UserID] && !r.AccessedAt.Before(since) && (limit <= 0 || len(out) < limit) {
			out = append(out, *r)
		}
	}
	return out, nil
}

// THE DEVICE A CLIENT DECLARES IS FOR ADMINS, AND THE SUB-LOG LIST IS NOT.
//
// GET /api/admin/sub-logs is on the staff group, so operators read it too. A
// device label ("iOS 17.5 · iPhone15,2") plus a stable per-account id is a
// description of somebody's hardware; it is shown to the role that can also
// act on the risk signals built from it, and to nobody else. The role check
// lives in the handler, not the route, so this drives the handler with each
// caller shape: admin claims, operator claims, and no claims at all (which
// must read as "not an admin", never as "unchecked").
func TestAdminSubLogs_DeviceLabelIsAdminOnly(t *testing.T) {
	const fullID = "beefcafe12345678"
	// A whole-second time, so no digit run in the timestamp can be mistaken
	// for the id prefix the non-admin cases look for.
	at := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	repo := &fakeSubLogRepo{rows: []*domain.SubLog{
		{ID: 1, UserID: 7, IP: "198.51.100.7", UA: "Happ/3.13.0", ClientType: "other",
			AccessedAt: at, DeviceID: fullID, DeviceLabel: "iOS 17.5 · iPhone15,2"},
		{ID: 2, UserID: 7, IP: "198.51.100.8", UA: "v2rayN/7.0", ClientType: "V2RayN",
			AccessedAt: at},
	}}
	h := NewAdminSubLogHandler(repo, nil, nil)

	list := func(t *testing.T, role domain.Role) (string, []map[string]any) {
		t.Helper()
		c, rec := claimsCtx(role)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/sub-logs", nil)
		h.List(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("List = %d: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body.String())
		}
		if len(body.Items) != 2 {
			t.Fatalf("items = %d, want 2: %s", len(body.Items), rec.Body.String())
		}
		return rec.Body.String(), body.Items
	}

	t.Run("admin", func(t *testing.T) {
		raw, items := list(t, domain.RoleAdmin)
		if got := items[0]["device_label"]; got != "iOS 17.5 · iPhone15,2" {
			t.Errorf("device_label = %v, want the stored label", got)
		}
		if got := items[0]["device_id4"]; got != fullID[:4] {
			t.Errorf("device_id4 = %v, want %q", got, fullID[:4])
		}
		// Only a prefix, even for an admin: enough to tell two devices of
		// one account apart at a glance, not a value to copy elsewhere.
		if strings.Contains(raw, fullID) {
			t.Errorf("the full device id reached the wire: %s", raw)
		}
		// A fetch that declared nothing shows nothing, not empty strings.
		for _, k := range []string{"device_label", "device_id4"} {
			if _, ok := items[1][k]; ok {
				t.Errorf("row without a declared device carries %q: %v", k, items[1])
			}
		}
	})
	for _, tc := range []struct {
		name string
		role domain.Role
	}{
		{"operator", domain.RoleOperator},
		{"no claims", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := list(t, tc.role)
			if strings.Contains(raw, "device") {
				t.Errorf("a non-admin caller received device data: %s", raw)
			}
			if strings.Contains(raw, "iPhone15") || strings.Contains(raw, fullID[:4]) {
				t.Errorf("a non-admin caller received the label or the id: %s", raw)
			}
		})
	}
}
