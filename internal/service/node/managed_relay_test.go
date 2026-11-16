package node

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// panelListPool answers List for the entry-server check; nothing else is used.
type panelListPool struct {
	ports.XUIPool
	panels []*domain.Panel
}

func (p panelListPool) List() []*domain.Panel { return p.panels }

type fleetNodeRepo struct {
	labelNodeRepo
	fleet []*domain.Node
}

func (r *fleetNodeRepo) List(context.Context) ([]*domain.Node, error) { return r.fleet, nil }

func TestUpdateMetadataChecksManagedRelayLines(t *testing.T) {
	pool := panelListPool{panels: []*domain.Panel{
		{ID: 1, Kind: domain.PanelKind3XUI}, {ID: 7, Kind: domain.PanelKindPSP}, {ID: 9, Kind: domain.PanelKindPSP},
	}}
	stored := domain.Node{ID: 1, PanelID: 7, DisplayName: "landing", DesiredPort: 443, Enabled: true}
	fleet := []*domain.Node{
		&stored,
		{ID: 2, PanelID: 9, DisplayName: "entry inbound", DesiredPort: 443},
		{ID: 3, PanelID: 7, DisplayName: "other landing", DesiredPort: 8443, Relays: []domain.RelayLine{
			{ID: "x", Address: "a.example", Port: 20001, Enabled: true, EntryPanelID: 9},
		}},
	}
	managed := func(entry int64, port int) domain.RelayLine {
		return domain.RelayLine{Address: "a.example", Port: port, Enabled: true, EntryPanelID: entry, Tags: []string{}}
	}
	for name, test := range map[string]struct {
		lines []domain.RelayLine
		want  string
	}{
		"free port on a PN server":       {lines: []domain.RelayLine{managed(9, 20002)}},
		"external lines are not checked": {lines: []domain.RelayLine{{Address: "x", Port: 443, Enabled: true, Tags: []string{}}}},
		"entry is a 3X-UI server":        {lines: []domain.RelayLine{managed(1, 20002)}, want: "Passwall Node"},
		"entry is unknown":               {lines: []domain.RelayLine{managed(5, 20002)}, want: "Passwall Node"},
		"entry is the landing's own":     {lines: []domain.RelayLine{managed(7, 20002)}, want: "own server"},
		"port of an entry inbound":       {lines: []domain.RelayLine{managed(9, 443)}, want: "inbound of node"},
		"port of another line":           {lines: []domain.RelayLine{managed(9, 20001)}, want: "relay line of node"},
		"two lines of this node collide": {lines: []domain.RelayLine{managed(9, 20002), {Address: "b", Port: 20002, Enabled: true, EntryPanelID: 9, Tags: []string{}}}, want: "relay line 1 of this node"},
		"no port":                        {lines: []domain.RelayLine{managed(9, 0)}, want: "entry port"},
		"SNI override":                   {lines: []domain.RelayLine{func() domain.RelayLine { l := managed(9, 20002); l.SNI = "x"; return l }()}, want: "SNI"},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &fleetNodeRepo{labelNodeRepo: labelNodeRepo{stored: stored}, fleet: fleet}
			svc := &Service{nodes: repo, pool: pool}
			next := stored
			next.Relays = append([]domain.RelayLine(nil), test.lines...)
			err := svc.UpdateMetadata(context.Background(), &next)
			if test.want == "" {
				if err != nil {
					t.Fatalf("rejected: %v", err)
				}
				return
			}
			if err == nil || !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want a validation error mentioning %q", err, test.want)
			}
			if repo.written != nil {
				t.Fatal("an invalid line was saved")
			}
		})
	}
}
