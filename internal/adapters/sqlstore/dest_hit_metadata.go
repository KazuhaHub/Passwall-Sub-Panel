package sqlstore

import (
	"slices"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

func readHitSources(tx *gorm.DB, q domain.DestHitQuery, out *[]domain.DestHitSource) (map[string]string, error) {
	var sources []string
	if err := hitTimeScope(tx, q).Distinct("source").Order("source").Pluck("source", &sources).Error; err != nil {
		return nil, err
	}
	policies, groups := []int64{}, []int64{}
	for _, source := range sources {
		kind, id, ok := hitSourceID(source)
		if !ok {
			return nil, domain.ErrUnavailable
		}
		if kind == 'p' {
			policies = append(policies, id)
		} else {
			groups = append(groups, id)
		}
	}
	pnames, err := readHitDisplayNames(tx, &destPolicyRow{}, "name", policies)
	if err != nil {
		return nil, err
	}
	gnames, err := readHitDisplayNames(tx, &groupRow{}, "name", groups)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, source := range sources {
		kind, id, _ := hitSourceID(source)
		lookup := pnames
		if kind == 'g' {
			lookup = gnames
		}
		name := hitName(lookup, id)
		if name != nil {
			names[source] = *name
		}
		*out = append(*out, domain.DestHitSource{Source: source, Name: name})
	}
	return names, nil
}

// Every display lookup selects two public admin-display columns, in bounded
// IN statements. Credentials and list contents never enter these reads.
func readHitDisplayNames(tx *gorm.DB, model any, column string, ids []int64) (map[int64]string, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	out := map[int64]string{}
	for start := 0; start < len(ids); start += 200 {
		var rows []struct {
			ID   int64
			Name string
		}
		if err := tx.Model(model).Select("id, "+column+" AS name").Where("id IN ?", ids[start:min(start+200, len(ids))]).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			out[row.ID] = row.Name
		}
	}
	return out, nil
}

func hitName[K comparable](names map[K]string, key K) *string {
	if value, ok := names[key]; ok {
		return &value
	}
	return nil
}
