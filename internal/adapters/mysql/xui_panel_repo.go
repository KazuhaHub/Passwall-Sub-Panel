package mysql

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type xuiPanelRepo struct{ db *gorm.DB }

func (r *xuiPanelRepo) List(ctx context.Context) ([]*domain.XUIPanel, error) {
	var rows []xuiPanelRow
	if err := r.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.XUIPanel, len(rows))
	for i := range rows {
		panel, err := rows[i].toDomain()
		if err != nil {
			return nil, err
		}
		out[i] = panel
	}
	return out, nil
}

func (r *xuiPanelRepo) GetByID(ctx context.Context, id int64) (*domain.XUIPanel, error) {
	var row xuiPanelRow
	if err := r.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain()
}

func (r *xuiPanelRepo) GetByName(ctx context.Context, name string) (*domain.XUIPanel, error) {
	var row xuiPanelRow
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return row.toDomain()
}

func (r *xuiPanelRepo) Save(ctx context.Context, p *domain.XUIPanel) error {
	if p.Name == "" {
		return fmt.Errorf("%w: panel name required", domain.ErrValidation)
	}
	if p.URL == "" {
		return fmt.Errorf("%w: panel url required", domain.ErrValidation)
	}
	row, err := xuiPanelFromDomain(p)
	if err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).Save(row).Error; err != nil {
		return err
	}
	p.ID = row.ID
	return nil
}

// Delete removes a panel row, but refuses the operation when any nodes
// or owned client rows still point at it. AutoMigrate doesn't emit FK
// constraints, so without this app-level guard a delete would leave
// `nodes.panel_id` / `user_xui_clients.panel_id` pointing at a non-
// existent row — every subsequent traffic poll / pool.Get on that
// dangling reference panics on nil. Caller is expected to clean the
// referencing rows (or reassign them) first.
func (r *xuiPanelRepo) Delete(ctx context.Context, id int64) error {
	var nodeRefs int64
	if err := r.db.WithContext(ctx).Model(&nodeRow{}).Where("panel_id = ?", id).Count(&nodeRefs).Error; err != nil {
		return err
	}
	if nodeRefs > 0 {
		return fmt.Errorf("%w: panel still has %d node(s); remove or reassign them first", domain.ErrValidation, nodeRefs)
	}
	var clientRefs int64
	if err := r.db.WithContext(ctx).Model(&ownershipRow{}).Where("panel_id = ?", id).Count(&clientRefs).Error; err != nil {
		return err
	}
	if clientRefs > 0 {
		return fmt.Errorf("%w: panel still owns %d user client row(s); detach them first", domain.ErrValidation, clientRefs)
	}
	return r.db.WithContext(ctx).Delete(&xuiPanelRow{}, id).Error
}
