package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
)

func TestDestinationGroupPutRequiresFullFieldsAndCanonicalCAS(t *testing.T) {
	calls := 0
	save := func(_ context.Context, id int64, input destpolicy.GroupModeInput, expected time.Time) (domain.DestGroupMode, error) {
		calls++
		if id != 7 || input.Mode != "allowlist" || input.Stage != "trial" || !expected.IsZero() || len(input.ListIDs) != 0 || len(input.BaseHosts) != 1 {
			t.Fatal("initial mode input or zero-version CAS was changed")
		}
		changed := time.UnixMilli(1_800_000_000_001).UTC()
		return domain.DestGroupMode{GroupID: id, Mode: input.Mode, Stage: input.Stage, BaseListID: 20, ExtraListID: 21, UpdatedAt: changed, StageChangedAt: &changed}, nil
	}
	r := gin.New()
	r.PUT("/groups/:group_id", NewAdminDestinationGroupsHandler(save).Put)
	valid := `{"mode":"allowlist","stage":"trial","list_ids":[],"base_hosts":["dns.example.test"],"updated_at":0}`
	for _, item := range []struct{ path, body string }{
		{"/groups/0", valid}, {"/groups/01", valid}, {"/groups/nope", valid}, {"/groups/7?extra=1", valid},
		{"/groups/7", `{}`}, {"/groups/7", `{"mode":"open","list_ids":[],"updated_at":0}`},
		{"/groups/7", `{"mode":"open","stage":"","updated_at":0}`},
		{"/groups/7", `{"mode":"open","stage":"","list_ids":[],"updated_at":null}`},
		{"/groups/7", `{"mode":"open","stage":"","list_ids":[],"updated_at":-1}`},
		{"/groups/7", strings.Replace(valid, `"updated_at":0`, `"updated_at":0.5`, 1)},
		{"/groups/7", strings.Replace(valid, `"updated_at":0`, `"updated_at":0,"extra":1`, 1)},
		{"/groups/7", valid + `{}`},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("PUT", item.path, strings.NewReader(item.body)))
		if w.Code != 400 || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("invalid group request %s %s HTTP=%d", item.path, item.body, w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("invalid HTTP input reached mode mutation")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/groups/7", strings.NewReader(valid)))
	var got struct {
		GroupID        int64   `json:"group_id"`
		UpdatedAt      int64   `json:"updated_at"`
		StageChangedAt int64   `json:"stage_changed_at"`
		ListIDs        []int64 `json:"list_ids"`
		BaseListID     int64   `json:"base_list_id"`
		ExtraListID    int64   `json:"extra_list_id"`
	}
	if w.Code != 200 || calls != 1 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.GroupID != 7 || got.UpdatedAt != 1_800_000_000_001 || got.StageChangedAt != got.UpdatedAt || got.ListIDs == nil || got.BaseListID != 20 || got.ExtraListID != 21 {
		t.Fatal("saved mode omitted canonical millisecond edit version or real owned IDs")
	}
}

func TestDestinationGroupPutMapsKnownConflictsAndWithholdsUnexpectedFailures(t *testing.T) {
	for _, item := range []struct {
		err    error
		status int
		code   string
	}{
		{domain.ErrNotFound, 404, "dest_group_not_found"},
		{fmt.Errorf("%w: dest_mode_stale", domain.ErrConflict), 409, "dest_mode_stale"},
		{fmt.Errorf("%w: dest_list_not_ready", domain.ErrConflict), 409, "dest_list_not_ready"},
		{fmt.Errorf("%w: dest_mode_invalid_transition", domain.ErrConflict), 409, "dest_mode_invalid_transition"},
		{errors.New("private-group-driver-detail"), 503, ""},
	} {
		save := func(context.Context, int64, destpolicy.GroupModeInput, time.Time) (domain.DestGroupMode, error) {
			return domain.DestGroupMode{GroupID: 888, BaseListID: 999}, item.err
		}
		r := gin.New()
		r.PUT("/groups/:group_id", NewAdminDestinationGroupsHandler(save).Put)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("PUT", "/groups/7", strings.NewReader(`{"mode":"open","stage":"","list_ids":[],"updated_at":1800000000000}`)))
		if w.Code != item.status || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "base_list_id") || item.code != "" && !strings.Contains(w.Body.String(), item.code) {
			t.Fatalf("mode error mapping HTTP=%d body=%s", w.Code, w.Body.String())
		}
	}
}
