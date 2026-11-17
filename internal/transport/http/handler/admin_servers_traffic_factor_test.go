package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// factorPanelRepo adds the column-scoped factor writer to the metadata repo.
type factorPanelRepo struct {
	channelPanelRepo
	factorWrites []int
	factorErr    error
}

func (r *factorPanelRepo) UpdateTrafficFactor(_ context.Context, _ int64, milli int) error {
	if r.factorErr != nil {
		return r.factorErr
	}
	r.factorWrites = append(r.factorWrites, milli)
	m := milli
	r.panel.TrafficFactorMilli = &m
	return nil
}

func TestServerTrafficFactorUpdateIsScopedAndAudited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, kind := range []domain.PanelKind{domain.PanelKindPSP, domain.PanelKind3XUI, domain.PanelKindSUI} {
		t.Run(string(kind), func(t *testing.T) {
			r := &factorPanelRepo{channelPanelRepo: channelPanelRepo{panel: &domain.Panel{ID: 41, Kind: kind, Name: "hk", URL: "https://hk.example", APIToken: "token"}}}
			pool := &channelPool{}
			audit := &upgradeGateAudit{}
			h := NewAdminServersHandler(r, pool, nil, audit, nil, nil)

			w := updateChannelRequest(t, h, `{"traffic_factor_milli":1500}`)
			if w.Code != 200 {
				t.Fatalf("update = %d %s", w.Code, w.Body.String())
			}
			var dto serverDTO
			if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
				t.Fatal(err)
			}
			if dto.TrafficFactorMilli != 1500 || len(r.factorWrites) != 1 || r.factorWrites[0] != 1500 || pool.replaced != 1 {
				t.Fatalf("dto %d writes %v replaced %d", dto.TrafficFactorMilli, r.factorWrites, pool.replaced)
			}
			if !audit.saw("server_traffic_factor_changed") || audit.details[0] != `{"traffic_factor_milli":1500}` {
				t.Fatalf("audit = %v %v", audit.actions, audit.details)
			}

			// The same value again is not a change: no write, no audit row.
			w = updateChannelRequest(t, h, `{"traffic_factor_milli":1500}`)
			if w.Code != 200 || len(r.factorWrites) != 1 || len(audit.actions) != 1 {
				t.Fatalf("unchanged factor rewrote: %d writes %v audit %v", w.Code, r.factorWrites, audit.actions)
			}
			// An edit that omits the factor keeps it.
			w = updateChannelRequest(t, h, `{"name":"hk-2"}`)
			if w.Code != 200 || r.panel.EffectiveTrafficFactor() != 1500 || len(r.factorWrites) != 1 {
				t.Fatalf("omitted factor changed: %d factor %d", w.Code, r.panel.EffectiveTrafficFactor())
			}
			// Free is a valid factor.
			if w = updateChannelRequest(t, h, `{"traffic_factor_milli":0}`); w.Code != 200 || r.panel.EffectiveTrafficFactor() != 0 {
				t.Fatalf("free factor = %d, code %d", r.panel.EffectiveTrafficFactor(), w.Code)
			}
		})
	}
}

func TestServerTrafficFactorRejectsOutOfRangeBeforeWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{"traffic_factor_milli":-1}`, `{"traffic_factor_milli":100001}`, `{"traffic_factor_milli":"1.5"}`} {
		r := &factorPanelRepo{channelPanelRepo: channelPanelRepo{panel: &domain.Panel{ID: 41, Kind: domain.PanelKindPSP, Name: "hk", URL: "psp://agt"}}}
		pool := &channelPool{}
		w := updateChannelRequest(t, NewAdminServersHandler(r, pool, nil, nil, nil, nil), body)
		if w.Code != 400 || len(r.factorWrites) != 0 || r.narrowWrites != 0 || pool.replaced != 0 {
			t.Fatalf("%s: code %d writes %v narrow %d replaced %d", body, w.Code, r.factorWrites, r.narrowWrites, pool.replaced)
		}
	}
}

// A pool failure after the factor landed puts the old factor back.
func TestServerTrafficFactorRollsBackOnPoolFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := &factorPanelRepo{channelPanelRepo: channelPanelRepo{panel: &domain.Panel{ID: 41, Kind: domain.PanelKindPSP, Name: "hk", URL: "psp://agt"}}}
	pool := &channelPool{err: errors.New("pool down")}
	audit := &upgradeGateAudit{}
	w := updateChannelRequest(t, NewAdminServersHandler(r, pool, nil, audit, nil, nil), `{"traffic_factor_milli":2000}`)
	if w.Code != 500 || r.panel.EffectiveTrafficFactor() != domain.TrafficFactorDefault {
		t.Fatalf("code %d factor %d, want 500 and the old factor back", w.Code, r.panel.EffectiveTrafficFactor())
	}
	if len(r.factorWrites) != 2 || r.factorWrites[1] != domain.TrafficFactorDefault || len(audit.actions) != 0 {
		t.Fatalf("writes %v audit %v", r.factorWrites, audit.actions)
	}
}

// A repo without the factor writer cannot store a factor; the request is
// refused rather than silently dropped.
func TestServerTrafficFactorRefusedWithoutWriter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := &channelPanelRepo{panel: &domain.Panel{ID: 41, Kind: domain.PanelKind3XUI, Name: "hk", URL: "https://hk", APIToken: "t"}}
	w := updateChannelRequest(t, NewAdminServersHandler(r, &channelPool{}, nil, nil, nil, nil), `{"traffic_factor_milli":1500}`)
	if w.Code != 503 || r.fullWrites != 0 {
		t.Fatalf("code %d full writes %d", w.Code, r.fullWrites)
	}
}

func TestUserStatusCarriesFactorAndResolvesNamePlaceholder(t *testing.T) {
	n := &domain.Node{DisplayName: "HK {{ factor }}", Region: "HK", Enabled: true,
		Relays: []domain.RelayLine{{Name: "relay {{ factor }}", Address: "r.example", Port: 1, Enabled: true}}}
	got := withTrafficFactor(userStatusForNode(n), 1500)
	if got.TrafficFactorMilli != 1500 || got.Name != "HK ×1.5" ||
		len(got.RelayStatuses) != 1 || got.RelayStatuses[0].Name != "relay ×1.5" {
		t.Fatalf("status = %+v", got)
	}
	plain := withTrafficFactor(userStatusForNode(&domain.Node{DisplayName: "JP", Enabled: true}), 1000)
	if plain.Name != "JP" || plain.TrafficFactorMilli != 1000 {
		t.Fatalf("plain status = %+v", plain)
	}
}

func TestServerTrafficFactorChangeInvalidatesRenderCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := &factorPanelRepo{channelPanelRepo: channelPanelRepo{panel: &domain.Panel{ID: 41, Kind: domain.PanelKindPSP, Name: "hk", URL: "psp://agt"}}}
	invalidated := 0
	h := NewAdminServersHandler(r, &channelPool{}, nil, nil, nil, func() { invalidated++ })
	if w := updateChannelRequest(t, h, `{"traffic_factor_milli":2000}`); w.Code != 200 || invalidated != 1 {
		t.Fatalf("code %d invalidated %d", w.Code, invalidated)
	}
	if w := updateChannelRequest(t, h, `{"traffic_factor_milli":2000}`); w.Code != 200 || invalidated != 1 {
		t.Fatalf("unchanged factor invalidated: %d", invalidated)
	}
}
