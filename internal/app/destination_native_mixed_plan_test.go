package app

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"testing"
)

var nativeMixedKinds = [4]string{"block", "observe", "trial", "usage"}
var nativeMixedKeyLimits = [4]int{20000, 10000, 10000, 50000}
var nativeMixedJournalPrefix = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\.\d{6} \[Info\] passwall-node: audit_pressure=`)

type nativeMixedNodeLane struct {
	Kind         string `json:"kind"`
	Keys         int    `json:"keys"`
	KeyLimit     int    `json:"key_limit"`
	Hours        int    `json:"hours"`
	Pending      int    `json:"pending"`
	PendingRows  int    `json:"pending_rows"`
	PendingBytes int    `json:"pending_bytes"`
	Dropped      uint64 `json:"retained_dropped_events"`
	Unmatched    uint64 `json:"retained_unmatched_events"`
}

type nativeMixedNodeReading struct {
	Schema      int                   `json:"schema_version"`
	HeapAlloc   uint64                `json:"go_heap_alloc_bytes"`
	HeapInuse   uint64                `json:"go_heap_inuse_bytes"`
	HeapObjects uint64                `json:"go_heap_objects"`
	Goroutines  int                   `json:"go_goroutines"`
	Lanes       []nativeMixedNodeLane `json:"lanes"`
	Access      nativeMixedAccess     `json:"access_log"`
}

type nativeMixedAccess struct {
	Available        int    `json:"available"`
	Private          uint64 `json:"private_lines"`
	Usage            uint64 `json:"usage_events"`
	Denied           uint64 `json:"denied_events"`
	Unmatched        uint64 `json:"unmatched_events"`
	Dropped          uint64 `json:"dropped_events"`
	Stale            uint64 `json:"stale_events"`
	Queued           int    `json:"parse_queued"`
	Limit            int    `json:"parse_limit"`
	PendingDropped   uint64 `json:"pending_dropped"`
	PendingUnmatched uint64 `json:"pending_unmatched"`
}

func (v nativeMixedAccess) valid() bool {
	if v.Available != 1 || v.Limit != 4096 || v.Queued < 0 || v.Queued > v.Limit {
		return false
	}
	for _, n := range []uint64{v.Private, v.Usage, v.Denied, v.Unmatched, v.Dropped, v.Stale, v.PendingDropped, v.PendingUnmatched} {
		if n > 1<<53-1 {
			return false
		}
	}
	return true
}

func (v nativeMixedAccess) quiescent() bool {
	return v.valid() && v.Queued == 0 && v.PendingDropped == 0 && v.PendingUnmatched == 0 && v.Private == v.Usage+v.Denied+v.Unmatched+v.Dropped+v.Stale
}

// The fixture owns connection counts. A difference before this filter is not
// a fabricated Node loss, nor a measurement inside the upstream core's queue.
type nativeMixedIngress struct {
	Before             nativeMixedAccess `json:"before"`
	After              nativeMixedAccess `json:"after"`
	AllowedExpected    uint64            `json:"allowed_expected"`
	AllowedReceived    uint64            `json:"allowed_received"`
	AllowedNotReceived uint64            `json:"allowed_not_received"`
	DeniedExpected     uint64            `json:"denied_expected"`
	DeniedReceived     uint64            `json:"denied_received"`
	DeniedNotReceived  uint64            `json:"denied_not_received"`
}

func nativeMixedIngressBetween(before, after nativeMixedAccess, allowed, denied uint64) (nativeMixedIngress, bool) {
	out := nativeMixedIngress{Before: before, After: after, AllowedExpected: allowed, DeniedExpected: denied}
	if !before.quiescent() || !after.quiescent() || after.Private < before.Private || after.Usage < before.Usage || after.Denied < before.Denied || after.Unmatched < before.Unmatched || after.Dropped < before.Dropped || after.Stale != before.Stale {
		return out, false
	}
	out.AllowedReceived = after.Usage - before.Usage + after.Unmatched - before.Unmatched + after.Dropped - before.Dropped
	out.DeniedReceived = after.Denied - before.Denied
	if out.AllowedReceived > allowed || out.DeniedReceived > denied {
		return out, false
	}
	out.AllowedNotReceived = allowed - out.AllowedReceived
	out.DeniedNotReceived = denied - out.DeniedReceived
	return out, true
}

// Closed opt-in numeric journal only. Other private output stays private.
// Reject malformed observations rather than exporting arbitrary JSON fields.
func nativeReadMixedNodeLine(line []byte) (nativeMixedNodeReading, bool, bool) {
	var out nativeMixedNodeReading
	prefix := nativeMixedJournalPrefix.FindIndex(line)
	if prefix == nil {
		return out, false, true
	}
	if len(line) > 16*1024 {
		return out, true, false
	}
	decoder := json.NewDecoder(bytes.NewReader(line[prefix[1]:]))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(new(any)) != io.EOF || out.Schema != 2 || out.HeapAlloc == 0 || out.HeapInuse < out.HeapAlloc || out.HeapObjects == 0 || out.Goroutines < 1 || len(out.Lanes) != 4 || !out.Access.valid() {
		return out, true, false
	}
	var shape struct {
		Access map[string]json.RawMessage `json:"access_log"`
	}
	if json.Unmarshal(line[prefix[1]:], &shape) != nil || len(shape.Access) != 11 {
		return out, true, false
	}
	for _, raw := range shape.Access {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return out, true, false
		}
	}
	for i, lane := range out.Lanes {
		maxRows := 4096
		if i == 3 {
			maxRows = 8192
		}
		if lane.Kind != nativeMixedKinds[i] || lane.KeyLimit != nativeMixedKeyLimits[i] || lane.Keys < 0 || lane.Keys > lane.KeyLimit || lane.Hours < 0 || lane.Hours > 50 || lane.Pending < 0 || lane.Pending > 1 || lane.PendingRows < 0 || lane.PendingRows > maxRows || lane.PendingBytes < 0 || lane.PendingBytes > 1<<20 || lane.Pending == 0 && (lane.PendingRows != 0 || lane.PendingBytes != 0) || lane.Pending == 1 && lane.PendingBytes == 0 {
			return out, true, false
		}
	}
	return out, true, true
}

func TestNativeMixedObservationRejectsPrivateFieldsAndForeignQuotas(t *testing.T) {
	reading := nativeMixedNodeReading{Schema: 2, HeapAlloc: 100, HeapInuse: 200, HeapObjects: 10, Goroutines: 5, Access: nativeMixedAccess{Available: 1, Limit: 4096}}
	for i, kind := range nativeMixedKinds {
		reading.Lanes = append(reading.Lanes, nativeMixedNodeLane{Kind: kind, KeyLimit: nativeMixedKeyLimits[i]})
	}
	prefix := []byte("2026/10/12 12:00:00.000001 [Info] passwall-node: audit_pressure=")
	encode := func(value any) []byte {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal("cannot encode numeric fixture")
		}
		return append(append([]byte(nil), prefix...), data...)
	}
	if _, found, ok := nativeReadMixedNodeLine(encode(reading)); !found || !ok {
		t.Fatal("valid numeric observation rejected")
	}
	for _, mutate := range []func(*nativeMixedNodeReading){
		func(v *nativeMixedNodeReading) { v.HeapInuse = 1 },
		func(v *nativeMixedNodeReading) { v.Lanes[0].KeyLimit++ },
		func(v *nativeMixedNodeReading) { v.Lanes[1].Keys = 10001 },
		func(v *nativeMixedNodeReading) { v.Lanes[2].Pending = 1 },
		func(v *nativeMixedNodeReading) { v.Lanes[3].PendingBytes = 1 },
		func(v *nativeMixedNodeReading) { v.Lanes[0].PendingRows = 4097 },
		func(v *nativeMixedNodeReading) { v.Lanes[0].Hours = 51 },
		func(v *nativeMixedNodeReading) { v.Schema = 1 },
		func(v *nativeMixedNodeReading) { v.Access.Available = 0 },
		func(v *nativeMixedNodeReading) { v.Access.Limit = 8192 },
		func(v *nativeMixedNodeReading) { v.Access.Queued = 4097 },
		func(v *nativeMixedNodeReading) { v.Access.Private = 1 << 53 },
	} {
		copy := reading
		copy.Lanes = append([]nativeMixedNodeLane(nil), reading.Lanes...)
		mutate(&copy)
		if _, found, ok := nativeReadMixedNodeLine(encode(copy)); !found || ok {
			t.Fatal("invalid numeric bounds accepted")
		}
	}
	data := encode(reading)
	for _, invalid := range [][]byte{
		bytes.Replace(data, []byte(`"private_lines":0,`), nil, 1),
		bytes.Replace(data, []byte(`"private_lines":0`), []byte(`"private_lines":null`), 1),
	} {
		if _, _, ok := nativeReadMixedNodeLine(invalid); ok {
			t.Fatal("missing or null ingress counter accepted")
		}
	}
	data = append(data[:len(data)-1], []byte(`,"private_destination":"secret"}`)...)
	if _, _, ok := nativeReadMixedNodeLine(data); ok {
		t.Fatal("private journal extension accepted")
	}
	if _, found, ok := nativeReadMixedNodeLine([]byte("a private unrelated line")); found || !ok {
		t.Fatal("foreign journal was parsed")
	}
}

func TestNativeMixedIngressSeparatesCoreOmissionsFromPersistedUsage(t *testing.T) {
	before := nativeMixedAccess{Available: 1, Limit: 4096, Private: 1, Usage: 1}
	after := nativeMixedAccess{Available: 1, Limit: 4096, Private: 196045 + 70000, Usage: 39999 + 18193 + 1, Dropped: 137852, Denied: 70000}
	ingress, ok := nativeMixedIngressBetween(before, after, 225090, 72000)
	if !ok || ingress.AllowedReceived != 196044 || ingress.AllowedNotReceived != 29046 || ingress.DeniedNotReceived != 2000 {
		t.Fatal("before-filter omissions were confused with the persisted usage ledger")
	}
	for _, mutate := range []func(*nativeMixedAccess){
		func(v *nativeMixedAccess) { v.Private++ },
		func(v *nativeMixedAccess) { v.Queued = 1 },
		func(v *nativeMixedAccess) { v.PendingDropped = 1 },
		func(v *nativeMixedAccess) { v.PendingUnmatched = 1 },
		func(v *nativeMixedAccess) { v.Stale++; v.Private++ },
		func(v *nativeMixedAccess) { v.Denied += 2001; v.Private += 2001 },
		func(v *nativeMixedAccess) { v.Usage += 29047; v.Private += 29047 },
	} {
		invalid := after
		mutate(&invalid)
		if _, ok := nativeMixedIngressBetween(before, invalid, 225090, 72000); ok {
			t.Fatal("unreconciled or nonquiescent ingress accepted")
		}
	}
}
