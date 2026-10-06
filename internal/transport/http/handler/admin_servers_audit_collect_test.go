package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/gin-gonic/gin"
)

func TestServerCollectionDTOKeepsThirdPartyRowsWithoutNativePreference(t *testing.T) {
	for _, kind := range []domain.PanelKind{domain.PanelKindPSP, domain.PanelKind3XUI, domain.PanelKindSUI} {
		data, err := json.Marshal(toServerDTO(&domain.Panel{Kind: kind, AuditCollect: domain.AuditCollectOff}))
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if kind == domain.PanelKindPSP {
			if string(fields["audit_collect"]) != `"off"` {
				t.Fatal("native DTO omitted saved off mode")
			}
		} else if _, present := fields["audit_collect"]; present {
			t.Fatal("third-party DTO advertised native collection")
		}
	}
}

func TestNativeCollectionPoolFailureRestoresModeWithNewRevision(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := &channelPanelRepo{panel: &domain.Panel{ID: 41, Kind: domain.PanelKindPSP, Name: "original", URL: "psp://agt_original", AuditCollect: domain.AuditCollectHits, AuditCollectRevision: 1}}
	pool := &channelPool{err: errors.New("test pool failure")}
	w := updateChannelRequest(t, NewAdminServersHandler(r, pool, nil, nil, nil, nil), `{"audit_collect":"off","remark":"must roll back"}`)
	if w.Code != 500 || r.fullWrites != 0 || r.narrowWrites != 2 || r.panel.AuditCollect != domain.AuditCollectHits || r.panel.AuditCollectRevision != 3 || r.panel.Remark != "" {
		t.Fatal("pool rollback erased collection revision or left the failed mode active")
	}
}

type collectionLegacyRepo struct {
	ports.XUIPanelRepo
	saves int
}

func (r *collectionLegacyRepo) GetByID(context.Context, int64) (*domain.Panel, error) {
	return &domain.Panel{ID: 41, Kind: domain.PanelKindPSP, Name: "original", AuditCollect: domain.AuditCollectHits, AuditCollectRevision: 1}, nil
}
func (r *collectionLegacyRepo) Save(context.Context, *domain.Panel) error { r.saves++; return nil }

func TestNativeCollectionRequiresAtomicWriterBeforeAnyMetadataSave(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := &collectionLegacyRepo{}
	pool := &channelPool{}
	w := updateChannelRequest(t, NewAdminServersHandler(r, pool, nil, nil, nil, nil), `{"audit_collect":"off","remark":"must not commit"}`)
	if w.Code != 503 || r.saves != 0 || pool.replaced != 0 {
		t.Fatal("missing collection writer reported a successful non-atomic save")
	}
}
