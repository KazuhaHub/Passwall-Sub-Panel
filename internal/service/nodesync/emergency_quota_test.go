package nodesync

import (
	"context"
	"errors"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type emergencyDirectiveSettings struct {
	global  ports.UISettings
	perUser map[int64]ports.UISettings
}

func (s emergencyDirectiveSettings) Load(_ context.Context, defaults ports.UISettings) (ports.UISettings, error) {
	defaults.EmergencyAccessQuotaGB = s.global.EmergencyAccessQuotaGB
	return defaults, nil
}

func (s emergencyDirectiveSettings) LoadForGroup(ctx context.Context, _ int64, defaults ports.UISettings) (ports.UISettings, error) {
	return s.Load(ctx, defaults)
}

func (s emergencyDirectiveSettings) LoadForUser(ctx context.Context, user *domain.User, defaults ports.UISettings) (ports.UISettings, error) {
	if user != nil {
		if effective, ok := s.perUser[user.ID]; ok {
			return effective, nil
		}
	}
	return s.Load(ctx, defaults)
}

type emergencyDirectiveUsers struct {
	ports.UserRepo
	user *domain.User
	err  error
}

func (r emergencyDirectiveUsers) GetByID(context.Context, int64) (*domain.User, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.user, nil
}

type emergencyDirectiveClients struct {
	ports.PSPClientRepo
	client *domain.PSPClient
}

func (r emergencyDirectiveClients) ListAll(context.Context) ([]*domain.PSPClient, error) {
	return []*domain.PSPClient{r.client}, nil
}

type emergencyDirectiveAgents struct {
	ports.NodeAgentRepo
	agent *domain.NodeAgent
}

func (r emergencyDirectiveAgents) List(context.Context) ([]*domain.NodeAgent, error) {
	return []*domain.NodeAgent{r.agent}, nil
}

func TestBuildDirectivesUserLoadFailureEmitsNoQuota(t *testing.T) {
	wantErr := errors.New("group limits unavailable")
	client := &domain.PSPClient{ID: 11, UserID: 7, PanelID: 9}
	agent := &domain.NodeAgent{AgentID: "agt_failed_limits", PanelID: client.PanelID}
	service := &Service{
		users:   emergencyDirectiveUsers{err: wantErr},
		clients: emergencyDirectiveClients{client: client},
		agents:  emergencyDirectiveAgents{agent: agent},
		settings: emergencyDirectiveSettings{
			global: ports.UISettings{},
		},
		reports: make(map[string]receivedFullReport),
		anchors: make(map[int64]nodeprotocol.ClientCounters),
		grants:  make(map[string]map[nodeprotocol.ClientKey]int64),
	}
	body, _, err := service.buildDirectives(t.Context(), agent, &ports.NativeDesiredSnapshot{
		Clients: []ports.NativeDesiredClient{{Client: client}},
	}, nodeprotocol.NodeReport{}, nodeprotocol.Version{Epoch: 1, Version: 1}, time.Now())
	if !errors.Is(err, wantErr) {
		t.Fatalf("buildDirectives error = %v, want wrapped %v", err, wantErr)
	}
	if len(body.Quota) != 0 {
		t.Fatalf("failed user load emitted quota directives: %+v", body.Quota)
	}
}

// TestBuildDirectivesEmergencyAccessOverridesExhaustedPeriod pins the native
// equivalent of user.emergencyFloor. Deleting the emergency branch makes the
// finite case fall back to the exhausted normal period and return zero.
func TestBuildDirectivesEmergencyAccessOverridesExhaustedPeriod(t *testing.T) {
	const gib = int64(1 << 30)
	now := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	activeUntil := now.Add(2 * time.Hour)
	expiredAt := now.Add(-time.Second)
	zero := int64(0)
	threeGiB := int64(3 * gib)

	for _, tc := range []struct {
		name        string
		until       *time.Time
		effectiveGB float64
		want        *int64
	}{
		{name: "finite group-scoped emergency quota remeasures from emergency baseline", until: &activeUntil, effectiveGB: 5, want: &threeGiB},
		{name: "zero emergency quota deliberately unarms the gate", until: &activeUntil, effectiveGB: 0, want: nil},
		{name: "expired emergency falls back to exhausted period", until: &expiredAt, effectiveGB: 5, want: &zero},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := &domain.User{
				ID: 7, TrafficLimitBytes: gib, TrafficResetPeriod: domain.ResetMonthly,
				LifetimeTotalBytes: 5 * gib, PeriodBaselineBytes: 0,
				EmergencyUntil: tc.until, EmergencyBaselineBytes: 3 * gib,
			}
			client := &domain.PSPClient{ID: 11, UserID: user.ID, PanelID: 9}
			agent := &domain.NodeAgent{AgentID: "agt_emergency", PanelID: client.PanelID}
			service := &Service{
				users:   emergencyDirectiveUsers{user: user},
				clients: emergencyDirectiveClients{client: client},
				agents:  emergencyDirectiveAgents{agent: agent},
				settings: emergencyDirectiveSettings{
					global:  ports.UISettings{EmergencyAccessQuotaGB: 0},
					perUser: map[int64]ports.UISettings{user.ID: {EmergencyAccessQuotaGB: tc.effectiveGB}},
				},
				reports: make(map[string]receivedFullReport),
				anchors: make(map[int64]nodeprotocol.ClientCounters),
				grants:  make(map[string]map[nodeprotocol.ClientKey]int64),
			}
			body, _, err := service.buildDirectives(t.Context(), agent, &ports.NativeDesiredSnapshot{
				Clients: []ports.NativeDesiredClient{{Client: client}},
			}, nodeprotocol.NodeReport{
				Clients: []nodeprotocol.ClientCounters{{
					Key: nodeprotocol.NewClientKey(client.ID), UpBytes: 100, DownBytes: 50, CounterEpoch: 1,
				}},
			}, nodeprotocol.Version{Epoch: 1, Version: 1}, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(body.Quota) != 1 {
				t.Fatalf("quota entries = %d, want 1", len(body.Quota))
			}
			entry := body.Quota[0]
			if entry.BaselineBytes != 150 {
				t.Fatalf("baseline = %d, want current agent counter 150", entry.BaselineBytes)
			}
			if tc.want == nil {
				if entry.HeadroomBytes != nil {
					t.Fatalf("headroom = %d, want nil (unlimited emergency window)", *entry.HeadroomBytes)
				}
			} else if entry.HeadroomBytes == nil {
				t.Fatalf("headroom = nil, want %d", *tc.want)
			} else if *entry.HeadroomBytes != *tc.want {
				t.Fatalf("headroom = %d, want %d", *entry.HeadroomBytes, *tc.want)
			}
			if entry.NextPeriodHeadroomBytes == nil || *entry.NextPeriodHeadroomBytes != gib {
				t.Fatalf("next-period headroom = %v, want %d", entry.NextPeriodHeadroomBytes, gib)
			}
		})
	}
}
