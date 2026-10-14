package nodesync

import (
	"reflect"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

func TestFleetCounterReportsReadCurrentOwnedValuesWithoutPrivatePayloads(t *testing.T) {
	counter := nodeprotocol.ClientCounters{
		Key: nodeprotocol.NewClientKey(11), Present: false,
		UpBytes: 120, DownBytes: 30, CounterEpoch: 4,
		Gate: nodeprotocol.GateUnconfigured, LiveIPs: []string{"192.0.2.1"},
	}
	service := &Service{reports: map[string]receivedFullReport{
		"active": {report: nodeprotocol.NodeReport{
			Clients:     []nodeprotocol.ClientCounters{counter},
			Objects:     []nodeprotocol.ObjectStatus{{Stream: nodeprotocol.StreamRoster}},
			TaskResults: []nodeprotocol.TaskResult{{Result: []byte("owned private result")}},
		}, receivedAtMS: 1000},
		"retired": {report: nodeprotocol.NodeReport{Clients: []nodeprotocol.ClientCounters{counter}}},
	}}
	active := map[string]struct{}{"active": {}, "missing": {}}
	first := service.fleetCounterReports(active)
	want := fleetClientCounter{key: counter.Key, present: counter.Present, upBytes: counter.UpBytes, downBytes: counter.DownBytes, counterEpoch: counter.CounterEpoch}
	if len(first) != 1 || first["active"].receivedAtMS != 1000 || !reflect.DeepEqual(first["active"].clients, []fleetClientCounter{want}) {
		t.Fatal("fleet snapshot lost counters or retained inactive/private values")
	}
	// A snapshot owns its values. Mutating it cannot alter the authenticated
	// full report, including the actual closed-client flag and live IP data.
	first["active"].clients[0].upBytes = 999
	if !reflect.DeepEqual(service.reports["active"].report.Clients[0], counter) {
		t.Fatal("fleet snapshot aliases stored authenticated counters")
	}
	// Each call reads anew; neither counters nor receipt time may be cached.
	changed := service.reports["active"]
	changed.report.Clients[0].DownBytes = 90
	changed.receivedAtMS = 2000
	service.reports["active"] = changed
	second := service.fleetCounterReports(active)
	if second["active"].clients[0].downBytes != 90 || second["active"].receivedAtMS != 2000 || first["active"].clients[0].downBytes != 30 {
		t.Fatal("fleet snapshot reused old values or shares a stored slice")
	}
	if got := service.fleetCounterReports(nil); len(got) != 0 {
		t.Fatal("empty active fleet read retired reports")
	}
}
