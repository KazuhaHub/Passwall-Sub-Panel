package destpolicy

import (
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
)

func TestVerifiedFinanceCategoryCompilesWithoutBroadEntriesForEveryAction(t *testing.T) {
	file, err := os.Open("../destlist/testdata/dlc-20261004053124.yml.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, destlist.MaxRemoteBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	const expectedSHA = "c0f7da9a7f95c86b354002650e8268b9d6bb0b638229274d3afa5651a7cf74b8"
	if len(raw) != 3614228 || fmt.Sprintf("%x", sha256.Sum256(raw)) != expectedSHA {
		t.Fatal("fixed upstream release bytes changed")
	}
	checksum, err := os.ReadFile("../destlist/testdata/dlc-20261004053124.sha256sum")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := destlist.ParseGeosite(raw, checksum)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := catalog.Select("category-finance", nil)
	if err != nil || parsed.EntryCount == 0 {
		t.Fatalf("finance is not usable: %v", err)
	}
	found := false
	for _, sample := range parsed.Report.Samples {
		if sample.Text == "domain:hsbc" && sample.Reason == "broad_entry" && sample.Line > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("finance report lost the actual public suffix and its source ordinal")
	}
	want := strings.Split(strings.TrimSuffix(string(parsed.Entries), "\n"), "\n")
	list := readyList(1, string(parsed.Entries))
	list.Kind, list.ContentSHA256, list.ParseReport = domain.DestListGeosite, parsed.ContentSHA256, &parsed.Report
	for _, use := range []string{"allow", "block", "observe", "allowlist-trial", "allowlist-enforce"} {
		t.Run(use, func(t *testing.T) {
			defs := domain.DestDefinitions{Lists: []domain.DestList{list}}
			if stage, ok := strings.CutPrefix(use, "allowlist-"); ok {
				defs.Groups = []domain.DestGroupMode{{GroupID: 8, Mode: "allowlist", Stage: stage, ListIDs: []int64{1}}}
			} else {
				policy := rulePolicy(1, domain.DestAction(use), 1)
				policy.ListIDs = []int64{1}
				defs.Policies = []domain.DestPolicy{policy}
			}
			policy, err := BuildPolicy(defs, testRoster())
			if err != nil || policy == nil {
				t.Fatalf("filtered finance could not compile: %v", err)
			}
			matched := 0
			for _, rule := range policy.Rules {
				if rule.CatchAll {
					continue // The explicit group fallback has no list entries.
				}
				matched++
				if !slices.Equal(rule.Domains, want) || len(rule.CIDRs) != 0 {
					t.Fatal("compiled list differs from the complete filtered finance category")
				}
				for _, entry := range rule.Domains {
					if destlist.IsBroad(entry) || entry == "domain:hsbc" {
						t.Fatalf("broad entry reached the policy: %q", entry)
					}
				}
			}
			if matched != 1 {
				t.Fatalf("missing or duplicated finance rule: %d", matched)
			}
		})
	}
}
