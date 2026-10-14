package pspnode

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type statusReader struct {
	ports.NativePanelSnapshotReader
	status  ports.ServerStatus
	err     error
	panelID int64
}

func (r *statusReader) NativePanelStatus(_ context.Context, id int64) (ports.ServerStatus, error) {
	r.panelID = id
	return r.status, r.err
}

func TestServerStatusUsesOptionalObservedStatusWithoutFullSnapshotFallback(t *testing.T) {
	want := ports.ServerStatus{PanelVersion: "v0.2", CoreEngine: "xray", XrayVersion: "26.6.27", XrayState: "running"}
	reader := &statusReader{status: want}
	client := &Client{panelID: 9, reader: reader}
	got, err := client.GetServerStatus(t.Context())
	if err != nil || got == nil || *got != want || reader.panelID != 9 {
		t.Fatal("optional status was not scoped to the panel")
	}
	got.XrayState = "caller-edit"
	if reader.status != want {
		t.Fatal("facade status aliases the reader")
	}
	for _, failure := range []error{ports.ErrNativePanelSnapshotMissing, ports.ErrNativePanelAgentOffline, ports.ErrNativePanelSnapshotStale, context.Canceled} {
		reader.err = failure
		if got, err := client.GetServerStatus(t.Context()); got != nil || !errors.Is(err, failure) {
			t.Fatal("optional status failure fell back or returned an observation")
		}
	}
	// The absent embedded full reader would panic if the optional path fell
	// back. Readers without the optional port still use the original contract.
	legacy := &Client{reader: snapshotReader{snapshot: &ports.NativePanelSnapshot{Status: want}}}
	got, err = legacy.GetServerStatus(t.Context())
	if err != nil || got == nil || *got != want {
		t.Fatal("legacy status projection changed")
	}
}
