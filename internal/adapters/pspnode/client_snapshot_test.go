package pspnode

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/corefixtures"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type clientSnapshotReader struct {
	snapshotReader
	detail *ports.ClientDetail
	err    error
	panel  int64
	email  string
	calls  int
}

func (r *clientSnapshotReader) NativePanelClientSnapshot(_ context.Context, panelID int64, email string) (*ports.ClientDetail, error) {
	r.calls++
	r.panel, r.email = panelID, email
	return r.detail, r.err
}

func TestClientLookupUsesScopedReaderWithoutChangingUnavailableOrOwnership(t *testing.T) {
	for _, unavailable := range []error{nil, ports.ErrNativePanelSnapshotMissing, ports.ErrNativePanelSnapshotStale, ports.ErrNativePanelAgentOffline} {
		r := &clientSnapshotReader{snapshotReader: snapshotReader{err: errors.New("full fleet read must not run")}, err: unavailable}
		if unavailable == nil {
			r.detail = &ports.ClientDetail{ID: "confirmed-fixture", Email: "exact@fixture.local", InboundIDs: []int{8}}
		}
		client, err := New(&domain.Panel{ID: 9, Kind: domain.PanelKindPSP}, r, nodeRepo{}, &agentRepo{}, corefixtures.Static{})
		if err != nil {
			t.Fatal(err)
		}
		detail, err := client.GetClient(t.Context(), "exact@fixture.local")
		if !errors.Is(err, unavailable) || r.calls != 1 || r.panel != 9 || r.email != "exact@fixture.local" {
			t.Fatal("scoped lookup changed errors or owner/email identity")
		}
		if unavailable != nil {
			if detail != nil {
				t.Fatal("unavailable observation became a client")
			}
			continue
		}
		if detail == nil || detail.ID != "confirmed-fixture" {
			t.Fatal("scoped lookup lost confirmed identity")
		}
		detail.InboundIDs[0] = 99
		again, err := client.GetClient(t.Context(), "exact@fixture.local")
		if err != nil || again.InboundIDs[0] != 8 {
			t.Fatal("scoped reader exposed mutable cached storage")
		}
		r.detail = nil
		if absent, err := client.GetClient(t.Context(), "absent@fixture.local"); err != nil || absent != nil {
			t.Fatal("observed absence changed semantics")
		}
	}
}
