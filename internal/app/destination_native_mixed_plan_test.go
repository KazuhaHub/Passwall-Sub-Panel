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
	if decoder.Decode(&out) != nil || decoder.Decode(new(any)) != io.EOF || out.Schema != 1 || out.HeapAlloc == 0 || out.HeapInuse < out.HeapAlloc || out.HeapObjects == 0 || out.Goroutines < 1 || len(out.Lanes) != 4 {
		return out, true, false
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
	reading := nativeMixedNodeReading{Schema: 1, HeapAlloc: 100, HeapInuse: 200, HeapObjects: 10, Goroutines: 5}
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
	} {
		copy := reading
		copy.Lanes = append([]nativeMixedNodeLane(nil), reading.Lanes...)
		mutate(&copy)
		if _, found, ok := nativeReadMixedNodeLine(encode(copy)); !found || ok {
			t.Fatal("invalid numeric bounds accepted")
		}
	}
	data := encode(reading)
	data = append(data[:len(data)-1], []byte(`,"private_destination":"secret"}`)...)
	if _, _, ok := nativeReadMixedNodeLine(data); ok {
		t.Fatal("private journal extension accepted")
	}
	if _, found, ok := nativeReadMixedNodeLine([]byte("a private unrelated line")); found || !ok {
		t.Fatal("foreign journal was parsed")
	}
}
