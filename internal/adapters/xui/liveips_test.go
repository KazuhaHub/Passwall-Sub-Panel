package xui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
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
