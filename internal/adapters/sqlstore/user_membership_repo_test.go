package sqlstore

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"gorm.io/gorm"
)

func TestUserMembershipReadsOnlyNarrowColumnsInBoundedChunks(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	var rows []userRow
	var ids []int64
	for id := int64(1); id <= idReadChunk+2; id++ {
		rows = append(rows, userRow{ID: id, UPN: fmt.Sprintf("membership%d@example.test", id), SubToken: fmt.Sprintf("membership-token-%d", id), UUID: fmt.Sprintf("%036d", id), GroupID: id%2 + 1, Enabled: id%2 == 0})
		ids = append(ids, id)
	}
	if err := db.CreateInBatches(rows, 50).Error; err != nil {
		t.Fatal(err)
	}
	queries := 0
	db.Callback().Query().After("gorm:query").Register("membership-narrow", func(tx *gorm.DB) {
		if tx.Statement.Table != "users" {
			return
		}
		queries++
		sql := tx.Statement.SQL.String()
		if strings.Contains(sql, "SELECT *") || strings.Contains(sql, "password_hash") || strings.Contains(sql, "sub_token") || strings.Contains(sql, "uuid") {
			tx.AddError(errors.New("membership lookup materialized account secrets"))
		}
	})
	t.Cleanup(func() { _ = db.Callback().Query().Remove("membership-narrow") })
	repo := NewRepos(db).User.(ports.UserMembershipRepo)
	groups, err := repo.GroupIDsByIDs(t.Context(), append(ids, 1, 999999))
	if err != nil || len(groups) != len(rows) || queries != 2 || groups[1] != 2 || groups[2] != 1 {
		t.Fatalf("narrow chunk lookup: rows=%d queries=%d / %v", len(groups), queries, err)
	}
	queries = 0
	members, err := repo.MembersByGroupIDs(t.Context(), []int64{2, 1, 2, 999999})
	if err != nil || queries != 1 || len(members[1])+len(members[2]) != len(rows) || len(members[2]) == 0 || members[2][0] != 1 {
		t.Fatalf("membership omitted disabled users or duplicated groups: %+v queries=%d / %v", members, queries, err)
	}
	queries = 0
	if _, err := repo.GroupIDsByIDs(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.MembersByGroupIDs(t.Context(), nil); err != nil || queries != 0 {
		t.Fatalf("empty read hit SQL: queries=%d / %v", queries, err)
	}
}
