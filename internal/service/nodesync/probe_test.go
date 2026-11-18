package nodesync

import (
	"encoding/json"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/sourceaddr"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/linkprobe"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/transit"
)

var (
	forwardCaps = []string{nodeprotocol.CapabilityTransitForward}
	probeCaps   = []string{nodeprotocol.CapabilityTransitForward, nodeprotocol.CapabilityTransitLink, nodeprotocol.CapabilityTransitProbe}
)

// liveClock moves the fixture's clock to the present. The loaders judge a
// window's freshness against the wall clock, so a test of what a fresh result
// does must not report at the fixture's fixed date.
func (f *configAppliedFixture) liveClock() { f.now = time.Now().UTC().Truncate(time.Second) }

func (f *configAppliedFixture) withProbes() {
	f.service.SetTransit(transit.New(f.repos.Node, f.repos.Transit).WithLinks(f.repos.TransitLinks, f.repos.NodeAgent))
	f.service.SetProbes(linkprobe.New(f.repos.ScopedSettings, f.repos.TransitProbe, func() time.Time { return f.now }))
}

func (f *configAppliedFixture) setProbeSettings(t *testing.T, change func(*ports.UISettings)) {
	t.Helper()
	settings, err := f.repos.Settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	change(&settings)
	if err := f.repos.Settings.Save(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
}

func (f *configAppliedFixture) agentReport(caps []string, seq int64) nodeprotocol.NodeReport {
	return nodeprotocol.NodeReport{
		AgentID: f.agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: f.now.UnixMilli() + seq, Have: emptyProtocolHave(), Capabilities: caps,
	}
}

// probeReceipt is a full report from an entry that holds response's config and
// carries the given probe results.
func (f *configAppliedFixture) probeReceipt(response nodeprotocol.SyncResponse, seq int64, results ...nodeprotocol.ProbeResult) nodeprotocol.NodeReport {
	report := f.receipt(response)
	report.Capabilities, report.ReportedAtMS, report.Probes = probeCaps, f.now.UnixMilli()+seq, results
	return report
}

func window(key, kind string, attempts, failures int) nodeprotocol.ProbeResult {
	result := nodeprotocol.ProbeResult{Key: key, Kind: kind, Attempts: attempts, Failures: failures}
	if failures < attempts {
		result.RTTMinMS, result.RTTMedianMS, result.RTTMaxMS = 20, 30, 50
	}
	if failures > 0 {
		result.LastError = nodeprotocol.ProbeErrorTimeout
	}
	return result
}

func (f *configAppliedFixture) probeRows(t *testing.T) map[string]domain.ProbeState {
	t.Helper()
	rows, err := f.repos.TransitProbe.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]domain.ProbeState{}
	for _, row := range rows {
		out[row.ObjectKey+"/"+row.Kind] = row
	}
	return out
}

// The probe block goes only to an agent that declared transit.probe.v1, and an
// agent that did not gets the bytes it got before probes existed.
func TestProbeBlockIsSentOnlyToAnAgentThatDeclaredTheCapability(t *testing.T) {
	bare := newConfigAppliedFixture(t)
	bare.service.SetTransit(transit.New(bare.repos.Node, bare.repos.Transit).WithLinks(bare.repos.TransitLinks, bare.repos.NodeAgent))
	bare.managedLanding(t, 20443)
	f := newConfigAppliedFixture(t)
	f.withProbes()
	f.managedLanding(t, 20443)

	without, err := json.Marshal(bare.syncReport(t, bare.agentReport(forwardCaps, 0)).Config)
	if err != nil {
		t.Fatal(err)
	}
	with, err := json.Marshal(f.syncReport(t, f.agentReport(forwardCaps, 0)).Config)
	if err != nil {
		t.Fatal(err)
	}
	if string(without) != string(with) {
		t.Fatalf("an agent without transit.probe.v1 got a different config:\n%s\n%s", without, with)
	}

	declared := f.syncReport(t, f.agentReport(probeCaps, 1))
	probe := declared.Config.Body.Transit.Probe
	if probe == nil {
		t.Fatal("an agent that declared transit.probe.v1 got no probe block")
	}
	key := declared.Config.Body.Transit.Forwards[0].Key
	if probe.IntervalSeconds != 60 || probe.TimeoutMS != 5000 || probe.LinkURL != "" ||
		len(probe.Forwards) != 1 || probe.Forwards[0] != (nodeprotocol.ProbeTarget{Key: key, Method: nodeprotocol.ProbeMethodTCP}) {
		t.Fatalf("probe block = %+v", probe)
	}
}

func TestProbeBlockFollowsTheSettings(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.withProbes()
	f.managedLanding(t, 20443)

	f.setProbeSettings(t, func(s *ports.UISettings) {
		s.TransitProbeIntervalSeconds, s.TransitProbeTimeoutMS = 90, 2500
	})
	probe := f.syncReport(t, f.agentReport(probeCaps, 0)).Config.Body.Transit.Probe
	if probe == nil || probe.IntervalSeconds != 90 || probe.TimeoutMS != 2500 {
		t.Fatalf("probe block = %+v, want the configured cadence", probe)
	}

	f.setProbeSettings(t, func(s *ports.UISettings) { s.TransitProbeOff = true })
	if got := f.syncReport(t, f.agentReport(probeCaps, 1)).Config.Body.Transit.Probe; got != nil {
		t.Fatalf("probing is off but the config carries %+v", got)
	}
}

// The method is the landing's: tcp, quic for a UDP-only landing.
func TestForwardProbeMethodFollowsTheLanding(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.withProbes()
	landing := f.managedLanding(t, 20443)
	if err := f.db.Table("nodes").Where("id = ?", landing.ID).Update("desired_protocol", "hysteria2").Error; err != nil {
		t.Fatal(err)
	}
	probe := f.syncReport(t, f.agentReport(probeCaps, 0)).Config.Body.Transit.Probe
	if probe == nil || len(probe.Forwards) != 1 || probe.Forwards[0].Method != nodeprotocol.ProbeMethodQUIC {
		t.Fatalf("probe block = %+v, want quic for a UDP-only landing", probe)
	}
}

// Only an object in the config that was sent, on the side that probes it, is
// kept: a forward key never sent and a link key the server does not dial are
// dropped. (A kind that does not belong to its key never gets this far: the
// contract refuses the report.)
func TestProbeResultsAreFilteredToWhatWasSent(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.withProbes()
	f.managedLanding(t, 20443)
	response := f.syncReport(t, f.agentReport(probeCaps, 0))
	key := string(response.Config.Body.Transit.Forwards[0].Key)

	f.syncReport(t, f.probeReceipt(response, 1,
		window(key, nodeprotocol.ProbeKindForward, 20, 0),
		window(string(nodeprotocol.NewForwardKey(999)), nodeprotocol.ProbeKindForward, 20, 0), // never sent
		window(string(nodeprotocol.NewLinkKey(7)), nodeprotocol.ProbeKindE2E, 20, 0),          // not a link of this server
		window(string(nodeprotocol.NewLinkKey(7)), nodeprotocol.ProbeKindTransport, 20, 0),
	))
	rows := f.probeRows(t)
	if len(rows) != 1 || rows[key+"/forward"].State != domain.ProbeOK {
		t.Fatalf("stored %+v, want only the sent forward", rows)
	}
}

// An agent that did not declare the capability cannot book anything, and a
// report with probing off books nothing either.
func TestProbeResultsNeedTheCapabilityAndProbingOn(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.withProbes()
	f.managedLanding(t, 20443)
	response := f.syncReport(t, f.agentReport(probeCaps, 0))
	key := string(response.Config.Body.Transit.Forwards[0].Key)

	undeclared := f.probeReceipt(response, 1, window(key, nodeprotocol.ProbeKindForward, 20, 0))
	undeclared.Capabilities = forwardCaps
	f.syncReport(t, undeclared)
	if rows := f.probeRows(t); len(rows) != 0 {
		t.Fatalf("results from an agent that never declared the capability were stored: %+v", rows)
	}

	f.setProbeSettings(t, func(s *ports.UISettings) { s.TransitProbeOff = true })
	f.now = f.now.Add(time.Minute)
	f.syncReport(t, f.probeReceipt(response, 2, window(key, nodeprotocol.ProbeKindForward, 20, 0)))
	if rows := f.probeRows(t); len(rows) != 0 {
		t.Fatalf("results were stored while probing is off: %+v", rows)
	}
}

func (f *configAppliedFixture) landingRelay(t *testing.T, id int64) domain.RelayLine {
	t.Helper()
	node, err := f.repos.Node.GetByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return node.Relays[0]
}

// A managed line whose forward is down for the configured number of windows is
// withheld, and returns after the configured number of ok ones. A line with no
// data never withholds.
func TestManagedLineIsWithheldAndRestoredByItsProbes(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.liveClock()
	f.withProbes()
	landing := f.managedLanding(t, 20443)
	f.setProbeSettings(t, func(s *ports.UISettings) {
		s.TransitProbeWithhold, s.TransitProbeWithholdAfter, s.TransitProbeRestoreAfter = true, 2, 2
	})
	response := f.syncReport(t, f.agentReport(probeCaps, 0))
	key := string(response.Config.Body.Transit.Forwards[0].Key)
	// The entry confirms the forward, so it is published before any probe.
	confirm := f.probeReceipt(response, 1)
	confirm.Objects = append(confirm.Objects, nodeprotocol.ObjectStatus{
		Stream: nodeprotocol.StreamConfig, Key: key, State: nodeprotocol.ObjectApplied, SinceVersion: response.Config.Version,
	})
	f.syncReport(t, confirm)
	if !f.landingRelay(t, landing.ID).Publishable() {
		t.Fatal("a confirmed line is not published")
	}

	seq := int64(10)
	report := func(attempts, failures int) {
		seq++
		f.now = f.now.Add(time.Minute)
		r := f.probeReceipt(response, seq, window(key, nodeprotocol.ProbeKindForward, attempts, failures))
		r.Objects = confirm.Objects
		f.syncReport(t, r)
	}
	published := func() bool { return f.landingRelay(t, landing.ID).Publishable() }

	report(0, 0) // no attempt yet: unknown
	report(20, 20)
	if !published() {
		t.Fatal("one down window withheld the line (needs 2)")
	}
	before := f.invalidations
	report(20, 20)
	if published() {
		t.Fatal("two down windows did not withhold the line")
	}
	if f.invalidations == before {
		t.Fatal("withholding did not refresh subscriptions")
	}
	relay := f.landingRelay(t, landing.ID)
	if !relay.ProbeWithheld || relay.Probe == nil || relay.Probe.State != domain.ProbeDown || !relay.Probe.Withheld {
		t.Fatalf("relay probe state = %+v withheld=%v", relay.Probe, relay.ProbeWithheld)
	}
	report(20, 5) // degraded: neither restores nor changes anything
	if published() {
		t.Fatal("a degraded window restored the line")
	}
	report(20, 0)
	if published() {
		t.Fatal("one ok window restored the line (needs 2)")
	}
	report(20, 0)
	if !published() {
		t.Fatal("two ok windows did not restore the line")
	}
}

// With withholding off nothing is ever withheld, however long the line is
// down, and turning it off lifts a withholding that was in force.
func TestWithholdingOffNeverWithholds(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.liveClock()
	f.withProbes()
	landing := f.managedLanding(t, 20443)
	response := f.syncReport(t, f.agentReport(probeCaps, 0))
	key := string(response.Config.Body.Transit.Forwards[0].Key)
	confirm := f.probeReceipt(response, 1)
	confirm.Objects = append(confirm.Objects, nodeprotocol.ObjectStatus{
		Stream: nodeprotocol.StreamConfig, Key: key, State: nodeprotocol.ObjectApplied, SinceVersion: response.Config.Version,
	})
	f.syncReport(t, confirm)
	for i := int64(2); i < 12; i++ {
		f.now = f.now.Add(time.Minute)
		r := f.probeReceipt(response, i, window(key, nodeprotocol.ProbeKindForward, 20, 20))
		r.Objects = confirm.Objects
		f.syncReport(t, r)
	}
	relay := f.landingRelay(t, landing.ID)
	if !relay.Publishable() || relay.ProbeWithheld {
		t.Fatal("a line was withheld with withholding off")
	}
	if relay.Probe == nil || relay.Probe.State != domain.ProbeDown {
		t.Fatalf("the down state is not shown: %+v", relay.Probe)
	}
}

// A tunnel node: results are kept for the links its entry dials (e2e and
// transport), the node is withheld from its default exit's down probes, and a
// result from the exit's side is dropped.
func TestTunnelNodeIsWithheldByItsLinkProbes(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.liveClock()
	f.withProbes()
	exit := f.exitServer(t)
	if err := f.db.Table("nodes").Where("id = ?", f.node.ID).Update("egress_panel_id", exit.PanelID).Error; err != nil {
		t.Fatal(err)
	}
	f.setProbeSettings(t, func(s *ports.UISettings) {
		s.TransitProbeWithhold, s.TransitProbeWithholdAfter = true, 2
	})
	fromExit := sourceaddr.With(t.Context(), "198.51.100.7")
	exitReport := f.agentReport(probeCaps, 0)
	exitReport.AgentID = exit.AgentID
	exitResponse := f.syncAs(t, fromExit, exitReport)
	if exitResponse.Config.Body.Transit.Probe != nil {
		t.Fatalf("the exit side was asked to probe: %+v", exitResponse.Config.Body.Transit.Probe)
	}
	response := f.syncReport(t, f.agentReport(probeCaps, 1))
	out := response.Config.Body.Transit.LinksOut[0]
	probe := response.Config.Body.Transit.Probe
	if probe == nil || probe.LinkURL != ports.TransitProbeURLDefault {
		t.Fatalf("probe block = %+v", probe)
	}
	key := string(out.Key)
	node := func() *domain.Node {
		n, err := f.repos.Node.GetByID(t.Context(), f.node.ID)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	seq := int64(10)
	send := func(e2e, transport nodeprotocol.ProbeResult) {
		seq++
		f.now = f.now.Add(time.Minute)
		r := f.probeReceipt(response, seq, e2e, transport)
		r.Objects = nil
		f.syncReport(t, r)
	}
	up := func() (nodeprotocol.ProbeResult, nodeprotocol.ProbeResult) {
		return window(key, nodeprotocol.ProbeKindE2E, 20, 0), window(key, nodeprotocol.ProbeKindTransport, 20, 0)
	}
	send(window(key, nodeprotocol.ProbeKindE2E, 20, 20), window(key, nodeprotocol.ProbeKindTransport, 20, 0))
	if node().EgressWithheld {
		t.Fatal("one down window withheld the node (needs 2)")
	}
	send(window(key, nodeprotocol.ProbeKindE2E, 20, 20), window(key, nodeprotocol.ProbeKindTransport, 20, 0))
	got := node()
	if !got.EgressWithheld || got.EgressProbe == nil || got.EgressProbe.State != domain.ProbeDown || got.EgressProbe.Kind != domain.ProbeKindE2E {
		t.Fatalf("node egress = withheld %v probe %+v", got.EgressWithheld, got.EgressProbe)
	}
	// The exit's own report cannot say anything about this link.
	forged := f.agentReport(probeCaps, 99)
	forged.AgentID = exit.AgentID
	forged.Probes = []nodeprotocol.ProbeResult{window(key, nodeprotocol.ProbeKindE2E, 20, 0)}
	f.syncAs(t, fromExit, forged)
	if !node().EgressWithheld {
		t.Fatal("the exit side cleared the entry's verdict")
	}
	for i := 0; i < 3; i++ {
		send(up())
	}
	if node().EgressWithheld {
		t.Fatal("ok windows did not restore the node")
	}
}

// A result that is too old says nothing: the line shows no data and is never
// withheld, whatever flag the stored run carries.
func TestStaleProbeNeverWithholds(t *testing.T) {
	f := newConfigAppliedFixture(t) // the fixture's clock is months behind the wall clock
	f.withProbes()
	landing := f.managedLanding(t, 20443)
	f.setProbeSettings(t, func(s *ports.UISettings) {
		s.TransitProbeWithhold, s.TransitProbeWithholdAfter = true, 1
	})
	response := f.syncReport(t, f.agentReport(probeCaps, 0))
	key := string(response.Config.Body.Transit.Forwards[0].Key)
	confirm := f.probeReceipt(response, 1, window(key, nodeprotocol.ProbeKindForward, 20, 20))
	confirm.Objects = append(confirm.Objects, nodeprotocol.ObjectStatus{
		Stream: nodeprotocol.StreamConfig, Key: key, State: nodeprotocol.ObjectApplied, SinceVersion: response.Config.Version,
	})
	f.syncReport(t, confirm)
	if row := f.probeRows(t)[key+"/forward"]; !row.Withheld || row.State != domain.ProbeDown {
		t.Fatalf("stored row = %+v, want the down run flagged", row)
	}
	relay := f.landingRelay(t, landing.ID)
	if !relay.Publishable() || relay.ProbeWithheld || relay.Probe == nil || relay.Probe.State != domain.ProbeUnknown {
		t.Fatalf("a stale result withheld the line: publishable=%v probe=%+v", relay.Publishable(), relay.Probe)
	}
}

// An empty URL is transport-only: the block carries no link URL, and the e2e
// verdict does not exist.
func TestEmptyProbeURLIsTransportOnly(t *testing.T) {
	f := newConfigAppliedFixture(t)
	f.liveClock()
	f.withProbes()
	exit := f.exitServer(t)
	if err := f.db.Table("nodes").Where("id = ?", f.node.ID).Update("egress_panel_id", exit.PanelID).Error; err != nil {
		t.Fatal(err)
	}
	f.setProbeSettings(t, func(s *ports.UISettings) { s.TransitProbeURL, s.TransitProbeURLOff = "", true })
	exitReport := f.agentReport(probeCaps, 0)
	exitReport.AgentID = exit.AgentID
	f.syncAs(t, sourceaddr.With(t.Context(), "198.51.100.7"), exitReport)
	response := f.syncReport(t, f.agentReport(probeCaps, 1))
	probe := response.Config.Body.Transit.Probe
	if probe == nil || probe.LinkURL != "" {
		t.Fatalf("probe block = %+v, want one with no link URL", probe)
	}
	key := string(response.Config.Body.Transit.LinksOut[0].Key)
	r := f.probeReceipt(response, 2, window(key, nodeprotocol.ProbeKindE2E, 20, 0), window(key, nodeprotocol.ProbeKindTransport, 20, 0))
	r.Objects = nil
	f.syncReport(t, r)
	rows := f.probeRows(t)
	if _, ok := rows[key+"/e2e"]; ok || len(rows) != 1 {
		t.Fatalf("rows = %v, want the transport result only", rows)
	}
}
