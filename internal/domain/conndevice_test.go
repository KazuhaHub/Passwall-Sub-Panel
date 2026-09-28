package domain

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The live view names the device behind a connection by INFERENCE: the
// panels report who is connected from where, never with what, so the view
// looks for the same account's subscription fetches from the same source.
// These pin what may be matched, how a client is told apart from another,
// and how much of it is shown.

func fetchAt(uid int64, ip, ua, client, device string, at time.Time) SubLog {
	return SubLog{UserID: uid, IP: ip, UA: ua, ClientType: client, DeviceID: device, AccessedAt: at}
}

// A connection's device is read only from its own account's fetches, from
// the same SOURCE (an IPv6 privacy address rotates inside its /64, so a
// fetch from another member of the /64 is the same source), and only from
// fetches at or after since: a fetch from another account, another source or
// before the window says nothing about this connection.
func TestInferConnectionDevices_MatchesTheSameSourceOnly(t *testing.T) {
	since := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	conns := []LiveConnection{
		{UserID: 7, PanelID: 1, Node: "n1", SourceKey: "2001:db8:1:2::/64", IP: "2001:db8:1:2::5"},
		{UserID: 7, PanelID: 2, SourceKey: "203.0.113.7", IP: "203.0.113.7"},
	}
	fetches := []SubLog{
		fetchAt(7, "2001:db8:1:2::99", "clash-verge/2.0", "mihomo", "", since.Add(time.Hour)), // another member of the /64
		fetchAt(7, "2001:db8:1:3::1", "other-app/1.0", "mihomo", "", since.Add(time.Hour)),    // another /64
		fetchAt(8, "203.0.113.7", "stranger/1.0", "mihomo", "", since.Add(time.Hour)),         // another account
		fetchAt(7, "203.0.113.7", "old-app/1.0", "mihomo", "", since.Add(-time.Second)),       // before the window
		fetchAt(7, "::ffff:203.0.113.7", "v2rayN/7.0", "uri-list", "", since),                 // at since, mapped form
		fetchAt(7, "", "no-address/1.0", "mihomo", "", since.Add(time.Minute)),                // no source at all
		fetchAt(7, "198.51.100.1", "elsewhere/1.0", "mihomo", "", since.Add(2*time.Hour)),     // no connection there
	}

	got := InferConnectionDevices(conns, fetches, since)

	want := map[UserSource][]string{
		{UserID: 7, SourceKey: "2001:db8:1:2::/64"}: {"clash-verge/2.0"},
		{UserID: 7, SourceKey: "203.0.113.7"}:       {"v2rayN/7.0"},
	}
	if len(got) != len(want) {
		t.Fatalf("inferred for %d sources (%+v), want %d", len(got), got, len(want))
	}
	for key, uas := range want {
		devices := got[key]
		var gotUAs []string
		for _, d := range devices {
			gotUAs = append(gotUAs, d.UA)
		}
		if !reflect.DeepEqual(gotUAs, uas) {
			t.Errorf("%+v: devices %v, want %v", key, gotUAs, uas)
		}
	}
}

// A client is told apart from another by THE rule the risk worker's fetch
// window uses (SubLogIdentity): the device id it declared, else its exact
// client string. The client type is the panel's guess from the user agent
// and changes when a client asks for another format; it is not an identity.
// Two fetches with the same client string are one device whatever format
// they asked for, and the device shows the newest format. A declared id is
// one device across client versions, showing the newest client string and
// label.
func TestInferConnectionDevices_UsesTheWindowIdentityRule(t *testing.T) {
	since := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	conns := []LiveConnection{{UserID: 7, PanelID: 1, SourceKey: "203.0.113.7", IP: "203.0.113.7"}}
	withLabel := func(l SubLog, label string) SubLog { l.DeviceLabel = label; return l }
	fetches := []SubLog{
		fetchAt(7, "203.0.113.7", "clash.meta/1.19", "mihomo", "", since.Add(time.Minute)),
		fetchAt(7, "203.0.113.7", "clash.meta/1.19", "sing-box", "", since.Add(3*time.Minute)),
		withLabel(fetchAt(7, "203.0.113.7", "Happ/3.12", "mihomo", "beefcafe12345678", since.Add(2*time.Minute)), "iOS 17.4 · iPhone15,2"),
		withLabel(fetchAt(7, "203.0.113.7", "Happ/3.13", "mihomo", "beefcafe12345678", since.Add(4*time.Minute)), "iOS 17.5 · iPhone15,2"),
		fetchAt(7, "203.0.113.7", "Happ/3.13", "mihomo", "beefcafe12345678", since.Add(5*time.Minute)), // declared no label this time
	}

	got := InferConnectionDevices(conns, fetches, since)[UserSource{UserID: 7, SourceKey: "203.0.113.7"}]

	want := []ConnDevice{
		{Label: "iOS 17.5 · iPhone15,2", DeviceID4: "beef", ClientType: "mihomo", UA: "Happ/3.13", Fetches: 3,
			LastAtMS: since.Add(5 * time.Minute).UnixMilli()},
		{ClientType: "sing-box", UA: "clash.meta/1.19", Fetches: 2, LastAtMS: since.Add(3 * time.Minute).UnixMilli()},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("devices =\n%+v\nwant\n%+v", got, want)
	}

	for _, c := range []struct {
		in        SubLog
		key, kind string
	}{
		{SubLog{UA: "clash.meta/1.19", ClientType: "mihomo"}, "u:clash.meta/1.19", SubLogIdentityUA},
		{SubLog{UA: "clash.meta/1.19", ClientType: "sing-box"}, "u:clash.meta/1.19", SubLogIdentityUA},
		{SubLog{UA: "Happ/3.13", DeviceID: "beefcafe12345678"}, "d:beefcafe12345678", SubLogIdentityHWID},
	} {
		if key, kind := SubLogIdentity(c.in); key != c.key || kind != c.kind {
			t.Errorf("SubLogIdentity(%+v) = (%q, %q), want (%q, %q)", c.in, key, kind, c.key, c.kind)
		}
	}
}

// An internal address (a private or CGNAT range) and PSP's own node or
// relay address are not the account's egress: a fetch "from" them came
// through a tunnel or a relay, and naming a device there would pin a device
// on every account behind the same relay. A listed address (the admin's
// office exit) and a shared carrier exit are still the account's own egress
// and are matched.
func TestInferConnectionDevices_SkipsInfraAndInternal(t *testing.T) {
	since := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	ips := map[string]string{
		"10.0.0.8":     AddressExcludedInternal,
		"203.0.113.9":  AddressExcludedInfra,
		"198.51.100.1": AddressExcludedListed,
		"198.51.100.2": AddressExcludedShared,
		"192.0.2.44":   "",
	}
	var conns []LiveConnection
	var fetches []SubLog
	for ip, ex := range ips {
		conns = append(conns, LiveConnection{UserID: 7, PanelID: 1, SourceKey: ip, IP: ip, Exclusion: ex})
		fetches = append(fetches, fetchAt(7, ip, "clash/"+ip, "mihomo", "", since.Add(time.Minute)))
	}

	got := InferConnectionDevices(conns, fetches, since)

	for ip, ex := range ips {
		_, inferred := got[UserSource{UserID: 7, SourceKey: ip}]
		wantInferred := ex != AddressExcludedInternal && ex != AddressExcludedInfra
		if inferred != wantInferred {
			t.Errorf("source %s (exclusion %q): inferred %v, want %v", ip, ex, inferred, wantInferred)
		}
	}
}

// At most ConnDevicesMax devices per source, the most recently seen first:
// a household NAT behind which a dozen clients fetch shows the few that
// fetched last, never an unbounded list. The client string is cut to
// ConnDeviceUARunes characters, on a character boundary.
func TestInferConnectionDevices_NewestFirstCapped(t *testing.T) {
	since := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	conns := []LiveConnection{{UserID: 7, PanelID: 1, SourceKey: "203.0.113.7", IP: "203.0.113.7"}}
	long := strings.Repeat("客", ConnDeviceUARunes+10)
	var fetches []SubLog
	for i, ua := range []string{"a/1", "b/1", "c/1", "d/1", long} {
		fetches = append(fetches, fetchAt(7, "203.0.113.7", ua, "mihomo", "", since.Add(time.Duration(i)*time.Minute)))
	}

	got := InferConnectionDevices(conns, fetches, since)[UserSource{UserID: 7, SourceKey: "203.0.113.7"}]

	if len(got) != ConnDevicesMax {
		t.Fatalf("%d devices, want the cap of %d: %+v", len(got), ConnDevicesMax, got)
	}
	if got[1].UA != "d/1" || got[2].UA != "c/1" {
		t.Fatalf("devices %q, %q after the newest, want d/1 then c/1 (newest first)", got[1].UA, got[2].UA)
	}
	if n := utf8.RuneCountInString(got[0].UA); n != ConnDeviceUARunes || !strings.HasPrefix(long, got[0].UA) {
		t.Fatalf("newest client string is %d characters, want the first %d of it", n, ConnDeviceUARunes)
	}
}

// The declared device id is a keyed per-account digest, and the view shows
// its first DeviceIDShownLen characters — the same four the sub-log list
// shows admins — enough to tell one account's devices apart, not a value
// worth copying. No field of an inferred device holds more of it.
func TestInferConnectionDevices_ShowsOnlyFourOfTheDigest(t *testing.T) {
	const digest = "beefcafe12345678"
	since := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	conns := []LiveConnection{{UserID: 7, PanelID: 1, SourceKey: "203.0.113.7", IP: "203.0.113.7"}}
	fetches := []SubLog{fetchAt(7, "203.0.113.7", "Happ/3.13", "mihomo", digest, since)}

	got := InferConnectionDevices(conns, fetches, since)[UserSource{UserID: 7, SourceKey: "203.0.113.7"}]

	if DeviceIDShownLen != 4 {
		t.Fatalf("DeviceIDShownLen = %d, want 4", DeviceIDShownLen)
	}
	if len(got) != 1 || got[0].DeviceID4 != digest[:DeviceIDShownLen] {
		t.Fatalf("devices %+v, want one showing %q", got, digest[:DeviceIDShownLen])
	}
	v := reflect.ValueOf(got[0])
	for i := range v.NumField() {
		if s, ok := v.Field(i).Interface().(string); ok && strings.Contains(s, digest[:DeviceIDShownLen+1]) {
			t.Fatalf("ConnDevice.%s = %q holds more of the digest than %d characters", v.Type().Field(i).Name, s, DeviceIDShownLen)
		}
	}
}

// ---- One account's devices (the risk center drawer's 设备 tab) ----
//
// UserDevices answers "what does this account fetch with, and from where",
// over the account's whole fetch window rather than one connection's
// source. The identity rule is the one above (SubLogIdentity), so a device
// in the drawer is the same device the risk worker counts.

// Fetches are grouped by the declared device id, else by the exact client
// string: two formats asked for by one client string are one device, and
// one declared id across client versions is one device.
func TestUserDevices_GroupsByDeclaredIDElseClientString(t *testing.T) {
	since := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	fetches := []SubLog{
		fetchAt(7, "203.0.113.7", "clash.meta/1.19", "mihomo", "", since.Add(time.Minute)),
		fetchAt(7, "203.0.113.7", "clash.meta/1.19", "sing-box", "", since.Add(2*time.Minute)),
		fetchAt(7, "198.51.100.1", "Happ/3.12", "mihomo", "beefcafe12345678", since.Add(3*time.Minute)),
		fetchAt(7, "198.51.100.1", "Happ/3.13", "mihomo", "beefcafe12345678", since.Add(4*time.Minute)),
	}

	got := UserDevices(fetches, since)

	if len(got) != 2 {
		t.Fatalf("%d devices (%+v), want 2: one per declared id, one per client string", len(got), got)
	}
	happ, clash := got[0], got[1]
	if happ.DeviceID4 != "beef" || happ.UA != "Happ/3.13" || happ.Fetches != 2 {
		t.Fatalf("declared device = %+v, want id beef, the newest client string, 2 fetches", happ)
	}
	if clash.DeviceID4 != "" || clash.UA != "clash.meta/1.19" || clash.ClientType != "sing-box" || clash.Fetches != 2 {
		t.Fatalf("client-string device = %+v, want no id, the newest format (sing-box), 2 fetches", clash)
	}
	if clash.FirstAtMS != since.Add(time.Minute).UnixMilli() || clash.LastAtMS != since.Add(2*time.Minute).UnixMilli() {
		t.Fatalf("first/last = %d/%d, want the oldest and the newest fetch", clash.FirstAtMS, clash.LastAtMS)
	}
}

// The newest label, format and client string win, by fetch time and, within
// one millisecond, by the fetch id (insert order); a later fetch that sent
// no label does not erase an earlier one. The client string is cut to
// ConnDeviceUARunes characters, as the live view cuts it.
func TestUserDevices_NewestValuesWinTieByID(t *testing.T) {
	since := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	at := since.Add(time.Minute)
	withID := func(l SubLog, id int64, label string) SubLog { l.ID, l.DeviceLabel = id, label; return l }
	long := strings.Repeat("长", ConnDeviceUARunes+10)
	fetches := []SubLog{
		withID(fetchAt(7, "203.0.113.7", "Happ/3.12", "mihomo", "beefcafe12345678", at), 12, "iOS 17.5 · iPhone15,2"),
		withID(fetchAt(7, "203.0.113.7", "Happ/3.11", "sing-box", "beefcafe12345678", at), 11, "iOS 17.4 · iPhone15,2"),
		withID(fetchAt(7, "203.0.113.7", long, "uri-list", "beefcafe12345678", at.Add(-time.Second)), 30, "iOS 17.3"),
		withID(fetchAt(7, "203.0.113.7", "Happ/3.12", "mihomo", "beefcafe12345678", at), 10, ""),
	}

	got := UserDevices(fetches, since)

	if len(got) != 1 {
		t.Fatalf("devices = %+v, want one", got)
	}
	d := got[0]
	if d.Label != "iOS 17.5 · iPhone15,2" || d.ClientType != "mihomo" || d.UA != "Happ/3.12" || d.Fetches != 4 {
		t.Fatalf("device = %+v, want the values of fetch 12 (newest, highest id in the millisecond)", d)
	}

	only := UserDevices(fetches[2:3], since)
	if n := utf8.RuneCountInString(only[0].UA); n != ConnDeviceUARunes || !strings.HasPrefix(long, only[0].UA) {
		t.Fatalf("client string is %d characters, want the first %d", n, ConnDeviceUARunes)
	}
}

// A device's sources are the distinct SOURCES it fetched from — an IPv6
// privacy address rotates inside its /64, so two members of one /64 are one
// source — newest first, at most UserDeviceSourcesMax of them, with the rest
// counted in SourcesMore. A fetch with no address counts as a fetch and adds
// no source.
func TestUserDevices_SourcesDedupedByPrefixAndCapped(t *testing.T) {
	since := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	fetches := []SubLog{
		fetchAt(7, "2001:db8:1:2::5", "v2rayN/7.0", "uri-list", "", since.Add(time.Minute)),
		fetchAt(7, "2001:db8:1:2::99", "v2rayN/7.0", "uri-list", "", since.Add(2*time.Minute)),
		fetchAt(7, "", "v2rayN/7.0", "uri-list", "", since.Add(3*time.Minute)),
	}
	for i := range UserDeviceSourcesMax + 2 {
		fetches = append(fetches, fetchAt(7, "198.51.100."+strconv.Itoa(i+1), "v2rayN/7.0", "uri-list", "",
			since.Add(time.Duration(10+i)*time.Minute)))
	}

	got := UserDevices(fetches, since)

	if len(got) != 1 {
		t.Fatalf("devices = %+v, want one", got)
	}
	d := got[0]
	if d.Fetches != len(fetches) {
		t.Fatalf("fetches = %d, want %d (the address-less one included)", d.Fetches, len(fetches))
	}
	if len(d.Sources) != UserDeviceSourcesMax || d.SourcesMore != 3 {
		t.Fatalf("sources %v (+%d), want %d listed and 3 more (the /64 once, %d addresses)",
			d.Sources, d.SourcesMore, UserDeviceSourcesMax, UserDeviceSourcesMax+2)
	}
	if want := "198.51.100." + strconv.Itoa(UserDeviceSourcesMax+2); d.Sources[0] != want {
		t.Fatalf("first source = %q, want the newest, %q", d.Sources[0], want)
	}
	for _, s := range d.Sources {
		if s == "" {
			t.Fatalf("sources %v hold an empty source", d.Sources)
		}
	}

	few := UserDevices(fetches[:3], since)
	if !slices.Equal(few[0].Sources, []string{"2001:db8:1:2::/64"}) || few[0].SourcesMore != 0 {
		t.Fatalf("sources = %v (+%d), want the one /64", few[0].Sources, few[0].SourcesMore)
	}
}

// Fetches before since say nothing about the window and are ignored — a
// device seen only before it is not listed. The list is newest first, and
// devices last seen in the same millisecond are ordered by their identity so
// the order is stable. No fetches is no devices, not nil.
func TestUserDevices_WindowAndOrder(t *testing.T) {
	since := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	fetches := []SubLog{
		fetchAt(7, "203.0.113.7", "old/1.0", "mihomo", "", since.Add(-time.Second)),
		fetchAt(7, "203.0.113.7", "b/1.0", "mihomo", "", since.Add(time.Minute)),
		fetchAt(7, "203.0.113.7", "a/1.0", "mihomo", "", since.Add(time.Minute)),
		fetchAt(7, "203.0.113.7", "c/1.0", "mihomo", "", since.Add(2*time.Minute)),
		fetchAt(7, "203.0.113.7", "b/1.0", "mihomo", "", since),
	}

	got := UserDevices(fetches, since)

	var uas []string
	for _, d := range got {
		uas = append(uas, d.UA)
	}
	if !slices.Equal(uas, []string{"c/1.0", "a/1.0", "b/1.0"}) {
		t.Fatalf("devices %v, want [c/1.0 a/1.0 b/1.0]: newest first, a tie by identity, old/1.0 outside the window", uas)
	}
	if got[2].Fetches != 2 || got[2].FirstAtMS != since.UnixMilli() {
		t.Fatalf("b/1.0 = %+v, want 2 fetches from since (inclusive)", got[2])
	}
	if none := UserDevices(nil, since); none == nil || len(none) != 0 {
		t.Fatalf("no fetches = %#v, want an empty, non-nil list", none)
	}
}
