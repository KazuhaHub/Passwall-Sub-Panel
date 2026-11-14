package domain

import (
	"cmp"
	"slices"
)

// dest_block: how many of an account's connections were BLOCKED, over the last
// 24 hours, by destination policies an admin marked as counting toward risk.
//
// Observe-only like the other signals: nothing here suspends anyone. It counts
// block hits only. A report-only hit is a connection that was ALLOWED, and
// counting it would accuse an account of what the policy did not stop; the
// caller selects the rows, the evaluator sums what it is handed.
const (
	// RiskDefaultDestBlockThreshold is the blocked-connection count at which
	// the signal reads flagged. Suspect starts at half of it, rounded up.
	RiskDefaultDestBlockThreshold = 20
	// RiskDestBlockThresholdMax bounds the setting.
	RiskDestBlockThresholdMax = 10000
	// RiskDestBlockWindowHours is the fixed window.
	RiskDestBlockWindowHours = 24
	// RiskDestBlockEvidenceMaxSources bounds the evidence's per-source list.
	RiskDestBlockEvidenceMaxSources = 5
)

// DestBlockPolicy is the evaluator's policy: the switch and the threshold.
type DestBlockPolicy struct {
	Off       bool
	Threshold int
}

// DestBlockSourceCount is the blocked-hit count of one source (p<policy id> or
// s<node id>). A source is an identifier, never a destination.
type DestBlockSourceCount struct {
	Source string `json:"source"`
	Count  int64  `json:"count"`
}

// DestBlockInput is what one account's judgement reads. RecordingNodes is how
// many of the native nodes the account has a client on are recording hits.
type DestBlockInput struct {
	RecordingNodes int
	Sources        []DestBlockSourceCount
}

// DestBlockEvidence is stored with the verdict. It holds NO address, port or
// destination: it is copied into flag_records, which outlive the hits.
type DestBlockEvidence struct {
	V           int                    `json:"v"`
	WindowHours int                    `json:"window_hours"`
	Threshold   int                    `json:"threshold"`
	Total       int64                  `json:"total"`
	BySource    []DestBlockSourceCount `json:"by_source"`
	Nodes       int                    `json:"nodes"`
}

// EvaluateDestBlock judges one account. Branches, first match wins:
//
//  1. Off: disabled / signal_off.
//  2. no recording node: unknown / no_collector. Nothing was watching, so zero
//     blocks would be a claim nobody can make.
//  3. nothing blocked: idle / no_hits.
//  4. total below half the threshold: clean / within.
//  5. total below the threshold: suspect / over_building.
//  6. otherwise: flagged / over.
//
// It never returns exempt: trusting an account is about where it signs in
// from, not about what it tries to reach.
func EvaluateDestBlock(p DestBlockPolicy, in DestBlockInput) (RiskVerdict, *DestBlockEvidence) {
	threshold := p.Threshold
	if threshold <= 0 {
		threshold = RiskDefaultDestBlockThreshold
	}
	threshold = min(threshold, RiskDestBlockThresholdMax)
	switch {
	case p.Off:
		return RiskVerdict{State: GeoStateDisabled, Code: RiskCodeSignalOff}, nil
	case in.RecordingNodes <= 0:
		return RiskVerdict{State: GeoStateUnknown, Code: RiskCodeNoCollector}, nil
	}
	var total int64
	rows := make([]DestBlockSourceCount, 0, len(in.Sources))
	for _, s := range in.Sources {
		if s.Count > 0 {
			total += s.Count
			rows = append(rows, s)
		}
	}
	if total == 0 {
		return RiskVerdict{State: GeoStateIdle, Code: RiskCodeNoHits}, nil
	}
	slices.SortFunc(rows, func(a, b DestBlockSourceCount) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return cmp.Compare(a.Source, b.Source)
	})
	rows = rows[:min(len(rows), RiskDestBlockEvidenceMaxSources)]
	ev := &DestBlockEvidence{V: RiskEvidenceVersion, WindowHours: RiskDestBlockWindowHours, Threshold: threshold,
		Total: total, BySource: rows, Nodes: in.RecordingNodes}
	half := int64((threshold + 1) / 2)
	switch {
	case total >= int64(threshold):
		return RiskVerdict{State: GeoStateFlagged, Code: RiskCodeOver}, ev
	case total >= half:
		return RiskVerdict{State: GeoStateSuspect, Code: RiskCodeOverBuilding}, ev
	}
	return RiskVerdict{State: GeoStateClean, Code: RiskCodeWithin}, ev
}
