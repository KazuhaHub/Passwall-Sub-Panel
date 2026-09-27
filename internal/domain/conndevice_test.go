package domain

import (
	"reflect"
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
