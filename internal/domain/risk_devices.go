package domain

import "sort"

// devices: how many devices an account declared over the week's fetches.
//
// Some clients send an x-hwid header with every subscription fetch; the sub
// handler keeps a keyed per-account digest of it (never the raw value) and a
// sanitized OS/model label. A DEVICE here is one such digest. It is a count
// of what the clients SAY about themselves — spoofable, and most clients say
// nothing — so it is shown to an admin, never enforced.
//
//   - A device is RECURRENT when it fetched on min_days of the window's
//     days: three of seven is a device the account keeps using, one day is a
//     borrowed phone or a reinstall's leftover id.
//   - More recurrent devices than the limit (risk.max_devices) is flagged;
//     more devices counting the brief ones too is suspect.
//
// Known errors, both toward silence or a bounded over-count. A reinstall
// declares a new id, so one phone can read as two for up to one window —
// which is why the default limit is three, not two. Clients that declare a
// placeholder hwid fold several devices into one: an undercount. Rotating
// the panel's encryption key changes every id, and old and new coexist for
// up to one window.
//
// Evidence never carries a device id: only its first four characters, the
// label and the client name the fetches were detected as.

// Evidence caps: what one row stores and the admin table draws. The verdict
// counts every device; only the listing is bounded.
const (
	RiskEvidenceMaxDevices = 12
	RiskEvidenceMaxClients = 6
)

// DevicePolicy is what devices judges with.
type DevicePolicy struct {
	// Off is the signal's own switch (risk.devices_off, per group).
	// CaptureOff is the GLOBAL x-hwid capture switch: with it off nothing
	// new is declared, and a week of older rows would count a shrinking set.
	Off, CaptureOff bool
	// MinDays is how many window days make a device recurrent, clamped to
	// 1..RiskWindowDays; Max is how many recurrent devices are allowed, at
	// least 1.
	MinDays, Max int
}

// DeviceSighting is one declared device over the window.
type DeviceSighting struct {
	// ID is the stored per-account digest. It identifies the device here
	// and never leaves the evaluator whole: the evidence gets four
	// characters.
	ID string
	// Label is the newest OS/model label the device declared; Client the
	// newest client name its fetches were detected as.
	Label, Client string
	// Days is which window days it fetched on (bit i = day i, 0 = oldest).
	Days uint8
	// LastMS is its newest fetch in the window (unix ms).
	LastMS int64
}

// UAClientSighting is a client that declared no device, known by its client
// string (cut to 64 characters): context for an admin, never counted.
type UAClientSighting struct {
	Label string
	Days  uint8
}

// DeviceInput is one account's fetch window, folded by device.
type DeviceInput struct {
	// WindowDays is how many days the window holds: 7, or the sub-log
	// retention when that is shorter. RetentionDays is
	// sub_log_retention_days as stored (0 = never pruned), shown to explain
	// a short window.
	WindowDays, RetentionDays int
	// WindowStart is the panel-local date of window day 0.
	WindowStart string
	// Fetches is how many fetches the account made in the window; WithHWID
	// how many of them declared a device.
	Fetches, WithHWID int
	Devices           []DeviceSighting
	Clients           []UAClientSighting
}

// DevicesEvidence is what the admin UI draws for devices. The field names
// are a wire contract: the SPA reads them from rows as they were stored.
// Every slice is present, empty rather than null.
type DevicesEvidence struct {
	V           int    `json:"v"`
	WindowDays  int    `json:"window_days"`
	WindowStart string `json:"window_start"`
	// RetentionDays is left out when the logs are never pruned.
	RetentionDays int `json:"retention_days,omitempty"`
	MinDays       int `json:"min_days"`
	MaxDevices    int `json:"max_devices"`
	// Recurrent and Distinct count every device, listed or not.
	Recurrent int `json:"recurrent"`
	Distinct  int `json:"distinct"`
	// Devices are recurrent first, then most days, then by label; at most
	// RiskEvidenceMaxDevices.
	Devices         []DeviceEvidence `json:"devices"`
	FetchesWithHWID int              `json:"fetches_with_hwid"`
	FetchesWithout  int              `json:"fetches_without"`
	// Clients are the clients that declared no device, most days first; at
	// most RiskEvidenceMaxClients.
	Clients []UAClientEvidence `json:"clients"`
}

// DeviceEvidence is one declared device.
type DeviceEvidence struct {
	Label string `json:"label"`
	// HWID4 is the first four characters of the stored digest: enough to
	// tell one account's devices apart, useless for anything else.
	HWID4     string `json:"hwid4"`
	Days      uint8  `json:"days"`
	LastMS    int64  `json:"last_ms"`
	Client    string `json:"client"`
	Recurrent bool   `json:"recurrent"`
}

// UAClientEvidence is one client that declared no device.
type UAClientEvidence struct {
	Label string `json:"label"`
	Days  uint8  `json:"days"`
}

// EvaluateDevices judges how many devices one account declared. The
// branches, first match wins:
//
//  1. Off → disabled / signal_off, no evidence.
//  2. CaptureOff → disabled / capture_off, no evidence.
//  3. nothing fetched → idle / no_fetches, no evidence.
//  4. WindowDays < min_days → unknown / retention_short: the logs are not
//     kept long enough for any device to recur that often.
//  5. no device declared → unknown / no_hwid, with the clients listed.
//  6. recurrent > max → flagged / over; distinct > max → suspect /
//     over_building; otherwise clean / within.
//
// "Cannot tell" is never clean: an account whose clients declare nothing
// reads unknown, however few devices it may really have.
//
// Sightings are folded by id first, so one device is counted once whatever
// produced the list: days joined, the newer fetch's label and client kept.
// A sighting on no window day, or with no id, is not a device.
func EvaluateDevices(p DevicePolicy, in DeviceInput) (RiskVerdict, *DevicesEvidence) {
	minDays := min(max(p.MinDays, 1), RiskWindowDays)
	limit := max(p.Max, 1)
	switch {
	case p.Off:
		return RiskVerdict{State: GeoStateDisabled, Code: RiskCodeSignalOff}, nil
	case p.CaptureOff:
		return RiskVerdict{State: GeoStateDisabled, Code: RiskCodeCaptureOff}, nil
	case in.Fetches <= 0:
		return RiskVerdict{State: GeoStateIdle, Code: RiskCodeNoFetches}, nil
	}

	// The window and the fetch counts are known whichever guard stops, so
	// every verdict from here on carries them.
	with := min(max(in.WithHWID, 0), in.Fetches)
	ev := &DevicesEvidence{
		V:               RiskEvidenceVersion,
		WindowDays:      in.WindowDays,
		WindowStart:     in.WindowStart,
		RetentionDays:   max(in.RetentionDays, 0),
		MinDays:         minDays,
		MaxDevices:      limit,
		Devices:         []DeviceEvidence{},
		FetchesWithHWID: with,
		FetchesWithout:  in.Fetches - with,
		Clients:         []UAClientEvidence{},
	}
	if in.WindowDays < minDays {
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeRetentionShort}, ev
	}
	ev.Clients = clientEvidence(in.Clients)

	devices := foldDevices(in.Devices)
	if len(devices) == 0 {
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeNoHWID}, ev
	}
	type row struct {
		id string
		ev DeviceEvidence
	}
	rows := make([]row, 0, len(devices))
	for _, d := range devices {
		recurrent := DayCount(d.Days) >= minDays
		if recurrent {
			ev.Recurrent++
		}
		rows = append(rows, row{d.ID, DeviceEvidence{
			Label: d.Label, HWID4: firstRunes(d.ID, 4), Days: d.Days, LastMS: d.LastMS,
			Client: d.Client, Recurrent: recurrent,
		}})
	}
	ev.Distinct = len(devices)
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].ev, rows[j].ev
		if a.Recurrent != b.Recurrent {
			return a.Recurrent
		}
		if da, db := DayCount(a.Days), DayCount(b.Days); da != db {
			return da > db
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		// The id never leaves this function; it only makes two otherwise
		// identical rows order the same way every hour.
		return rows[i].id < rows[j].id
	})
	for _, r := range rows[:min(len(rows), RiskEvidenceMaxDevices)] {
		ev.Devices = append(ev.Devices, r.ev)
	}

	switch {
	case ev.Recurrent > limit:
		return RiskVerdict{State: GeoStateFlagged, Code: RiskCodeOver}, ev
	case ev.Distinct > limit:
		return RiskVerdict{State: GeoStateSuspect, Code: RiskCodeOverBuilding}, ev
	}
	return RiskVerdict{State: GeoStateClean, Code: RiskCodeWithin}, ev
}

// foldDevices merges sightings of one id — days joined, the newer fetch's
// label and client kept unless it declared none — and drops the ones that
// are not devices: no id, or no window day.
func foldDevices(in []DeviceSighting) []DeviceSighting {
	by := make(map[string]*DeviceSighting, len(in))
	var order []string
	for _, s := range in {
		if s.ID == "" || s.Days == 0 {
			continue
		}
		d := by[s.ID]
		if d == nil {
			c := s
			by[s.ID] = &c
			order = append(order, s.ID)
			continue
		}
		d.Days |= s.Days
		if s.LastMS > d.LastMS {
			d.LastMS = s.LastMS
			if s.Label != "" {
				d.Label = s.Label
			}
			if s.Client != "" {
				d.Client = s.Client
			}
		} else {
			if d.Label == "" {
				d.Label = s.Label
			}
			if d.Client == "" {
				d.Client = s.Client
			}
		}
	}
	out := make([]DeviceSighting, 0, len(order))
	for _, id := range order {
		out = append(out, *by[id])
	}
	return out
}

// clientEvidence lists the clients that declared no device, one per label
// (days joined), most days first, then by label, capped. A client on no
// window day is not listed.
func clientEvidence(in []UAClientSighting) []UAClientEvidence {
	days := map[string]uint8{}
	for _, c := range in {
		if c.Days != 0 {
			days[c.Label] |= c.Days
		}
	}
	out := make([]UAClientEvidence, 0, len(days))
	for label, mask := range days {
		out = append(out, UAClientEvidence{Label: label, Days: mask})
	}
	sort.Slice(out, func(i, j int) bool {
		if da, db := DayCount(out[i].Days), DayCount(out[j].Days); da != db {
			return da > db
		}
		return out[i].Label < out[j].Label
	})
	return out[:min(len(out), RiskEvidenceMaxClients)]
}

// firstRunes is s cut to at most n characters, never inside one. A digest
// is ASCII only because today's hasher makes it so; a cut inside a
// character would store invalid UTF-8.
func firstRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
