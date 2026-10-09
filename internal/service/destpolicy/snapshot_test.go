package destpolicy

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestDefinitionSnapshotRoundTripExcludesAdministrativeAndRosterData(t *testing.T) {
	d := domain.DestDefinitions{State: domain.DestPolicyState{Generation: 7, PublishedGeneration: 6, Paused: true}, Lists: []domain.DestList{readyList(1, "domain:example.com\n")}, Policies: []domain.DestPolicy{rulePolicy(1, domain.DestBlock, 1)}, Groups: []domain.DestGroupMode{{GroupID: 8, Mode: "allowlist", Stage: "trial", ListIDs: []int64{1}}}, Exemptions: []domain.DestExemption{{UserID: 2, Reason: "private reason", CreatedBy: 777}}}
	d.Lists[0].Name, d.Lists[0].SourceURL = "private list name", "https://example.com/secret-key"
	d.Lists[0].SourceText = []byte("# private comment\nexample.com")
	d.Lists[0].LastError = "private error"
	d.Lists[0].ParseReport = &domain.DestParseReport{Samples: []domain.DestParseSample{{Text: "private ignored sample"}}}
	d.Policies[0].Name = "private policy name"
	body, err := BuildDefinitionSnapshot(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private", "secret-key", "CreatedBy", "SourceText", "ParseReport", "usr_"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("snapshot leaks %s", secret)
		}
	}
	restored, err := DecodeDefinitionSnapshot(domain.DestPolicySnapshot{Generation: 7, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if restored.State.Generation != 7 || !restored.State.Paused {
		t.Fatalf("state lost: %+v", restored.State)
	}
	d.State.Paused, restored.State.Paused = false, false
	want, err := BuildPolicy(d, testRoster())
	if err != nil {
		t.Fatal(err)
	}
	got, err := BuildPolicy(restored, testRoster())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("semantic round trip: got=%+v want=%+v", got, want)
	}
}

func TestDefinitionCheckIncludesScopesAndCatchAllWithoutRealSubjects(t *testing.T) {
	p := rulePolicy(3, domain.DestBlock, 1)
	p.Scope, p.GroupIDs = domain.DestScopeGroups, []int64{999}
	d := domain.DestDefinitions{Policies: []domain.DestPolicy{p}, Groups: []domain.DestGroupMode{{GroupID: 8, Mode: "allowlist", Stage: "enforce"}}}
	for i := int64(1); i <= protocol.MaxDestinationSubjects+1; i++ {
		d.Exemptions = append(d.Exemptions, domain.DestExemption{UserID: i})
	}
	if err := CheckDefinitions(d); err != nil {
		t.Fatalf("placeholder or exemptions consumed real node quota: %v", err)
	}
	d.State.Paused = true
	d.Policies[0].Inline.Ports = "70000"
	if err := CheckDefinitions(d); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("paused hid malformed definition: %v", err)
	}
	d.Policies[0].Enabled = false
	if err := CheckDefinitions(d); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("disabled hid malformed definition: %v", err)
	}
}

func TestDefinitionCheckReportsAggregateBudget(t *testing.T) {
	for _, kind := range []string{"rules", "domains", "regexps", "cidrs"} {
		t.Run(kind, func(t *testing.T) {
			d := domain.DestDefinitions{}
			limit := int64(0)
			switch kind {
			case "rules":
				limit = protocol.MaxDestinationRules
				for i := int64(1); i <= limit+1; i++ {
					d.Policies = append(d.Policies, rulePolicy(i, domain.DestBlock, int(i)))
				}
			case "domains", "regexps", "cidrs":
				limit = protocol.MaxDestinationDomains
				if kind == "regexps" {
					limit = protocol.MaxDestinationRegexps
				}
				if kind == "cidrs" {
					limit = protocol.MaxDestinationCIDRs
				}
				var text strings.Builder
				for i := int64(0); i < limit/2+1; i++ {
					entry := fmt.Sprintf("domain:d%d.example.com", i)
					if kind == "regexps" {
						entry = fmt.Sprintf("regexp:site%d\\.example\\.com", i)
					}
					if kind == "cidrs" {
						entry = fmt.Sprintf("10.%d.%d.0/24", i/256, i%256)
					}
					text.WriteString(entry + "\n")
				}
				d.Lists = []domain.DestList{readyList(1, text.String())}
				for i := int64(1); i <= 2; i++ {
					p := rulePolicy(i, domain.DestBlock, int(i))
					p.ListIDs = []int64{1}
					d.Policies = append(d.Policies, p)
				}
			}
			err := CheckDefinitions(d)
			var typed *DefinitionError
			if !errors.As(err, &typed) || typed.Detail.Kind != kind || typed.Detail.Limit != limit || typed.Detail.Used <= limit {
				t.Fatalf("budget error=%v detail=%+v", err, typed)
			}
		})
	}
}

func TestDefinitionSnapshotRejectsCorruptionAndWrongGeneration(t *testing.T) {
	body, err := BuildDefinitionSnapshot(domain.DestDefinitions{State: domain.DestPolicyState{Generation: 2}})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []domain.DestPolicySnapshot{{Generation: 3, Body: body}, {Generation: 2, Body: []byte(`{}`)}, {Generation: 2, Body: append(append([]byte(nil), body...), []byte(`{}`)...)}, {Generation: 2, Body: []byte(`{"schema":99,"generation":2}`)}} {
		if _, err := DecodeDefinitionSnapshot(s); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("corrupt snapshot accepted: %v", err)
		}
	}
}

func TestDefinitionCheckRejectsUnreferencedCorruptionAndBroadInline(t *testing.T) {
	for _, l := range []domain.DestList{readyList(1, "domain:com\n"), readyList(1, "regexp:[\n"), {ID: 1, Kind: "unknown"}, {ID: 1, Kind: domain.DestListCustom, Entries: []byte("domain:example.com\n")}} {
		if err := CheckDefinitions(domain.DestDefinitions{Lists: []domain.DestList{l}}); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("invalid unreferenced list accepted: %+v / %v", l, err)
		}
	}
	p := rulePolicy(1, domain.DestBlock, 1)
	p.Inline.CIDRs = []string{"0.0.0.0/0"}
	if _, err := BuildPolicy(domain.DestDefinitions{Policies: []domain.DestPolicy{p}}, testRoster()); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("broad inline accepted: %v", err)
	}
}

func TestDefinitionCheckDefersFinalByteQuotaToNode(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 45000; i++ {
		fmt.Fprintf(&body, "domain:d%d.%s.longer-label.example.com\n", i, strings.Repeat("a", 63))
	}
	l := readyList(1, body.String())
	l.Kind = domain.DestListRemote
	p := rulePolicy(1, domain.DestBlock, 1)
	p.ListIDs = []int64{1}
	d := domain.DestDefinitions{Lists: []domain.DestList{l}, Policies: []domain.DestPolicy{p}}
	if err := CheckDefinitions(d); err != nil {
		t.Fatalf("node byte quota blocked publication: %v", err)
	}
	if _, err := BuildPolicy(d, testRoster()); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("final node bytes not bounded: %v", err)
	}
}
