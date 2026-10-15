package app

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

// Pressure fixtures reconcile rejected row counts with connection events only
// because every actual destination key is unique. HTTP Host alone is insufficient:
// routeOnly sniffing retains the VLESS destination in the core's access log.
func nativePressureBlockRequest(uuid, destination string, port int) ([]byte, error) {
	if destination == "" || net.ParseIP(destination) != nil {
		return nil, fmt.Errorf("pressure block destination must be a unique domain")
	}
	return nativeVLESSRequest(uuid, destination, port, destination)
}

func nativeUnitAuditRows(observation *protocol.AuditObservation) bool {
	if observation == nil {
		return true
	}
	for _, row := range observation.Hits {
		if row.Count != 1 {
			return false
		}
	}
	for _, row := range observation.Usage {
		if row.Count != 1 {
			return false
		}
	}
	return true
}

func TestNativePressureBlockUsesDistinctVLESSDestinationKeys(t *testing.T) {
	const uuid = "33333333-3333-4333-8333-333333333333"
	var prior []byte
	for _, destination := range []string{"pressure-000001.test", "pressure-000002.test"} {
		packet, err := nativePressureBlockRequest(uuid, destination, 12345)
		if err != nil || len(packet) < 23+len(destination) || packet[21] != 2 || int(packet[22]) != len(destination) || string(packet[23:23+len(destination)]) != destination {
			t.Fatal("pressure key exists only in HTTP Host instead of the actual VLESS target")
		}
		if !bytes.Contains(packet[23+len(destination):], []byte("Host: "+destination+"\r\n")) || bytes.Equal(packet, prior) {
			t.Fatal("pressure packet lost its independent target and HTTP host")
		}
		prior = packet
	}
	for _, destination := range []string{"", "192.0.2.1", "2001:db8::1"} {
		if _, err := nativePressureBlockRequest(uuid, destination, 12345); err == nil {
			t.Fatal("pressure accepted an aggregate IP key in place of a unique domain")
		}
	}
}

func TestNativePressureAccountingRejectsAggregatedSerializedRows(t *testing.T) {
	for _, observation := range []*protocol.AuditObservation{nil, {Dropped: 7}, {Hits: []protocol.AuditHit{{Count: 1}, {Count: 1}}}, {Usage: []protocol.AuditUsage{{Count: 1}}}} {
		if !nativeUnitAuditRows(observation) {
			t.Fatal("unit-row pressure observation was rejected")
		}
	}
	for _, observation := range []*protocol.AuditObservation{{Hits: []protocol.AuditHit{{Count: 0}}}, {Hits: []protocol.AuditHit{{Count: 1}, {Count: 2}}}, {Usage: []protocol.AuditUsage{{Count: 1200}}}} {
		if nativeUnitAuditRows(observation) {
			t.Fatal("pressure would add rejected rows and connection events with different units")
		}
	}
}

func nativeVLESSRequest(uuid, address string, port int, host string) ([]byte, error) {
	id, err := hex.DecodeString(strings.ReplaceAll(uuid, "-", ""))
	if err != nil || len(id) != 16 || port < 1 || port > 65535 || address == "" || strings.ContainsAny(host, "\r\n") {
		return nil, fmt.Errorf("invalid private VLESS probe input")
	}
	header := append([]byte{0}, id...)
	header = append(header, 0, 1, byte(port>>8), byte(port))
	ip := net.ParseIP(address)
	switch {
	case ip.To4() != nil:
		header = append(header, 1)
		header = append(header, ip.To4()...)
	case ip.To16() != nil:
		header = append(header, 3)
		header = append(header, ip.To16()...)
	default:
		if len(address) > 255 || strings.ContainsAny(address, "\r\n \t") {
			return nil, fmt.Errorf("invalid private VLESS probe domain")
		}
		header = append(header, 2, byte(len(address)))
		header = append(header, []byte(address)...)
	}
	return append(header, []byte("GET / HTTP/1.1\r\nHost: "+host+"\r\nConnection: close\r\n\r\n")...), nil
}

func TestNativeVLESSRequestPreservesLiteralAndDomainDestinations(t *testing.T) {
	const uuid = "33333333-3333-4333-8333-333333333333"
	id, _ := hex.DecodeString(strings.ReplaceAll(uuid, "-", ""))
	for _, tc := range []struct {
		address string
		kind    byte
		wire    []byte
	}{
		{"2001:db8::1", 3, net.ParseIP("2001:db8::1").To16()},
		{"127.0.0.1", 1, []byte{127, 0, 0, 1}},
		{"localhost", 2, append([]byte{9}, []byte("localhost")...)},
	} {
		request, err := nativeVLESSRequest(uuid, tc.address, 12345, "fixture.example.test")
		want := append([]byte{0}, id...)
		want = append(want, 0, 1, 0x30, 0x39, tc.kind)
		want = append(want, tc.wire...)
		want = append(want, []byte("GET / HTTP/1.1\r\nHost: fixture.example.test\r\nConnection: close\r\n\r\n")...)
		if err != nil || !bytes.Equal(request, want) {
			t.Fatal("VLESS probe changed its destination type or packet")
		}
	}
	for _, tc := range []struct {
		id, address, host string
		port              int
	}{
		{"invalid", "127.0.0.1", "localhost", 80},
		{uuid, "", "localhost", 80},
		{uuid, strings.Repeat("x", 256), "localhost", 80},
		{uuid, "localhost", "bad\r\nheader", 80},
		{uuid, "localhost", "localhost", 0},
		{uuid, "localhost", "localhost", 65536},
	} {
		if _, err := nativeVLESSRequest(tc.id, tc.address, tc.port, tc.host); err == nil {
			t.Fatal("VLESS probe accepted an invalid destination or request")
		}
	}
}
