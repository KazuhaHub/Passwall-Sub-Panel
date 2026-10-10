package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

func TestBuildDestinationStatusReadsDurableAuditUnitsAndRetainsOffHistory(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	f.report.Capabilities = []string{protocol.CapabilityAuditHits}
	f.report.CoreEngine = "xray"
	f.report.Audit = fixtureAudit(t, f, 45)
	_ = syncNativeCacheFixture(t, f.a, f.credential, f.report)
	f.a.startDestinationAudit()
	waitAuditRows(t, f.a, "dest_hits", 1)
	f.a.destAudit.StopOffers()
	f.a.bgWG.Wait()
	token := destinationRefreshAdminToken(t, f.a)
	check := func() {
		t.Helper()
		w := destinationListRequest(t, f.a, token, "GET", "status", nil)
		var v destpolicy.DestinationStatus
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || len(v.Nodes) != 1 {
			t.Fatal("status read failed")
		}
		data, _ := json.Marshal(v.Nodes[0])
		var row struct {
			Hits   *int64 `json:"hits_24h"`
			Losses *struct {
				Rows, Events, Unmatched int64
				Scope                   string
				Complete                bool
			} `json:"losses"`
		}
		if json.Unmarshal(data, &row) != nil || row.Hits == nil || *row.Hits != 4 || row.Losses == nil || row.Losses.Rows != 0 || row.Losses.Events != 2 || row.Losses.Unmatched != 3 || row.Losses.Scope != "panel" || row.Losses.Complete {
			t.Fatal("status omitted stored counters or conflated loss units")
		}
	}
	check()
	if w := serverAuditRequest(t, f.a, token, "PUT", fmt.Sprintf("/%d", f.agent.PanelID), map[string]any{"audit_collect": "off"}); w.Code != 200 {
		t.Fatal("collection update failed")
	}
	check()
}

type auditStatsProbe struct {
	entered, release chan struct{}
}

func (p *auditStatsProbe) ReadDestinationAuditPanelStats(ctx context.Context, _, _ time.Time, _ []int64) (map[int64]domain.DestAuditPanelStats, error) {
	close(p.entered)
	select {
	case <-p.release:
		return nil, domain.ErrUnavailable
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestBuildDestinationStatusRetainsBackendAdmissionAndPropagatesFailedRead(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	f.report.Capabilities = []string{protocol.CapabilityAuditHits}
	f.report.CoreEngine = "xray"
	_ = syncNativeCacheFixture(t, f.a, f.credential, f.report)
	p := &auditStatsProbe{entered: make(chan struct{}), release: make(chan struct{})}
	f.a.destAuditRead = p
	var once sync.Once
	finish := func() { once.Do(func() { close(p.release) }) }
	defer finish()
	done := make(chan error, 1)
	go func() {
		_, err := f.a.destinationStatus(t.Context())
		done <- err
	}()
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("status did not read its audit projection")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	err := f.a.operationGate.Exclusive(ctx, func(context.Context) error {
		t.Error("backend switch crossed an active status read")
		return nil
	})
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("status released admission before its audit read")
	}
	finish()
	if err := <-done; !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("failed audit read became a successful zero")
	}
	if err := f.a.operationGate.Exclusive(t.Context(), func(context.Context) error {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := f.a.destinationStatus(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Error("canceled status crossed backend admission")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
