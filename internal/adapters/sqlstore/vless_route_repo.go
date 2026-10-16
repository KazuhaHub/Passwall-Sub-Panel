package sqlstore

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

type jsonRouteState domain.NodeRouteState

func (j jsonRouteState) Value() (driver.Value, error) {
	b, err := json.Marshal(j)
	return string(b), err
}
func (jsonRouteState) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Dialector.Name() == "mysql" {
		return "longtext"
	}
	return "text"
}
func (j *jsonRouteState) Scan(value any) error {
	*j = jsonRouteState{}
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return fmt.Errorf("unsupported route state type %T", value)
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, j)
}

type routeLeaseRow struct {
	Backend string    `gorm:"primaryKey;size:64"`
	Owner   string    `gorm:"size:64;not null"`
	Until   time.Time `gorm:"column:expires_at;not null"`
}

func (routeLeaseRow) TableName() string { return "vless_route_leases" }

func (r *nodeRepo) SaveRouteState(ctx context.Context, nodeID, revision int64, state *domain.NodeRouteState) error {
	if state == nil {
		return domain.ErrValidation
	}
	next := *state
	next.Revision = revision + 1
	result := r.db.WithContext(ctx).Model(&nodeRow{}).Where("id = ? AND route_revision = ?", nodeID, revision).Updates(map[string]any{"route_state": jsonRouteState(next), "route_revision": next.Revision})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("%w: node route revision changed; reload node detail", domain.ErrConflict)
	}
	*state = next
	return nil
}

func (r *nodeRepo) AcquireRouteLease(ctx context.Context, backend, owner string, until time.Time) (bool, error) {
	row := routeLeaseRow{Backend: backend, Owner: owner, Until: until}
	created := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if created.Error != nil {
		return false, created.Error
	}
	if created.RowsAffected == 1 {
		return true, nil
	}
	result := r.db.WithContext(ctx).Model(&routeLeaseRow{}).Where("backend = ? AND expires_at < ?", backend, time.Now().UTC()).Updates(map[string]any{"owner": owner, "expires_at": until})
	return result.RowsAffected == 1, result.Error
}

func (r *nodeRepo) ReleaseRouteLease(ctx context.Context, backend, owner string) error {
	return r.db.WithContext(ctx).Where("backend = ? AND owner = ?", backend, owner).Delete(&routeLeaseRow{}).Error
}
