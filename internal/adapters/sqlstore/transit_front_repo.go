package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// transitFrontRow is one SNI front: a TCP port of a Passwall Node server that
// several TLS or REALITY uses share. The row id is what the front's wire key
// (fnt_<id>) is minted from; the body (listeners, routes, default) is one JSON
// document because it is written and read as a whole and never queried into.
// A server holds at most one front per port.
type transitFrontRow struct {
	ID      int64  `gorm:"primaryKey;autoIncrement"`
	PanelID int64  `gorm:"not null;uniqueIndex:uk_transit_front,priority:1"`
	Port    int    `gorm:"not null;uniqueIndex:uk_transit_front,priority:2"`
	Spec    string `gorm:"type:text;not null"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (transitFrontRow) TableName() string { return "transit_fronts" }

func (r transitFrontRow) toDomain() (domain.TransitFront, error) {
	front := domain.TransitFront{ID: r.ID, PanelID: r.PanelID, Port: r.Port, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if err := front.DecodeFrontSpec(r.Spec); err != nil {
		return domain.TransitFront{}, fmt.Errorf("front %d: %w", r.ID, err)
	}
	return front, nil
}

type transitFrontRepo struct{ db *gorm.DB }

// NewTransitFrontRepo returns the SNI front store.
func NewTransitFrontRepo(db *gorm.DB) *transitFrontRepo { return &transitFrontRepo{db: db} }

// ListByPanel returns a server's fronts in port order.
func (r *transitFrontRepo) ListByPanel(ctx context.Context, panelID int64) ([]domain.TransitFront, error) {
	var rows []transitFrontRow
	if err := r.db.WithContext(ctx).Where("panel_id = ?", panelID).Order("port ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return frontsOf(rows)
}

// List returns every front, by server then port.
func (r *transitFrontRepo) List(ctx context.Context) ([]domain.TransitFront, error) {
	var rows []transitFrontRow
	if err := r.db.WithContext(ctx).Order("panel_id ASC, port ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return frontsOf(rows)
}

func frontsOf(rows []transitFrontRow) ([]domain.TransitFront, error) {
	out := make([]domain.TransitFront, 0, len(rows))
	for _, row := range rows {
		front, err := row.toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, front)
	}
	return out, nil
}

// Get returns one front, domain.ErrNotFound when it does not exist.
func (r *transitFrontRepo) Get(ctx context.Context, id int64) (domain.TransitFront, error) {
	var row transitFrontRow
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.TransitFront{}, domain.ErrNotFound
		}
		return domain.TransitFront{}, err
	}
	return row.toDomain()
}

// Create stores a new front and returns it with its id. A second front on the
// same (server, port) is domain.ErrAlreadyExists.
func (r *transitFrontRepo) Create(ctx context.Context, front domain.TransitFront) (domain.TransitFront, error) {
	spec, err := front.EncodeSpec()
	if err != nil {
		return domain.TransitFront{}, err
	}
	row := transitFrontRow{PanelID: front.PanelID, Port: front.Port, Spec: spec}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		if isUniqueViolationErr(err) {
			return domain.TransitFront{}, domain.ErrAlreadyExists
		}
		return domain.TransitFront{}, err
	}
	return row.toDomain()
}

// Update replaces a front's port and body.
func (r *transitFrontRepo) Update(ctx context.Context, front domain.TransitFront) (domain.TransitFront, error) {
	spec, err := front.EncodeSpec()
	if err != nil {
		return domain.TransitFront{}, err
	}
	result := r.db.WithContext(ctx).Model(&transitFrontRow{}).Where("id = ? AND panel_id = ?", front.ID, front.PanelID).
		Updates(map[string]any{"port": front.Port, "spec": spec, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		if isUniqueViolationErr(result.Error) {
			return domain.TransitFront{}, domain.ErrAlreadyExists
		}
		return domain.TransitFront{}, result.Error
	}
	if result.RowsAffected == 0 {
		return domain.TransitFront{}, domain.ErrNotFound
	}
	return r.Get(ctx, front.ID)
}

// Delete removes a front of a server. It reports whether one existed.
func (r *transitFrontRepo) Delete(ctx context.Context, panelID, id int64) (bool, error) {
	result := r.db.WithContext(ctx).Where("id = ? AND panel_id = ?", id, panelID).Delete(&transitFrontRow{})
	return result.RowsAffected > 0, result.Error
}
