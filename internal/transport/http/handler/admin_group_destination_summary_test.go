package handler

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/gin-gonic/gin"
)

type groupDestinationSummaryGroups struct{ ports.GroupRepo }

func (*groupDestinationSummaryGroups) ListPaged(context.Context, ports.Pagination) ([]*domain.Group, int64, error) {
	return []*domain.Group{{ID: 7, Name: "Staff group"}}, 1, nil
}
func (*groupDestinationSummaryGroups) CountMembersByGroups(context.Context, []int64) (map[int64]int64, error) {
	return map[int64]int64{7: 3}, nil
}

type groupDestinationSummaryModes struct {
	result map[int64]domain.DestGroupAccessMode
	err    error
}

func (m groupDestinationSummaryModes) ReadDestinationGroupModes(context.Context, []int64) (map[int64]domain.DestGroupAccessMode, error) {
	return m.result, m.err
}

func TestGroupListWithholdsPartialSummaryOnMissingCorruptOrFailedModeRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		repo ports.DestGroupModeReadRepo
	}{
		{"unwired", nil},
		{"missing", groupDestinationSummaryModes{result: map[int64]domain.DestGroupAccessMode{}}},
		{"unknown", groupDestinationSummaryModes{result: map[int64]domain.DestGroupAccessMode{7: "private-corrupt-mode"}}},
		{"failed", groupDestinationSummaryModes{err: errors.New("private-group-driver-marker")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewAdminGroupHandler(group.New(&groupDestinationSummaryGroups{}, nil, nil), nil, nil, tc.repo)
			r := gin.New()
			r.GET("/groups", h.List)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/groups", nil))
			if w.Code != 503 || strings.Contains(w.Body.String(), "private-") || strings.Contains(w.Body.String(), "Staff group") || strings.Contains(w.Body.String(), "dest_mode") {
				t.Fatal("mode-read failure leaked diagnostics or partial/default-open staff summary")
			}
		})
	}
}
