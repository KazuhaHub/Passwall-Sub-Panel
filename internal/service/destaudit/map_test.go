package destaudit

import (
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

func mappingHit(rule, action, subject, dest string, port uint16, count uint32, offset int64) protocol.AuditHit {
	const hour = int64(1_800_000_000_000)
	return protocol.AuditHit{Hour: hour, RuleID: rule, Action: action, Subject: protocol.SubjectKey(subject), Dest: dest, Port: port, Count: count, FirstMS: hour + offset, LastMS: hour + offset + 5}
}

func TestMappingMergesFinalPrimaryKeysBeforePostgresStatements(t *testing.T) {
	b := queueFixture("block", 1)
	b.Hits = []protocol.AuditHit{
		mappingHit("p12x2", "block", "usr_7", "example.com", 443, math.MaxUint32, 20),
		mappingHit("p12x1", "block", "usr_7", "example.com", 443, 3, 10),
		mappingHit("p12", "block", "usr_7", "example.com", 443, 4, 30),
		mappingHit("g5", "block", "usr_7", "example.com", 443, 2, 40),
		mappingHit("p12", "block", "usr_8", "example.com", 443, 5, 50),
		mappingHit("p12", "block", "usr_7", "example.com", 80, 6, 60),
		mappingHit("p12", "block", "usr_7", "other.example.com", 443, 7, 70),
	}
	if err := protocol.ValidateAuditObservation(b); err != nil {
		t.Fatal(err)
	}
	got := mapBatch(9, b, map[int64]bool{7: true, 8: true}, time.UnixMilli(b.Hour))
	if len(got.hits) != 5 || len(got.usage) != 0 || len(got.losses) != 0 {
		t.Fatalf("mapped shape hits%d usage%d losses%v", len(got.hits), len(got.usage), got.losses)
	}
	merged := 0
	for _, row := range got.hits {
		if row.PanelID != 9 || row.HourMS != b.Hour || row.Action != "block" {
			t.Fatal("mapping changed frozen identity/action")
		}
		if row.Source == "p12" && row.UserID == 7 && row.Dest == "example.com" && row.Port == 443 {
			merged++
			if row.Count != int64(math.MaxUint32)+7 || row.FirstAt.UnixMilli() != b.Hour+10 || row.LastAt.UnixMilli() != b.Hour+35 {
				t.Fatalf("merged row wrong: %+v", row)
			}
		}
	}
	if merged != 1 {
		t.Fatal("duplicate final primary key escaped mapping")
	}
	// Budget truncation must retain the same keys after a peer reorders input.
	reversed := b
	reversed.Hits = slices.Clone(b.Hits)
	slices.Reverse(reversed.Hits)
	if other := mapBatch(9, reversed, map[int64]bool{7: true, 8: true}, time.UnixMilli(b.Hour)); !reflect.DeepEqual(got, other) {
		t.Fatal("logical row order depends on peer input")
	}
}

func TestMappingKeepsAnonymousTrialAndDeletedSource(t *testing.T) {
	b := queueFixture("trial", 2)
	b.Hits = []protocol.AuditHit{mappingHit("g999", "observe", "", "example.com", 0, 7, 10), mappingHit("g999", "observe", "", "(ip)", 0, 2, 20)}
	if err := protocol.ValidateAuditObservation(b); err != nil {
		t.Fatal(err)
	}
	got := mapBatch(9, b, nil, time.UnixMilli(b.Hour))
	if len(got.hits) != 2 || len(got.losses) != 0 {
		t.Fatal("trial treated as unknown account or deleted policy")
	}
	for _, row := range got.hits {
		if row.UserID != 0 || row.Port != 0 || row.Action != "observe" || row.Source != "g999" {
			t.Fatal("trial privacy/frozen source changed")
		}
	}
}

func TestMappingUnknownSubjectCountsRowsNotEvents(t *testing.T) {
	b := queueFixture("observe", 3)
	b.Hits = []protocol.AuditHit{mappingHit("p12x1", "observe", "usr_7", "example.com", 443, 100, 10), mappingHit("p12x2", "observe", "usr_999", "example.com", 443, 200, 20)}
	if err := protocol.ValidateAuditObservation(b); err != nil {
		t.Fatal(err)
	}
	got := mapBatch(9, b, map[int64]bool{7: true, 999: false}, time.UnixMilli(b.Hour))
	if len(got.hits) != 1 || got.hits[0].Count != 100 || got.losses["unknown_subject"] != 1 {
		t.Fatalf("wrong subject loss/count: %+v", got)
	}
}

func TestMappingUsagePreservesSuppliedCollapsedSite(t *testing.T) {
	b := queueFixture("usage", 4)
	b.Usage = []protocol.AuditUsage{{Hour: b.Hour, Subject: "usr_7", Site: "foo.github.io", Count: 4}, {Hour: b.Hour, Subject: "usr_7", Site: "(ip)", Count: 5}, {Hour: b.Hour, Subject: "usr_999", Site: "example.com", Count: 300}}
	if err := protocol.ValidateAuditObservation(b); err != nil {
		t.Fatal(err)
	}
	got := mapBatch(9, b, map[int64]bool{7: true}, time.UnixMilli(b.Hour))
	if len(got.hits) != 0 || len(got.usage) != 2 || got.losses["unknown_subject"] != 1 {
		t.Fatalf("wrong usage rows/loss: %+v", got)
	}
	for _, row := range got.usage {
		if row.UserID != 7 || row.PanelID != 9 || (row.Site != "foo.github.io" && row.Site != "(ip)") {
			t.Fatal("receiver refolded supplied site")
		}
	}
}

func TestMappingAgeBoundariesUseReceiverClock(t *testing.T) {
	for _, kind := range []string{"block", "usage", "trial"} {
		b := queueFixture(kind, 5)
		if kind == "usage" {
			b.Usage = []protocol.AuditUsage{{Hour: b.Hour, Subject: "usr_7", Site: "example.com", Count: 500}}
		} else if kind == "trial" {
			b.Hits = []protocol.AuditHit{mappingHit("g1", "observe", "", "example.com", 0, 500, 10)}
		} else {
			b.Hits = []protocol.AuditHit{mappingHit("p1", "block", "usr_7", "example.com", 443, 500, 10)}
		}
		for _, tc := range []struct {
			name           string
			receiverOffset time.Duration
			kept           bool
		}{{"old inclusive", 48 * time.Hour, true}, {"too old", 48*time.Hour + time.Millisecond, false}, {"future inclusive", -time.Hour, true}, {"too new", -time.Hour - time.Millisecond, false}} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				got := mapBatch(9, b, map[int64]bool{7: true}, time.UnixMilli(b.Hour).Add(tc.receiverOffset))
				rows := len(got.hits) + len(got.usage)
				if tc.kept && (rows != 1 || len(got.losses) != 0) {
					t.Fatalf("boundary row lost: %+v", got)
				}
				if !tc.kept && (rows != 0 || got.losses["out_of_range"] != 1) {
					t.Fatalf("out of range rows/events mixed: %+v", got)
				}
			})
		}
	}
}
