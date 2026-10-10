package domain

import (
	"cmp"
	"math"
	"slices"
	"strconv"
)

const (
	RiskCodeNoCollector      RiskCode = "no_collector"
	RiskCodeNoHits           RiskCode = "no_hits"
	RiskDestBlockWindowHours          = 24
	RiskDestBlockMaxSources           = 5
)

type DestBlockPolicy struct {
	Off       bool
	Threshold int
}

// DestBlockSource counts only frozen block hits for a currently existing
// policy selected for risk. Neither policy names nor destination data reach
// the evaluator: its evidence is retained longer than the original hits.
type DestBlockSource struct {
	Source string `json:"source"`
	Count  int64  `json:"count"`
}

type DestBlockInput struct {
	// CollectingNodes requires current client membership and the same recent,
	// applied, exact-digest minted-body proof as destination status. Historical
	// hits, a configured mode or an old applied status alone cannot set it.
	CollectingNodes int
	Sources         []DestBlockSource
	// These are related panel block losses, never losses attributed to a user.
	Losses DestAuditLosses
}

type DestBlockEvidence struct {
	V                int               `json:"v"`
	WindowHours      int               `json:"window_hours"`
	Threshold        int               `json:"threshold"`
	Total            int64             `json:"total"`
	BySource         []DestBlockSource `json:"by_source"`
	Nodes            int               `json:"nodes"`
	CoverageComplete bool              `json:"coverage_complete"`
	Losses           DestAuditLosses   `json:"losses"`
}

// EvaluateDestBlock produces six observe-only states. Trust and location
// exemptions are deliberately absent: neither may hide a blocking signal.
// Counts are observable lower bounds; a lower bound reaching the threshold
// can flag, but a lower count cannot establish complete coverage or safety.
func EvaluateDestBlock(p DestBlockPolicy, in DestBlockInput) (RiskVerdict, *DestBlockEvidence) {
	if p.Off {
		return RiskVerdict{State: GeoStateDisabled, Code: RiskCodeSignalOff}, nil
	}
	if in.CollectingNodes <= 0 {
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeNoCollector}, nil
	}
	threshold := RiskPolicyFromSettings(RiskPolicySettings{DestBlockThreshold: p.Threshold}).DestBlockThreshold
	ev := &DestBlockEvidence{V: RiskEvidenceVersion, WindowHours: RiskDestBlockWindowHours, Threshold: threshold, BySource: []DestBlockSource{}, Nodes: in.CollectingNodes,
		Losses: DestAuditLosses{Rows: max(in.Losses.Rows, 0), Events: max(in.Losses.Events, 0), Unmatched: max(in.Losses.Unmatched, 0), Scope: "panel"}}
	counts := map[string]int64{}
	for _, source := range in.Sources {
		if !destBlockPolicySource(source.Source) || source.Count <= 0 {
			continue
		}
		counts[source.Source] = destBlockAdd(counts[source.Source], source.Count)
		ev.Total = destBlockAdd(ev.Total, source.Count)
	}
	for source, count := range counts {
		ev.BySource = append(ev.BySource, DestBlockSource{Source: source, Count: count})
	}
	slices.SortFunc(ev.BySource, func(a, b DestBlockSource) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Source, b.Source))
	})
	ev.BySource = ev.BySource[:min(len(ev.BySource), RiskDestBlockMaxSources)]
	switch {
	case ev.Total == 0:
		return RiskVerdict{State: GeoStateIdle, Code: RiskCodeNoHits}, ev
	case ev.Total >= int64(threshold):
		return RiskVerdict{State: GeoStateFlagged, Code: RiskCodeOver}, ev
	case ev.Total >= int64((threshold+1)/2):
		return RiskVerdict{State: GeoStateSuspect, Code: RiskCodeOverBuilding}, ev
	default:
		return RiskVerdict{State: GeoStateClean, Code: RiskCodeWithin}, ev
	}
}

func destBlockPolicySource(source string) bool {
	if len(source) < 2 || source[0] != 'p' {
		return false
	}
	id, err := strconv.ParseInt(source[1:], 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == source[1:]
}

func destBlockAdd(a, b int64) int64 {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}
