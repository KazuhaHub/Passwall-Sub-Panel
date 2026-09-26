package risk

import (
	"context"
	"fmt"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// devices judges how many devices each account declared over the fetch
// window (domain.EvaluateDevices) and appends one devices row per account of
// a readable group.
//
// It places nothing — a declared device is the same device from any
// address — so it needs neither the geo database nor the infrastructure set,
// and runs whenever the window was read in full. The capture switch is the
// GLOBAL setting: risk.hwid_capture_off is not group-overridable, and a
// group's copy of it is not what the sub handler obeyed when it recorded the
// week. min_days and the limit are the group's.
func (s *Service) devices(ctx context.Context, r *refresh, w *fetchWindow) error {
	for _, u := range r.users {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("risk refresh: %w", err)
		}
		policy, ok := r.policies[u.GroupID]
		if !ok {
			continue // unreadable group: previous rows kept
		}
		in := domain.DeviceInput{
			WindowDays:    w.days,
			RetentionDays: max(r.global.SubLogRetentionDays, 0),
			WindowStart:   w.start,
		}
		if uw := w.users[u.ID]; uw != nil {
			in.Fetches, in.WithHWID = uw.fetches, uw.withHWID
			in.Devices, in.Clients = uw.deviceSightings()
		}
		v, ev := domain.EvaluateDevices(domain.DevicePolicy{
			Off:        policy.risk.DevicesOff,
			CaptureOff: r.global.RiskHWIDCaptureOff,
			MinDays:    policy.risk.MinDays,
			Max:        policy.risk.MaxDevices,
		}, in)
		addVerdict(r, u.ID, domain.RiskKindDevices, v, ev)
	}
	return nil
}

// deviceSightings splits an account's clients into the devices they declared
// and the clients that declared none. The id handed to the evaluator is the
// stored digest; the evaluator keeps four characters of it.
func (uw *userWindow) deviceSightings() ([]domain.DeviceSighting, []domain.UAClientSighting) {
	var devices []domain.DeviceSighting
	var clients []domain.UAClientSighting
	for key, agg := range uw.identities {
		if agg.kind == "hwid" {
			devices = append(devices, domain.DeviceSighting{
				ID: strings.TrimPrefix(key, "d:"), Label: agg.label, Client: agg.client,
				Days: agg.days, LastMS: agg.lastMS,
			})
			continue
		}
		clients = append(clients, domain.UAClientSighting{Label: agg.label, Days: agg.days})
	}
	return devices, clients
}
