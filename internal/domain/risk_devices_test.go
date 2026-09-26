package domain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The devices of the fixtures. An id is what the store keeps: a keyed
// per-account digest of the declared x-hwid, 16 lower-case hex characters.
const (
	devPhone    = "0a1b2c3d4e5f6071"
	devPhoneNew = "9f8e7d6c5b4a3921" // the same phone after a reinstall
	devLaptop   = "1b2c3d4e5f607182"
	devTablet   = "2c3d4e5f60718293"
	devTV       = "3d4e5f6071829304"
)

// lastSeen is a fixed fetch time (unix ms) the fixtures stamp devices with.
const lastSeen int64 = 1_790_000_000_000

func declared(id, label string, days uint8) DeviceSighting {
	return DeviceSighting{ID: id, Label: label, Client: "Happ", Days: days, LastMS: lastSeen}
}

// devicePolicy is the shipped policy: a device seen on 3 of the week's days
// is one the account uses, and 3 of those are allowed.
func devicePolicy() DevicePolicy {
	return DevicePolicy{MinDays: RiskDefaultMinDays, Max: RiskDefaultMaxDevices}
}

// deviceInput is a fetched seven-day window in which every fetch declared a
// device.
func deviceInput(devices ...DeviceSighting) DeviceInput {
	return DeviceInput{
		WindowDays: RiskWindowDays, WindowStart: "2026-09-19",
		Fetches: 30, WithHWID: 30,
		Devices: devices,
	}
}

func wantDevices(t *testing.T, v RiskVerdict, state GeoState, code RiskCode) {
	t.Helper()
	if v.State != state || v.Code != code {
		t.Fatalf("verdict = %s/%s, want %s/%s", v.State, v.Code, state, code)
	}
}

func mustDevicesEvidence(t *testing.T, ev *DevicesEvidence) *DevicesEvidence {
	t.Helper()
	if ev == nil {
		t.Fatal("no evidence for a verdict that judged something")
	}
	return ev
}

// fourDevices is input that would be flagged if it were judged, for the
// guards that must stop it first.
func fourDevices() DeviceInput {
	return deviceInput(
		declared(devPhone, "iOS 17.5 · iPhone15,2", week),
		declared(devLaptop, "macOS 15.1 · Mac15,3", span(0, 4)),
		declared(devTablet, "iPadOS 18.0 · iPad14,1", on(1, 3, 5)),
		declared(devTV, "Android 12 · SHIELD", span(2, 6)),
	)
}

// A phone reinstalled mid-week declares a new id, so the week holds two ids
// for one phone, each on enough days to count. With a laptop beside it that
// is three — within the default limit, which is why the default is three
// and not two: one reinstall must not turn an ordinary household suspect.
func TestDevices_OneReinstallFitsTheDefaultLimit(t *testing.T) {
	v, ev := EvaluateDevices(devicePolicy(), deviceInput(
		declared(devPhone, "iOS 17.5 · iPhone15,2", span(0, 2)),
		declared(devPhoneNew, "iOS 17.5 · iPhone15,2", span(3, 6)),
		declared(devLaptop, "macOS 15.1 · Mac15,3", week),
	))
	wantDevices(t, v, GeoStateClean, RiskCodeWithin)
	if ev = mustDevicesEvidence(t, ev); ev.Recurrent != 3 || ev.Distinct != 3 {
		t.Fatalf("recurrent %d, distinct %d; want 3 and 3", ev.Recurrent, ev.Distinct)
	}
}

// Four devices each used on several days of the week: more devices the
// account keeps using than the limit allows.
func TestDevices_FourRecurrentDevicesAreFlagged(t *testing.T) {
	v, ev := EvaluateDevices(devicePolicy(), fourDevices())
	wantDevices(t, v, GeoStateFlagged, RiskCodeOver)
	if ev = mustDevicesEvidence(t, ev); ev.Recurrent != 4 || ev.Distinct != 4 || ev.MaxDevices != 3 {
		t.Fatalf("recurrent %d, distinct %d, max %d; want 4, 4 and 3", ev.Recurrent, ev.Distinct, ev.MaxDevices)
	}
}

// A fourth device seen on fewer days than min_days — a borrowed phone, a
// reinstall late in the week — is not a habit yet: over the limit counting
// every device, within it counting the recurring ones. Worth a look, not a
// flag.
func TestDevices_OccasionalFourthDeviceIsSuspect(t *testing.T) {
	for _, days := range []uint8{on(5), on(2, 6)} {
		v, ev := EvaluateDevices(devicePolicy(), deviceInput(
			declared(devPhone, "iOS 17.5 · iPhone15,2", week),
			declared(devLaptop, "macOS 15.1 · Mac15,3", span(0, 4)),
			declared(devTablet, "iPadOS 18.0 · iPad14,1", on(1, 3, 5)),
			declared(devTV, "Android 12 · SHIELD", days),
		))
		wantDevices(t, v, GeoStateSuspect, RiskCodeOverBuilding)
		if ev = mustDevicesEvidence(t, ev); ev.Recurrent != 3 || ev.Distinct != 4 {
			t.Fatalf("fourth device on %07b: recurrent %d, distinct %d; want 3 and 4", days, ev.Recurrent, ev.Distinct)
		}
	}
}

// Most clients declare no device at all. With none declared there is
// nothing to count — unknown, never clean — and the evidence lists the
// clients that did fetch, so the admin sees why.
func TestDevices_NoHWIDIsUnknownAndListsClients(t *testing.T) {
	v, ev := EvaluateDevices(devicePolicy(), DeviceInput{
		WindowDays: RiskWindowDays, WindowStart: "2026-09-19", Fetches: 12,
		Clients: []UAClientSighting{
			{Label: "ClashX Pro/1.118.0", Days: on(2)},
			{Label: "clash.meta/1.19.2", Days: week},
		},
	})
	wantDevices(t, v, GeoStateUnknown, RiskCodeNoHWID)
	ev = mustDevicesEvidence(t, ev)
	want := []UAClientEvidence{{Label: "clash.meta/1.19.2", Days: week}, {Label: "ClashX Pro/1.118.0", Days: on(2)}}
	if !reflect.DeepEqual(ev.Clients, want) {
		t.Fatalf("clients = %+v, want %+v (most days first)", ev.Clients, want)
	}
	if ev.Devices == nil || len(ev.Devices) != 0 || ev.Recurrent != 0 || ev.Distinct != 0 {
		t.Fatalf("devices %#v, recurrent %d, distinct %d; want an empty list and zeros", ev.Devices, ev.Recurrent, ev.Distinct)
	}
	if ev.FetchesWithHWID != 0 || ev.FetchesWithout != 12 {
		t.Fatalf("fetches with %d, without %d; want 0 and 12", ev.FetchesWithHWID, ev.FetchesWithout)
	}
}

func TestDevices_NoFetchIsIdle(t *testing.T) {
	v, ev := EvaluateDevices(devicePolicy(), DeviceInput{WindowDays: RiskWindowDays, WindowStart: "2026-09-19"})
	wantDevices(t, v, GeoStateIdle, RiskCodeNoFetches)
	if ev != nil {
		t.Fatalf("evidence %+v for an account that fetched nothing, want none", ev)
	}
}

// With x-hwid capture switched off (a global setting) nothing new is
// declared, and the week's older rows would count a shrinking set: disabled,
// with no evidence, for every account.
func TestDevices_CaptureOffIsDisabled(t *testing.T) {
	p := devicePolicy()
	p.CaptureOff = true
	v, ev := EvaluateDevices(p, fourDevices())
	wantDevices(t, v, GeoStateDisabled, RiskCodeCaptureOff)
	if ev != nil {
		t.Fatalf("evidence %+v with capture off, want none", ev)
	}
}

// The signal's own switch reads disabled with no evidence, and it beats
// every other guard, capture included: the admin switched this signal off,
// and that is the reason to show.
func TestDevices_OffIsDisabled(t *testing.T) {
	p := devicePolicy()
	p.Off, p.CaptureOff = true, true
	v, ev := EvaluateDevices(p, fourDevices())
	wantDevices(t, v, GeoStateDisabled, RiskCodeSignalOff)
	if ev != nil {
		t.Fatalf("evidence %+v for a switched-off signal, want none", ev)
	}
}

// Sub-log retention shorter than min_days leaves a window in which no device
// can recur often enough to count: unknown, never clean — and never flagged
// either, whatever the distinct count. The evidence says how long the logs
// are kept and how many days were needed.
func TestDevices_RetentionShortIsUnknown(t *testing.T) {
	in := fourDevices()
	in.WindowDays, in.RetentionDays = 2, 2
	v, ev := EvaluateDevices(devicePolicy(), in)
	wantDevices(t, v, GeoStateUnknown, RiskCodeRetentionShort)
	ev = mustDevicesEvidence(t, ev)
	if ev.WindowDays != 2 || ev.RetentionDays != 2 || ev.MinDays != 3 {
		t.Fatalf("evidence %+v, want a 2-day window, retention 2, min_days 3", ev)
	}
	if ev.Devices == nil || ev.Clients == nil {
		t.Fatalf("a guard's evidence has a nil list: %+v", ev)
	}

	// Two days are enough when two days make a device recurrent.
	p := devicePolicy()
	p.MinDays = 2
	v, _ = EvaluateDevices(p, in)
	wantDevices(t, v, GeoStateFlagged, RiskCodeOver)
}

// The stored id is a keyed digest, but it is still a stable per-account
// handle; the evidence carries only its first four characters, enough to
// tell an admin's devices apart in one row and useless for anything else.
func TestDevices_EvidenceHoldsOnlyThePrefix(t *testing.T) {
	in := fourDevices()
	_, ev := EvaluateDevices(devicePolicy(), in)
	ev = mustDevicesEvidence(t, ev)
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range in.Devices {
		for _, leak := range []string{d.ID, d.ID[:5]} {
			if strings.Contains(string(raw), leak) {
				t.Fatalf("evidence %s contains %q of device id %s", raw, leak, d.ID)
			}
		}
		if !strings.Contains(string(raw), `"hwid4":"`+d.ID[:4]+`"`) {
			t.Fatalf("evidence %s lacks the prefix %q of device id %s", raw, d.ID[:4], d.ID)
		}
	}
	if m := regexp.MustCompile(`[0-9a-fA-F]{16}`).FindString(string(raw)); m != "" {
		t.Fatalf("evidence carries a 16-hex id %q: %s", m, raw)
	}
}

// MinDays is clamped to 1..7 like every risk signal's, and the limit is at
// least one device: a limit of 0 would put every account that declares a
// device over it.
func TestDevices_PolicyIsRepairedTowardSilence(t *testing.T) {
	_, ev := EvaluateDevices(DevicePolicy{MinDays: 0, Max: 0}, deviceInput(declared(devPhone, "iOS", on(3))))
	if ev = mustDevicesEvidence(t, ev); ev.MinDays != 1 || ev.MaxDevices != 1 {
		t.Fatalf("min_days %d, max_devices %d from a zero policy; want 1 and 1", ev.MinDays, ev.MaxDevices)
	}
	v, _ := EvaluateDevices(DevicePolicy{MinDays: 0, Max: 0}, deviceInput(declared(devPhone, "iOS", on(3))))
	wantDevices(t, v, GeoStateClean, RiskCodeWithin)

	v, ev = EvaluateDevices(DevicePolicy{MinDays: 9, Max: 3}, fourDevices())
	// Clamped to 7, only the phone (every day) recurs; four are seen.
	wantDevices(t, v, GeoStateSuspect, RiskCodeOverBuilding)
	if ev = mustDevicesEvidence(t, ev); ev.MinDays != RiskWindowDays || ev.Recurrent != 1 {
		t.Fatalf("min_days %d, recurrent %d; want %d and 1", ev.MinDays, ev.Recurrent, RiskWindowDays)
	}
}

// One device is one device: two sightings of the same id (whatever produced
// them) count once, with their days joined and the newer fetch's label and
// client. A sighting on no window day, or without an id, is not a device.
// Fetch counts never go negative.
func TestDevices_DuplicateAndEmptySightingsAreNotDevices(t *testing.T) {
	older := DeviceSighting{ID: devPhone, Label: "iOS 17.4 · iPhone15,2", Client: "Shadowrocket", Days: on(0, 1), LastMS: lastSeen - 1}
	newer := DeviceSighting{ID: devPhone, Label: "iOS 17.5 · iPhone15,2", Client: "Happ", Days: on(2), LastMS: lastSeen}
	want := []DeviceEvidence{{Label: "iOS 17.5 · iPhone15,2", HWID4: "0a1b", Days: on(0, 1, 2), LastMS: lastSeen, Client: "Happ", Recurrent: true}}
	// Whichever order the two arrive in, the newer one names the device.
	for _, pair := range [][2]DeviceSighting{{newer, older}, {older, newer}} {
		in := deviceInput(pair[0], pair[1], declared(devLaptop, "macOS", 0), declared("", "nameless", week))
		in.Fetches, in.WithHWID = 5, 9
		v, ev := EvaluateDevices(DevicePolicy{MinDays: 3, Max: 1}, in)
		wantDevices(t, v, GeoStateClean, RiskCodeWithin)
		ev = mustDevicesEvidence(t, ev)
		if !reflect.DeepEqual(ev.Devices, want) || ev.Distinct != 1 || ev.Recurrent != 1 {
			t.Fatalf("devices %+v (distinct %d, recurrent %d), want %+v", ev.Devices, ev.Distinct, ev.Recurrent, want)
		}
		if ev.FetchesWithHWID != 5 || ev.FetchesWithout != 0 {
			t.Fatalf("fetches with %d, without %d; want 5 and 0", ev.FetchesWithHWID, ev.FetchesWithout)
		}
	}
}

// What one row stores is bounded — the verdict counts every device, the
// listing shows at most 12 and at most 6 clients — and ordered for reading:
// recurring devices first, then most days, then by label; clients by most
// days, then label. Every list is present, empty rather than null.
func TestDevices_EvidenceIsBoundedAndOrdered(t *testing.T) {
	var devices []DeviceSighting
	for i := range 14 {
		// Devices 0..4 recur on 3..7 days; 5..13 are seen on 1 or 2.
		days := span(0, 2+i)
		if i >= 5 {
			days = span(0, i%2)
		}
		devices = append(devices, declared(fmt.Sprintf("%02x3d4e5f60718293", i), fmt.Sprintf("device-%02d", i), days))
	}
	in := deviceInput(devices...)
	for i := range 8 {
		in.Clients = append(in.Clients, UAClientSighting{Label: fmt.Sprintf("client-%02d", i), Days: span(0, i%4)})
	}
	v, ev := EvaluateDevices(devicePolicy(), in)
	wantDevices(t, v, GeoStateFlagged, RiskCodeOver)
	ev = mustDevicesEvidence(t, ev)
	if ev.Distinct != 14 || ev.Recurrent != 5 {
		t.Fatalf("distinct %d, recurrent %d; want every device counted: 14 and 5", ev.Distinct, ev.Recurrent)
	}
	var labels []string
	for _, d := range ev.Devices {
		labels = append(labels, d.Label)
	}
	wantLabels := []string{
		"device-04", "device-03", "device-02", "device-01", "device-00", // recurring, most days first
		"device-05", "device-07", "device-09", "device-11", "device-13", // two days, by label
		"device-06", "device-08", // one day, by label; 10 and 12 cut
	}
	if !reflect.DeepEqual(labels, wantLabels) {
		t.Fatalf("devices = %v\nwant      %v", labels, wantLabels)
	}
	for i, d := range ev.Devices {
		if d.Recurrent != (i < 5) {
			t.Fatalf("device %s recurrent = %v", d.Label, d.Recurrent)
		}
	}
	var clients []string
	for _, c := range ev.Clients {
		clients = append(clients, c.Label)
	}
	if want := []string{"client-03", "client-07", "client-02", "client-06", "client-01", "client-05"}; !reflect.DeepEqual(clients, want) {
		t.Fatalf("clients = %v, want %v", clients, want)
	}

	_, ev = EvaluateDevices(devicePolicy(), deviceInput(declared(devPhone, "iOS", week)))
	if ev = mustDevicesEvidence(t, ev); ev.Clients == nil || ev.Devices == nil {
		t.Fatalf("a nil list in %+v", ev)
	}
}

// The evidence's JSON field names are a wire contract: the SPA reads them
// from rows written by older builds too.
func TestDevices_EvidenceShape(t *testing.T) {
	in := fourDevices()
	in.RetentionDays = 30
	in.Fetches, in.WithHWID = 40, 30
	in.Clients = []UAClientSighting{{Label: "clash.meta/1.19.2", Days: on(4)}}
	_, ev := EvaluateDevices(devicePolicy(), in)
	ev = mustDevicesEvidence(t, ev)
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	wantKeys := func(what string, obj map[string]json.RawMessage, keys ...string) {
		t.Helper()
		var got []string
		for k := range obj {
			got = append(got, k)
		}
		sort.Strings(got)
		sort.Strings(keys)
		if !reflect.DeepEqual(got, keys) {
			t.Fatalf("%s keys = %v, want %v", what, got, keys)
		}
	}
	wantKeys("evidence", doc, "v", "window_days", "window_start", "retention_days", "min_days", "max_devices",
		"recurrent", "distinct", "devices", "fetches_with_hwid", "fetches_without", "clients")
	var devices, clients []map[string]json.RawMessage
	if err := json.Unmarshal(doc["devices"], &devices); err != nil || len(devices) == 0 {
		t.Fatalf("devices %s: %v", doc["devices"], err)
	}
	wantKeys("device", devices[0], "label", "hwid4", "days", "last_ms", "client", "recurrent")
	if err := json.Unmarshal(doc["clients"], &clients); err != nil || len(clients) != 1 {
		t.Fatalf("clients %s: %v", doc["clients"], err)
	}
	wantKeys("client", clients[0], "label", "days")
	if ev.V != RiskEvidenceVersion || ev.WindowStart != "2026-09-19" || ev.RetentionDays != 30 ||
		ev.FetchesWithHWID != 30 || ev.FetchesWithout != 10 {
		t.Fatalf("v %d, window_start %q, retention %d, fetches %d/%d", ev.V, ev.WindowStart, ev.RetentionDays, ev.FetchesWithHWID, ev.FetchesWithout)
	}
	want := DeviceEvidence{Label: "iOS 17.5 · iPhone15,2", HWID4: "0a1b", Days: week, LastMS: lastSeen, Client: "Happ", Recurrent: true}
	if ev.Devices[0] != want {
		t.Fatalf("first device = %+v, want %+v", ev.Devices[0], want)
	}
	// Without a retention that shortened anything, the key is left out.
	in.RetentionDays = 0
	_, ev = EvaluateDevices(devicePolicy(), in)
	if raw, _ = json.Marshal(ev); strings.Contains(string(raw), "retention_days") {
		t.Fatalf("retention_days present for retention 0: %s", raw)
	}
}

// deviceFixtures reaches every code devices can return, one fixture each.
func deviceFixtures() []struct {
	p  DevicePolicy
	in DeviceInput
} {
	with := func(f func(*DevicePolicy)) DevicePolicy { p := devicePolicy(); f(&p); return p }
	change := func(f func(*DeviceInput)) DeviceInput { in := fourDevices(); f(&in); return in }
	return []struct {
		p  DevicePolicy
		in DeviceInput
	}{
		{with(func(p *DevicePolicy) { p.Off = true }), fourDevices()},
		{with(func(p *DevicePolicy) { p.CaptureOff = true }), fourDevices()},
		{devicePolicy(), change(func(in *DeviceInput) { in.Fetches = 0 })},
		{devicePolicy(), change(func(in *DeviceInput) { in.WindowDays = 2 })},
		{devicePolicy(), change(func(in *DeviceInput) { in.Devices, in.WithHWID = nil, 0 })},
		{devicePolicy(), fourDevices()},
		{devicePolicy(), change(func(in *DeviceInput) { in.Devices[3].Days = on(6) })},
		{devicePolicy(), change(func(in *DeviceInput) { in.Devices = in.Devices[:3] })},
	}
}

// AllRiskCodes()[devices] is the list the SPA's locale keys are checked
// against, so it must be exactly the codes this evaluator can return.
func TestDevices_CodesAreExactlyAllRiskCodes(t *testing.T) {
	reached := map[RiskCode]bool{}
	for _, f := range deviceFixtures() {
		v, _ := EvaluateDevices(f.p, f.in)
		reached[v.Code] = true
	}
	listed := map[RiskCode]bool{}
	for _, c := range AllRiskCodes()[RiskKindDevices] {
		listed[c] = true
	}
	if !reflect.DeepEqual(reached, listed) {
		t.Fatalf("devices codes reached %v, AllRiskCodes lists %v", reached, listed)
	}
}
