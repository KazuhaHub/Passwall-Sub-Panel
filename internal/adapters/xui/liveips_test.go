package xui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ListLiveClientIPs feeds the only per-USER connection figure PSP has, and
// that figure will be used to judge whether somebody is sharing an account.
// Both directions of error are expensive, so each case here names which one
// it guards.

func liveIPServer(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/panel/api/clients/clientIpsByGuid" {
			t.Errorf("unexpected path %q — the by-guid endpoint is what makes this one call per panel", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{baseURL: srv.URL, http: srv.Client(), apiToken: "t"}
}

// Upstream keys by node guid because one 3X-UI can front several nodes. PSP
// does not model that layer and must not: the same address seen on two of a
// panel's nodes is one person on one connection path, and counting it twice
// would inflate exactly the number this exists to make trustworthy.
func TestListLiveClientIPs_DeduplicatesAcrossNodeGuids(t *testing.T) {
	c := liveIPServer(t, `{"success":true,"obj":{
		"guid-a":{"u7@x":[{"ip":"1.1.1.1","timestamp":1},{"ip":"2.2.2.2","timestamp":1}]},
		"guid-b":{"u7@x":[{"ip":"1.1.1.1","timestamp":2}]}
	}}`)
	got, err := c.ListLiveClientIPs(context.Background())
	if err != nil {
		t.Fatalf("ListLiveClientIPs: %v", err)
	}
	want := []string{"1.1.1.1", "2.2.2.2"}
	if !reflect.DeepEqual(got["u7@x"], want) {
		t.Fatalf("ips = %v, want %v — an address on two nodes of one panel is one address", got["u7@x"], want)
	}
}

// Several emails on one panel are the credential partitions of (possibly) one
// user. The adapter keeps them separate; folding them is the caller's job,
// because only PSP knows which user each belongs to.
func TestListLiveClientIPs_KeepsEmailsSeparate(t *testing.T) {
	c := liveIPServer(t, `{"success":true,"obj":{
		"guid-a":{"u7@x":[{"ip":"1.1.1.1"}],"u7-c1@x":[{"ip":"3.3.3.3"}]}
	}}`)
	got, _ := c.ListLiveClientIPs(context.Background())
	if len(got) != 2 || got["u7@x"][0] != "1.1.1.1" || got["u7-c1@x"][0] != "3.3.3.3" {
		t.Fatalf("got %v, want the two emails kept apart", got)
	}
}

// Sorted, so a caller comparing two snapshots — or a test asserting on the
// value — is not reading Go's randomized map order.
func TestListLiveClientIPs_SortsAddresses(t *testing.T) {
	c := liveIPServer(t, `{"success":true,"obj":{
		"g":{"u7@x":[{"ip":"9.9.9.9"},{"ip":"1.1.1.1"},{"ip":"5.5.5.5"}]}
	}}`)
	got, _ := c.ListLiveClientIPs(context.Background())
	want := []string{"1.1.1.1", "5.5.5.5", "9.9.9.9"}
	if !reflect.DeepEqual(got["u7@x"], want) {
		t.Fatalf("ips = %v, want %v", got["u7@x"], want)
	}
}

// Blank keys and blank addresses are dropped rather than becoming a phantom
// client or a phantom connection. Either would land in somebody's count.
func TestListLiveClientIPs_DropsBlankEmailsAndAddresses(t *testing.T) {
	c := liveIPServer(t, `{"success":true,"obj":{
		"g":{"":[{"ip":"1.1.1.1"}],"u7@x":[{"ip":""},{"ip":"  "},{"ip":"2.2.2.2"}]}
	}}`)
	got, _ := c.ListLiveClientIPs(context.Background())
	if _, ok := got[""]; ok {
		t.Fatal("a blank email must not become a client")
	}
	if !reflect.DeepEqual(got["u7@x"], []string{"2.2.2.2"}) {
		t.Fatalf("ips = %v, want only the real address", got["u7@x"])
	}
}

// An empty answer is a real state — nobody is connected — and must not be an
// error. Erroring here would mark every user on a quiet panel as unread, and
// unread is meant to mean "we could not look".
func TestListLiveClientIPs_EmptyIsNotAnError(t *testing.T) {
	c := liveIPServer(t, `{"success":true,"obj":{}}`)
	got, err := c.ListLiveClientIPs(context.Background())
	if err != nil {
		t.Fatalf("an idle panel must not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

// A panel that fails the call must surface an error so its users are counted
// as unread. Returning an empty map would read as "nobody is connected",
// which is the failure this whole area keeps producing.
func TestListLiveClientIPs_FailureIsAnErrorNotAnEmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: srv.Client(), apiToken: "t"}
	if _, err := c.ListLiveClientIPs(context.Background()); err == nil {
		t.Fatal("a failed read must not be indistinguishable from an idle panel")
	}
}

// ---- ListLiveClientIPDetails ----------------------------------------------
//
// The detail read keeps what the plain read throws away: which node saw the
// address and when it last did. Without the timestamp, 30 minutes of
// upstream memory reads as "connected right now", and one commuter reads as
// several cities at once.

func details(t *testing.T, body string) map[string][]domain.LiveIPSighting {
	t.Helper()
	got, err := liveIPServer(t, body).ListLiveClientIPDetails(context.Background())
	if err != nil {
		t.Fatalf("ListLiveClientIPDetails: %v", err)
	}
	return got
}

func TestListLiveClientIPDetails_KeepsNodeAndTimestamp(t *testing.T) {
	got := details(t, `{"success":true,"obj":{
		"guid-b":{"u7@x":[{"ip":"2.2.2.2","timestamp":1727000100}]},
		"guid-a":{"u7@x":[{"ip":" 1.1.1.1 ","timestamp":1727000000}],"u8@x":[{"ip":"3.3.3.3","timestamp":1727000050}]}
	}}`)
	want := map[string][]domain.LiveIPSighting{
		"u7@x": {{IP: "1.1.1.1", Node: "guid-a", SeenAt: 1727000000}, {IP: "2.2.2.2", Node: "guid-b", SeenAt: 1727000100}},
		"u8@x": {{IP: "3.3.3.3", Node: "guid-a", SeenAt: 1727000050}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sightings = %+v, want %+v", got, want)
	}
}

// No timestamp is "unknown", which the freshness rule reads as live — the
// behaviour before timestamps were read at all. Never an error.
func TestListLiveClientIPDetails_MissingTimestampIsZero(t *testing.T) {
	got := details(t, `{"success":true,"obj":{"g":{"u7@x":[{"ip":"1.1.1.1"}]}}}`)
	want := []domain.LiveIPSighting{{IP: "1.1.1.1", Node: "g", SeenAt: 0}}
	if !reflect.DeepEqual(got["u7@x"], want) {
		t.Fatalf("sightings = %+v, want %+v", got["u7@x"], want)
	}
}

// Every supported 3X-UI serves seconds. A millisecond value (a fork, a
// future change) would otherwise sit 1000x in the future, become its node's
// reference, and make every real address on that node read as stale.
func TestListLiveClientIPDetails_MillisecondsNormalised(t *testing.T) {
	got := details(t, `{"success":true,"obj":{"g":{"u7@x":[{"ip":"1.1.1.1","timestamp":1727000000123}]}}}`)
	if s := got["u7@x"]; len(s) != 1 || s[0].SeenAt != 1727000000 {
		t.Fatalf("sightings = %+v, want SeenAt 1727000000", s)
	}
}

// One odd field must not cost the whole panel its read: an error here marks
// every user on the panel unread. A timestamp that cannot be read is
// "unknown", and a number that arrives as a float or a quoted string is
// still read.
func TestListLiveClientIPDetails_MalformedTimestampIsUnknownNotAnError(t *testing.T) {
	got := details(t, `{"success":true,"obj":{"g":{
		"a@x":[{"ip":"1.1.1.1","timestamp":"soon"}],
		"b@x":[{"ip":"1.1.1.2","timestamp":{}}],
		"c@x":[{"ip":"1.1.1.3","timestamp":true}],
		"d@x":[{"ip":"1.1.1.4","timestamp":null}],
		"e@x":[{"ip":"1.1.1.5","timestamp":-5}],
		"f@x":[{"ip":"1.1.1.6","timestamp":"1727000000"}],
		"g@x":[{"ip":"1.1.1.7","timestamp":1727000000.9}],
		"h@x":[{"ip":"1.1.1.8","timestamp":1e400}]
	}}}`)
	want := map[string]int64{
		"a@x": 0, "b@x": 0, "c@x": 0, "d@x": 0, "e@x": 0,
		"f@x": 1727000000, "g@x": 1727000000, "h@x": 0,
	}
	for email, at := range want {
		if s := got[email]; len(s) != 1 || s[0].SeenAt != at {
			t.Errorf("%s: sightings = %+v, want SeenAt %d", email, s, at)
		}
	}
}

// One 3X-UI can front several nodes, and each scans on its own clock, so
// the same address on two nodes is two sightings here. (The plain read
// still folds them into one address; see the test above.) The same address
// twice on ONE node is one sighting at its newest time.
func TestListLiveClientIPDetails_SameIPOnTwoNodesKeepsBoth(t *testing.T) {
	got := details(t, `{"success":true,"obj":{
		"guid-b":{"u7@x":[{"ip":"1.1.1.1","timestamp":20}]},
		"guid-a":{"u7@x":[{"ip":"1.1.1.1","timestamp":10},{"ip":"1.1.1.1","timestamp":30},{"ip":"","timestamp":99}],"":[{"ip":"9.9.9.9"}]}
	}}`)
	want := map[string][]domain.LiveIPSighting{"u7@x": {
		{IP: "1.1.1.1", Node: "guid-a", SeenAt: 30},
		{IP: "1.1.1.1", Node: "guid-b", SeenAt: 20},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sightings = %+v, want %+v", got, want)
	}
}

// nil means "plain reader, nothing to judge freshness by" one layer up. An
// idle detail reader must say "nobody", not "no clock".
func TestListLiveClientIPDetails_EmptyIsAnEmptyMapNotNil(t *testing.T) {
	got := details(t, `{"success":true,"obj":{}}`)
	if got == nil || len(got) != 0 {
		t.Fatalf("sightings = %#v, want a non-nil empty map", got)
	}
}

// A failed read is an error, never "nobody is connected".
func TestListLiveClientIPDetails_FailureIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: srv.Client(), apiToken: "t"}
	if _, err := c.ListLiveClientIPDetails(context.Background()); err == nil {
		t.Fatal("a failed read must not be indistinguishable from an idle panel")
	}
}
