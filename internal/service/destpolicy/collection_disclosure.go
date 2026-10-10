package destpolicy

import (
	"strconv"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// BuildCollectionDisclosure contains public counts and retention only. Usage
// can run with no executable hit rules, so it must not inherit hit availability.
func BuildCollectionDisclosure(current domain.DestStatusContext, settings domain.DestinationSettings, pollSeconds int, now time.Time) []domain.LegalAccessCollection {
	if pollSeconds <= 0 {
		pollSeconds = protocol.DefaultNextPollSeconds
	}
	counts := map[string]int{}
	seen := map[int64]bool{}
	for _, p := range current.Panels {
		if p.ID <= 0 || seen[p.ID] || !NeedsCollectionProof(p, time.Duration(pollSeconds)*time.Second, now) {
			continue
		}
		effective := string(EffectiveCollect(p.Collect, p.Agent.ObservedCapabilities, string(p.Agent.ObservedCoreEngine)))
		if p.Facts.Revision != p.CollectRevision || p.Facts.Collect != effective {
			continue
		}
		seen[p.ID] = true
		if p.Facts.Hits {
			counts["hits"]++
			if p.Facts.Trial {
				counts["trial"]++
			}
		}
		if effective == "hits_and_usage" {
			counts["usage"]++
		}
	}
	retention := settings.Effective()
	result := []domain.LegalAccessCollection{}
	for _, item := range []struct {
		kind string
		days int
	}{{"hits", retention.HitRetentionDays}, {"trial", retention.TrialRetentionDays}, {"usage", retention.UsageRetentionDays}} {
		if counts[item.kind] > 0 {
			result = append(result, domain.LegalAccessCollection{Kind: item.kind, Nodes: counts[item.kind], RetentionDays: item.days})
		}
	}
	return result
}

func trialFallback(rule protocol.DestinationRule) bool {
	if rule.Action != protocol.RuleObserve || !rule.CatchAll || len(rule.ID) < 2 || rule.ID[0] != 'g' {
		return false
	}
	id, err := strconv.ParseInt(rule.ID[1:], 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == rule.ID[1:]
}
