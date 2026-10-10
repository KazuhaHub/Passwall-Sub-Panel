package sqlstore

import (
	"cmp"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"golang.org/x/net/publicsuffix"
	"gorm.io/gorm"
)

type hitReadGroup struct {
	value   domain.DestHitGroup
	users   map[int64]struct{}
	sources map[string]struct{}
}

func readHitGroups(tx *gorm.DB, q domain.DestHitQuery, names map[string]string, out *domain.DestHitPage) error {
	// Site folding requires the same public/private suffix list as the Node;
	// grouping by the last two labels would combine unrelated hosted sites.
	// Stream narrow rows and retain aggregate keys/cardinality sets only.
	rows, err := hitFilteredScope(tx, q).Select("dest", "user_id", "source", "count", "last_at").Rows()
	if err != nil {
		return err
	}
	groups := map[string]*hitReadGroup{}
	for rows.Next() {
		var dest, source string
		var user, count int64
		var last time.Time
		if err := rows.Scan(&dest, &user, &source, &count, &last); err != nil {
			rows.Close()
			return err
		}
		if _, _, ok := hitSourceID(source); !ok || count < 0 || user < 0 {
			rows.Close()
			return domain.ErrUnavailable
		}
		key := source
		switch q.GroupBy {
		case "user":
			key = strconv.FormatInt(user, 10)
		case "site":
			key = hitReadSite(dest)
		}
		group, ok := groups[key]
		if !ok {
			group = &hitReadGroup{value: domain.DestHitGroup{Key: key}, users: map[int64]struct{}{}, sources: map[string]struct{}{}}
			groups[key] = group
		}
		group.value.Count = auditReadAdd(group.value.Count, count)
		group.value.LastAt = max(group.value.LastAt, last.UnixMilli())
		if user > 0 {
			group.users[user] = struct{}{}
		}
		group.sources[source] = struct{}{}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	all := make([]domain.DestHitGroup, 0, len(groups))
	for _, group := range groups {
		group.value.Users = int64(len(group.users))
		group.value.Sources = int64(len(group.sources))
		all = append(all, group.value)
	}
	slices.SortFunc(all, func(a, b domain.DestHitGroup) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(b.LastAt, a.LastAt), cmp.Compare(a.Key, b.Key))
	})
	out.Total = int64(len(all))
	start := min((q.Page-1)*q.PageSize, len(all))
	end := min(start+q.PageSize, len(all))
	out.Groups = append(out.Groups, all[start:end]...)
	if q.GroupBy == "policy" {
		for i := range out.Groups {
			out.Groups[i].Name = hitName(names, out.Groups[i].Key)
		}
	}
	if q.GroupBy == "user" {
		ids := []int64{}
		for _, group := range out.Groups {
			id, _ := strconv.ParseInt(group.Key, 10, 64)
			if id > 0 {
				ids = append(ids, id)
			}
		}
		users, err := readHitDisplayNames(tx, &userRow{}, "upn", ids)
		if err != nil {
			return err
		}
		for i := range out.Groups {
			id, _ := strconv.ParseInt(out.Groups[i].Key, 10, 64)
			out.Groups[i].Name = hitName(users, id)
		}
	}
	return nil
}

func hitReadSite(dest string) string {
	if _, err := netip.ParseAddr(dest); err == nil {
		return "(ip)"
	}
	if site, err := publicsuffix.EffectiveTLDPlusOne(dest); err == nil {
		return site
	}
	return dest
}
