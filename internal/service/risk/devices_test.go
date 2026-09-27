package risk

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// devicesRow is one account's saved devices row with its evidence decoded.
func devicesRow(t *testing.T, h *harness, uid int64) (domain.RiskSignal, domain.DevicesEvidence) {
	t.Helper()
	r, ok := h.store.saved(t)[uid][domain.RiskKindDevices]
	if !ok {
		t.Fatalf("no devices row saved for user %d", uid)
	}
	var ev domain.DevicesEvidence
	if r.Evidence != nil {
		if err := json.Unmarshal(r.Evidence, &ev); err != nil {
			t.Fatalf("user %d evidence %s: %v", uid, r.Evidence, err)
		}
	}
	return r, ev
}

func wantDevicesRow(t *testing.T, r domain.RiskSignal, state domain.GeoState, code domain.RiskCode) {
	t.Helper()
	if r.State != state || r.Code != code {
		t.Fatalf("user %d devices = %s/%s, want %s/%s (evidence %s)", r.UserID, r.State, r.Code, state, code, r.Evidence)
	}
}

// device is a client of account uid that declares id, fetching from ip.
func device(uid int64, ip, id, label string) func(at time.Time) domain.SubLog {
	return func(at time.Time) domain.SubLog {
		return domain.SubLog{UserID: uid, IP: ip, UA: "Happ/3.4.0", ClientType: "Happ", AccessedAt: at, DeviceID: id, DeviceLabel: label}
	}
}

// The device count is judged per account from the week's fetches: four
// devices each declared every day is over the default limit of three; an
// account whose clients declare nothing cannot be counted (unknown, with its
// clients listed); an account that fetched nothing is idle.
func TestRefresh_DevicesCountDeclaredDevicesPerAccount(t *testing.T) {
	h := newSpreadHarness(usersInGroups(0, 0, 0), rowsOf(
		everyDay(t, device(1, ipHomeGD, "0a1b2c3d4e5f6071", "iOS 17.5 · iPhone15,2")),
		everyDay(t, device(1, ipHomeGD, "1b2c3d4e5f607182", "macOS 15.1 · Mac15,3")),
		everyDay(t, device(1, ipHomeGD, "2c3d4e5f60718293", "iPadOS 18.0 · iPad14,1")),
		everyDay(t, device(1, ipHomeGD, "3d4e5f6071829304", "Android 12 · SHIELD")),
		everyDay(t, client(2, ipHomeGD2, "clash.meta/1.19.2")),
	))
	before := outcomes()
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := devicesRow(t, h, 1)
	wantDevicesRow(t, r, domain.GeoStateFlagged, domain.RiskCodeOver)
	if ev.Recurrent != 4 || ev.Distinct != 4 || ev.MaxDevices != 3 || ev.MinDays != 3 ||
		ev.WindowDays != 7 || ev.WindowStart != "2026-09-19" || ev.FetchesWithHWID != 28 || ev.FetchesWithout != 0 {
		t.Fatalf("user 1 evidence %+v, want 4 recurrent of 4 against 3, 28 declared fetches over the 7 days from 2026-09-19", ev)
	}

	r, ev = devicesRow(t, h, 2)
	wantDevicesRow(t, r, domain.GeoStateUnknown, domain.RiskCodeNoHWID)
	if want := []domain.UAClientEvidence{{Label: "clash.meta/1.19.2", Days: 0x7f}}; !reflect.DeepEqual(ev.Clients, want) || ev.FetchesWithout != 7 {
		t.Fatalf("user 2 clients %+v, fetches without %d; want %+v and 7", ev.Clients, ev.FetchesWithout, want)
	}

	r, _ = devicesRow(t, h, 3)
	wantDevicesRow(t, r, domain.GeoStateIdle, domain.RiskCodeNoFetches)
	if r.Evidence != nil {
		t.Fatalf("an account that fetched nothing has evidence %s", r.Evidence)
	}
	wantOutcome(t, before, "ok")
}

// A device is described by its NEWEST fetch, by when it was fetched rather
// than by scan order: the latest label it declared and the latest client its
// fetches were detected as, and a later fetch that declared neither erases
// neither. Its days and its last fetch count every fetch — with an address
// or without one — and a fetch that declared no device lists its client
// string instead.
func TestRefresh_DevicesUseTheLatestLabelAndClient(t *testing.T) {
	at := func(k, hour int) time.Time { return localDay(t, k, hour) }
	id := "0a1b2c3d4e5f6071"
	h := newSpreadHarness(usersInGroups(0), []domain.SubLog{
		{UserID: 1, IP: ipHomeGD, UA: "Happ/3.4.0", ClientType: "Happ", AccessedAt: at(3, 9), DeviceID: id, DeviceLabel: "Android 15 · Pixel 9"},
		{UserID: 1, IP: ipHunan, UA: "v2rayNG/1.9", ClientType: "v2rayNG", AccessedAt: at(1, 9), DeviceID: id, DeviceLabel: "Android 14 · Pixel 9"},
		{UserID: 1, IP: "", UA: "", ClientType: "", AccessedAt: at(5, 22), DeviceID: id},
		{UserID: 1, IP: ipHomeGD, UA: "clash.meta/1.19.2", ClientType: "mihomo", AccessedAt: at(0, 9)},
		{UserID: 1, IP: "", UA: "clash.meta/1.19.2", ClientType: "mihomo", AccessedAt: at(2, 9)},
	})
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := devicesRow(t, h, 1)
	wantDevicesRow(t, r, domain.GeoStateClean, domain.RiskCodeWithin)
	want := []domain.DeviceEvidence{{
		Label: "Android 15 · Pixel 9", HWID4: "0a1b", Days: 1<<1 | 1<<3 | 1<<5,
		LastMS: at(5, 22).UnixMilli(), Client: "Happ", Recurrent: true,
	}}
	if !reflect.DeepEqual(ev.Devices, want) {
		t.Fatalf("devices = %+v\nwant      %+v", ev.Devices, want)
	}
	if wantClients := []domain.UAClientEvidence{{Label: "clash.meta/1.19.2", Days: 1<<0 | 1<<2}}; !reflect.DeepEqual(ev.Clients, wantClients) {
		t.Fatalf("clients = %+v, want %+v", ev.Clients, wantClients)
	}
	if ev.FetchesWithHWID != 3 || ev.FetchesWithout != 2 {
		t.Fatalf("fetches with %d, without %d; want 3 and 2", ev.FetchesWithHWID, ev.FetchesWithout)
	}
}

// The device count places nothing, so it does not wait for the
// infrastructure set: while the place signals are skipped (infra_pending),
// the window is still read and devices is still judged.
func TestRefresh_DevicesDoNotWaitForInfra(t *testing.T) {
	h := newSpreadHarness(usersInGroups(0), rowsOf(
		everyDay(t, device(1, ipHomeGD, "0a1b2c3d4e5f6071", "iOS 17.5 · iPhone15,2")),
		everyDay(t, device(1, ipHunan, "1b2c3d4e5f607182", "macOS 15.1 · Mac15,3")),
	))
	h.infraLoaded = func() bool { return false }
	before := outcomes()
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := devicesRow(t, h, 1)
	wantDevicesRow(t, r, domain.GeoStateClean, domain.RiskCodeWithin)
	if ev.Distinct != 2 {
		t.Fatalf("evidence %+v, want the two devices counted", ev)
	}
	if _, ok := h.store.saved(t)[1][domain.RiskKindSubSpread]; ok {
		t.Fatal("sub_spread judged before the infrastructure set loaded")
	}
	if len(h.geo.lookups) != 0 {
		t.Fatalf("placed %v while waiting for the infrastructure set", h.geo.lookups)
	}
	wantOutcome(t, before, "infra_pending")
}

// Capture is a GLOBAL switch (risk.hwid_capture_off is not group-overridable):
// with it off every account reads disabled/capture_off with no evidence,
// whatever a group's own settings carry — and with it on, a group's copy
// that says otherwise does not switch its accounts off. The group's own
// risk.devices_off still applies per group.
func TestRefresh_CaptureOffGloballyDisablesDevices(t *testing.T) {
	rows := rowsOf(
		everyDay(t, device(1, ipHomeGD, "0a1b2c3d4e5f6071", "iOS")),
		everyDay(t, device(2, ipHomeGD2, "1b2c3d4e5f607182", "iOS")),
		everyDay(t, device(3, ipHomeGD3, "2c3d4e5f60718293", "iOS")),
	)
	h := newSpreadHarness(usersInGroups(0, 5, 6), rows)
	h.settings.global.RiskHWIDCaptureOff = true
	stale, off := h.settings.global, h.settings.global
	stale.RiskHWIDCaptureOff = false
	off.RiskDevicesOff = true
	h.settings.groups = map[int64]ports.UISettings{5: stale, 6: off}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	for uid, code := range map[int64]domain.RiskCode{1: domain.RiskCodeCaptureOff, 2: domain.RiskCodeCaptureOff, 3: domain.RiskCodeSignalOff} {
		r, _ := devicesRow(t, h, uid)
		wantDevicesRow(t, r, domain.GeoStateDisabled, code)
		if r.Evidence != nil {
			t.Fatalf("user %d: disabled with evidence %s", uid, r.Evidence)
		}
	}

	h = newSpreadHarness(usersInGroups(0, 5), rows)
	captureOff := h.settings.global
	captureOff.RiskHWIDCaptureOff = true
	h.settings.groups = map[int64]ports.UISettings{5: captureOff}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	for uid := int64(1); uid <= 2; uid++ {
		r, _ := devicesRow(t, h, uid)
		wantDevicesRow(t, r, domain.GeoStateClean, domain.RiskCodeWithin)
	}
}

// min_days and max_devices are the group's: a group that allows four
// devices allows the household that is over the global three.
func TestRefresh_GroupDeviceLimitReachesDevices(t *testing.T) {
	var rows []domain.SubLog
	for uid := int64(1); uid <= 2; uid++ {
		for _, id := range []string{"0a1b2c3d4e5f6071", "1b2c3d4e5f607182", "2c3d4e5f60718293", "3d4e5f6071829304"} {
			rows = append(rows, everyDay(t, device(uid, ipHomeGD, id, "iOS"))...)
		}
	}
	h := newSpreadHarness(usersInGroups(5, 0), rows)
	wide := h.settings.global
	wide.RiskMaxDevices = 4
	h.settings.groups = map[int64]ports.UISettings{5: wide}
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, ev := devicesRow(t, h, 1)
	wantDevicesRow(t, r, domain.GeoStateClean, domain.RiskCodeWithin)
	if ev.MaxDevices != 4 {
		t.Fatalf("group 5 limit = %d, want its risk.max_devices 4", ev.MaxDevices)
	}
	r, _ = devicesRow(t, h, 2)
	wantDevicesRow(t, r, domain.GeoStateFlagged, domain.RiskCodeOver)
}
