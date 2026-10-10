package destpolicy

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
)

type GroupTrialReportSite struct {
	domain.DestGroupTrialSite
	Added bool `json:"added"`
}

type GroupTrialReport struct {
	EffectiveDays  int                    `json:"effective_days"`
	Items          []GroupTrialReportSite `json:"items"`
	DroppedInRange int64                  `json:"dropped_in_range"`
	Losses         domain.DestAuditLosses `json:"losses"`
}

type GroupTrialReporter struct {
	readGroup       func(context.Context, int64) (*domain.Group, error)
	readDefinitions func(context.Context) (domain.DestDefinitions, error)
	readData        func(context.Context, domain.DestGroupTrialQuery) (domain.DestGroupTrialData, error)
	retention       func(context.Context) (int, error)
	gate            *operationgate.Gate
	now             func() time.Time
}

func NewGroupTrialReporter(readGroup func(context.Context, int64) (*domain.Group, error), readDefinitions func(context.Context) (domain.DestDefinitions, error), readData func(context.Context, domain.DestGroupTrialQuery) (domain.DestGroupTrialData, error), retention func(context.Context) (int, error)) *GroupTrialReporter {
	return &GroupTrialReporter{readGroup: readGroup, readDefinitions: readDefinitions, readData: readData, retention: retention, now: time.Now}
}

func (r *GroupTrialReporter) SetOperationGate(gate *operationgate.Gate) { r.gate = gate }

func (r *GroupTrialReporter) Read(ctx context.Context, groupID int64, days int) (GroupTrialReport, error) {
	if groupID <= 0 || days <= 0 {
		return GroupTrialReport{}, domain.ErrValidation
	}
	if r == nil || r.readGroup == nil || r.readDefinitions == nil || r.readData == nil || r.retention == nil {
		return GroupTrialReport{}, domain.ErrUnavailable
	}
	ctx, release, err := r.gate.Read(ctx)
	if err != nil {
		return GroupTrialReport{}, err
	}
	defer release()
	group, err := r.readGroup(ctx, groupID)
	if errors.Is(err, domain.ErrNotFound) || err == nil && (group == nil || group.ID != groupID) {
		return GroupTrialReport{}, domain.ErrNotFound
	}
	if err != nil {
		return GroupTrialReport{}, domain.ErrUnavailable
	}
	retention, err := r.retention(ctx)
	if err != nil || retention < 1 || retention > 30 {
		return GroupTrialReport{}, domain.ErrUnavailable
	}
	days = min(days, retention)
	defs, err := r.readDefinitions(ctx)
	if err != nil || CheckDefinitions(defs) != nil {
		return GroupTrialReport{}, domain.ErrUnavailable
	}
	coverage, err := groupTrialCoverage(defs, groupID)
	if err != nil {
		return GroupTrialReport{}, err
	}
	now := r.now().UTC()
	data, err := r.readData(ctx, domain.DestGroupTrialQuery{GroupID: groupID, Since: now.Add(-time.Duration(days) * 24 * time.Hour), Until: now})
	if ctx.Err() != nil {
		return GroupTrialReport{}, ctx.Err()
	}
	if err != nil || data.Losses.Scope != "panel" || data.Losses.Complete || data.Losses.Rows < 0 || data.Losses.Events < 0 || data.Losses.Unmatched < 0 || data.DroppedInRange != data.Losses.Rows {
		return GroupTrialReport{}, domain.ErrUnavailable
	}
	out := GroupTrialReport{EffectiveDays: days, Items: []GroupTrialReportSite{}, DroppedInRange: data.DroppedInRange, Losses: data.Losses}
	for _, item := range data.Items {
		if item.Site == "" || item.Count <= 0 || item.LastAt <= 0 {
			return GroupTrialReport{}, domain.ErrUnavailable
		}
		added := false
		if item.Site != "(ip)" {
			for suffix := item.Site; suffix != ""; {
				if coverage[suffix] {
					added = true
					break
				}
				_, rest, ok := strings.Cut(suffix, ".")
				if !ok {
					break
				}
				suffix = rest
			}
		}
		out.Items = append(out.Items, GroupTrialReportSite{DestGroupTrialSite: item, Added: added})
	}
	return out, nil
}

func groupTrialCoverage(defs domain.DestDefinitions, groupID int64) (map[string]bool, error) {
	mode := domain.DestGroupMode{GroupID: groupID, Mode: "open"}
	for _, group := range defs.Groups {
		if group.GroupID == groupID {
			mode = group
			break
		}
	}
	if _, err := domain.DestinationGroupAccessMode(mode.Mode, mode.Stage); err != nil {
		return nil, domain.ErrUnavailable
	}
	lists := map[int64]domain.DestList{}
	for _, list := range defs.Lists {
		lists[list.ID] = list
	}
	if mode.BaseListID < 0 || mode.ExtraListID < 0 || mode.BaseListID > 0 && mode.BaseListID == mode.ExtraListID || mode.Mode == "allowlist" && (mode.BaseListID == 0 || mode.ExtraListID == 0) {
		return nil, domain.ErrUnavailable
	}
	for _, id := range positiveIDs(mode.BaseListID, mode.ExtraListID) {
		list, ok := lists[id]
		if !ok || list.Kind != domain.DestListCustom || list.OwnerGroupID != groupID {
			return nil, domain.ErrUnavailable
		}
	}
	ids := append(append([]int64{}, mode.ListIDs...), positiveIDs(mode.BaseListID, mode.ExtraListID)...)
	for _, id := range ids {
		list, ok := lists[id]
		if !ok || list.OwnerGroupID != 0 && list.OwnerGroupID != groupID {
			return nil, domain.ErrUnavailable
		}
	}
	domains, _, err := listMatches(lists, uniqueIDs(ids))
	if err != nil {
		return nil, domain.ErrUnavailable
	}
	coverage := map[string]bool{}
	for _, entry := range domains {
		// A folded site has no original host. Exact hosts and arbitrary regexes
		// cannot prove that every subdomain represented by this row is allowed.
		if suffix, ok := strings.CutPrefix(entry, "domain:"); ok {
			coverage[suffix] = true
		}
	}
	return coverage, nil
}
