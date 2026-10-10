package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

func TestBuildDestinationReportOnlyRefreshKeepsNativeConfigIdentity(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	parsed, err := destlist.ParseCustom([]byte("domain:example.com\n"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	list := domain.DestList{Name: "stable content", Kind: domain.DestListGeosite, GeositeCategory: "finance", Entries: parsed.Entries, EntryCount: parsed.EntryCount, ContentSHA256: parsed.ContentSHA256, ParseReport: &parsed.Report, LastFetchedAt: &now}
	if err := a.destDefinitions.SaveList(t.Context(), &list, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	p := saveWiringPolicy(t, f)
	p.ListIDs = []int64{list.ID}
	if err := a.destDefinitions.SavePolicy(t.Context(), p, p.UpdatedAt, now); err != nil {
		t.Fatal(err)
	}
	if w := destinationListRequest(t, a, token, "POST", "publish", nil); w.Code != 200 {
		t.Fatal("publish fixture failed")
	}
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	before := syncNativeCacheFixture(t, a, f.credential, f.report)
	if before.Config.Body == nil || before.Config.Body.Policy == nil {
		t.Fatal("published native config fixture missing")
	}
	state, err := a.destDefinitions.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	if err != nil {
		t.Fatal(err)
	}
	next := domain.DestParseReport{Accepted: 1, Ignored: 1, IgnoredBroad: 1, Samples: []domain.DestParseSample{{Line: 2, Text: "domain:hsbc", Reason: "broad_entry"}}}
	if err := a.destDefinitions.CommitListRefresh(t.Context(), list, domain.DestListRefresh{Entries: parsed.Entries, ContentSHA256: parsed.ContentSHA256, EntryCount: 1, ParseReport: &next}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	after := syncNativeCacheFixture(t, a, f.credential, f.report)
	beforeBytes, err := json.Marshal(before.Config.Body)
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, err := json.Marshal(after.Config.Body)
	if err != nil || !bytes.Equal(beforeBytes, afterBytes) || before.Config.Version != after.Config.Version || before.Config.ETag != after.Config.ETag {
		t.Fatal("report-only refresh changed native config bytes, version or ETag")
	}
	current, err := a.destDefinitions.State(t.Context())
	if err != nil || current.Generation != state.Generation || current.PublishedGeneration != state.PublishedGeneration {
		t.Fatal("report-only refresh advanced definition or publication generation")
	}
	currentRuntime, err := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	if err != nil || currentRuntime.DesiredSHA256 != runtime.DesiredSHA256 || !bytes.Equal(currentRuntime.MintedBody, runtime.MintedBody) {
		t.Fatal("report-only refresh replaced the minted native candidate")
	}
	w := destinationListRequest(t, a, token, "GET", fmt.Sprintf("lists/%d", list.ID), nil)
	var detail struct {
		Report domain.DestParseReport `json:"parse_report"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.Report.IgnoredBroad != 1 || len(detail.Report.Samples) != 1 || detail.Report.Samples[0].Text != "domain:hsbc" {
		t.Fatal("unchanged native config hid the newly committed parse report")
	}
}
