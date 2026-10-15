package nodesync

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/boundedcache"
)

func rosterCacheService() *Service {
	return &Service{issuedRosterCache: boundedcache.New[*issuedRoster](issuedRosterCacheEntries, issuedRosterCacheBytes)}
}

func rosterCacheBody(t *testing.T, size int, owner string) []byte {
	t.Helper()
	roster := protocol.RosterBody{MinConfigVersion: protocol.Version{Epoch: 7, Version: 19}}
	for i := range size {
		roster.Clients = append(roster.Clients, protocol.Client{
			Key: protocol.NewClientKey(int64(i + 1)), Subject: protocol.NewSubjectKey(3),
			Enabled: true, ExpiresAtMS: 123456789, Listeners: []protocol.ListenerKey{protocol.NewListenerKey(9)},
			Credentials: protocol.Credential{UUID: owner},
		})
	}
	body, err := json.Marshal(roster)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestIssuedRosterCacheUsesCurrentBytesAndPreservesDecodeSemantics(t *testing.T) {
	s := rosterCacheService()
	body := rosterCacheBody(t, 200, "original")
	first, err := s.decodeIssuedRoster(body)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.decodeIssuedRoster(append([]byte(nil), body...))
	if err != nil || again != first {
		t.Fatal("identical bytes were decoded again")
	}
	var expected protocol.RosterBody
	if err := json.Unmarshal(body, &expected); err != nil {
		t.Fatal(err)
	}
	if first.minConfigVersion != expected.MinConfigVersion || len(first.clients) != 200 {
		t.Fatal("roster metadata or membership lost")
	}
	for _, client := range expected.Clients {
		if !reflect.DeepEqual(first.clients[client.Key], client) {
			t.Fatal("parsed client changed")
		}
	}
	// Same version, different actual bytes must invalidate the compiled syntax.
	changed := rosterCacheBody(t, 200, "modified")
	next, err := s.decodeIssuedRoster(changed)
	if err != nil || next == first || next.clients[protocol.NewClientKey(1)].Credentials.UUID != "modified" {
		t.Fatal("changed body reused old credentials")
	}
	for range 2 {
		if _, err := s.decodeIssuedRoster([]byte("{")); err == nil {
			t.Fatal("malformed bytes became a cached success")
		}
	}
	// Parsing owns strings and slices rather than retaining input storage.
	for i := range body {
		body[i] = 'x'
	}
	if first.clients[protocol.NewClientKey(1)].Credentials.UUID != "original" {
		t.Fatal("caller bytes mutated parsed syntax")
	}
	duplicate := []byte(`{"clients":[{"key":"client:1","enabled":true},{"key":"client:1","enabled":false}]}`)
	var decoded protocol.RosterBody
	if err := json.Unmarshal(duplicate, &decoded); err != nil {
		t.Fatal(err)
	}
	last, err := s.decodeIssuedRoster(duplicate)
	if err != nil || len(last.clients) != 1 || last.clients[decoded.Clients[1].Key].Enabled {
		t.Fatal("duplicate-key behavior changed")
	}
	if _, err := s.decodeIssuedRoster([]byte("null")); err != nil {
		t.Fatal("existing null decode semantics changed")
	}
}

func TestIssuedRosterCacheBoundsAndConcurrentIndependentOwners(t *testing.T) {
	s := rosterCacheService()
	bodies := make([][]byte, 65)
	warm := make([]*issuedRoster, len(bodies))
	for i := range bodies {
		bodies[i] = rosterCacheBody(t, 200, fmt.Sprintf("owner-%d", i))
		var err error
		warm[i], err = s.decodeIssuedRoster(bodies[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := range bodies {
		wg.Go(func() {
			for range 3 {
				got, err := s.decodeIssuedRoster(bodies[i])
				if err != nil || got != warm[i] {
					t.Error("independent owner lost warm parsed roster")
				}
			}
		})
	}
	wg.Wait()
	entries := &Service{issuedRosterCache: boundedcache.New[*issuedRoster](2, issuedRosterCacheBytes)}
	first, err := entries.decodeIssuedRoster(bodies[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range bodies[1:3] {
		if _, err := entries.decodeIssuedRoster(body); err != nil {
			t.Fatal(err)
		}
	}
	if next, err := entries.decodeIssuedRoster(bodies[0]); err != nil || next == first {
		t.Fatal("entry limit did not evict")
	}
	// A tiny weight budget evicts regardless of its entry allowance.
	small := &Service{issuedRosterCache: boundedcache.New[*issuedRoster](256, 1024)}
	a, err := small.decodeIssuedRoster([]byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := small.decodeIssuedRoster([]byte(`{"clients":[]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := small.decodeIssuedRoster([]byte(`{"clients":null}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := small.decodeIssuedRoster([]byte(`{"coverage":{}}`)); err != nil {
		t.Fatal(err)
	}
	b, err := small.decodeIssuedRoster([]byte("{}"))
	if err != nil || a == b {
		t.Fatal("byte budget did not evict")
	}
	// Unknown JSON fields keep existing unmarshal behavior but cannot defeat
	// the retention budget. Repeated oversized values are never retained.
	oversized := []byte(`{"ignored":"` + strings.Repeat("x", issuedRosterCacheBytes/2) + `"}`)
	a, err = s.decodeIssuedRoster(oversized)
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.decodeIssuedRoster(oversized)
	if err != nil || a == b {
		t.Fatal("oversized syntax was retained")
	}
	uncached := &Service{}
	a, err = uncached.decodeIssuedRoster(bodies[0])
	if err != nil {
		t.Fatal(err)
	}
	b, err = uncached.decodeIssuedRoster(bodies[0])
	if err != nil || a == b {
		t.Fatal("nil cache path changed")
	}
}

func TestIssuedRosterCacheWarmAllocationsDoNotScaleWithClientCount(t *testing.T) {
	measure := func(size int) float64 {
		s := rosterCacheService()
		body := rosterCacheBody(t, size, "allocation")
		if _, err := s.decodeIssuedRoster(body); err != nil {
			t.Fatal(err)
		}
		return testing.AllocsPerRun(5, func() {
			got, err := s.decodeIssuedRoster(body)
			if err != nil || len(got.clients) != size {
				t.Fatal("warm roster changed")
			}
		})
	}
	if small, large := measure(1), measure(2000); large > small+2 {
		t.Fatalf("warm allocations scale with clients: %.0f / %.0f", small, large)
	}
}
