package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// transitLinkRow is one tunnel between an ordered pair of Passwall Node
// servers. Secrets (the Shadowsocks key, the REALITY private key and UUID) are
// encrypted at rest like inbound settings.
//
// SENT AND APPLIED ARE BOTH KEPT, per side. "Sent" is the digest of the half
// PSP last put in that server's config (and, for the entry, the listeners bound
// to the link); "applied" is the digest of the half that server last confirmed
// holding. A node is ready only while applied equals sent on both sides and its
// listener is among the bound: a confirmation of older content can never make
// a newly bound node look ready, because the new send moves "sent" first.
type transitLinkRow struct {
	ID           int64 `gorm:"primaryKey;autoIncrement"`
	EntryPanelID int64 `gorm:"not null;uniqueIndex:uk_transit_link,priority:1"`
	ExitPanelID  int64 `gorm:"not null;uniqueIndex:uk_transit_link,priority:2;index"`

	Protocol    string `gorm:"size:16;not null"`
	ExitAddress string `gorm:"size:253;not null;default:''"`
	Port        int    `gorm:"not null"`

	SSMethod string `gorm:"size:32;not null"`
	SSKey    string `gorm:"type:text;not null"`

	RealityUUID       string `gorm:"type:text;not null"`
	RealityPrivateKey string `gorm:"type:text;not null"`
	RealityPublicKey  string `gorm:"size:64;not null"`
	RealityShortID    string `gorm:"size:16;not null;default:''"`
	RealityServerName string `gorm:"size:253;not null"`
	RealityTarget     string `gorm:"size:260;not null"`

	InSent     string `gorm:"size:64;not null;default:''"`
	InApplied  string `gorm:"size:64;not null;default:''"`
	OutSent    string `gorm:"size:64;not null;default:''"`
	OutApplied string `gorm:"size:64;not null;default:''"`
	// OutSentBound is the listeners bound to the link in what was last sent.
	OutSentBound jsonStrings `gorm:"type:text"`

	InUpBytes       int64  `gorm:"not null;default:0"`
	InDownBytes     int64  `gorm:"not null;default:0"`
	InLastRawUp     int64  `gorm:"not null;default:0"`
	InLastRawDown   int64  `gorm:"not null;default:0"`
	InCounterEpoch  uint64 `gorm:"not null;default:0"`
	OutUpBytes      int64  `gorm:"not null;default:0"`
	OutDownBytes    int64  `gorm:"not null;default:0"`
	OutLastRawUp    int64  `gorm:"not null;default:0"`
	OutLastRawDown  int64  `gorm:"not null;default:0"`
	OutCounterEpoch uint64 `gorm:"not null;default:0"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (transitLinkRow) TableName() string { return "transit_links" }

func (r transitLinkRow) toDomain() (domain.TransitLink, error) {
	ssKey, err := decryptSecret(r.SSKey)
	if err != nil {
		return domain.TransitLink{}, err
	}
	uuid, err := decryptSecret(r.RealityUUID)
	if err != nil {
		return domain.TransitLink{}, err
	}
	private, err := decryptSecret(r.RealityPrivateKey)
	if err != nil {
		return domain.TransitLink{}, err
	}
	return domain.TransitLink{
		ID: r.ID, EntryPanelID: r.EntryPanelID, ExitPanelID: r.ExitPanelID,
		Protocol: r.Protocol, ExitAddress: r.ExitAddress, Port: r.Port,
		SSMethod: r.SSMethod, SSKey: ssKey,
		RealityUUID: uuid, RealityPrivateKey: private, RealityPublicKey: r.RealityPublicKey,
		RealityShortID: r.RealityShortID, RealityServerName: r.RealityServerName, RealityTarget: r.RealityTarget,
		InSent: r.InSent, OutSent: r.OutSent, InApplied: r.InApplied, OutApplied: r.OutApplied,
		InUpBytes: r.InUpBytes, InDownBytes: r.InDownBytes, InLastRawUp: r.InLastRawUp, InLastRawDown: r.InLastRawDown, InCounterEpoch: r.InCounterEpoch,
		OutUpBytes: r.OutUpBytes, OutDownBytes: r.OutDownBytes, OutLastRawUp: r.OutLastRawUp, OutLastRawDown: r.OutLastRawDown, OutCounterEpoch: r.OutCounterEpoch,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, nil
}

type transitLinkRepo struct{ db *gorm.DB }

// EnsureLink returns the link of an ordered pair, creating it from create on
// first sight. The unique (entry, exit) index decides concurrent creations;
// the loser reads the winner's row.
func (r *transitLinkRepo) EnsureLink(ctx context.Context, entry, exit int64, create func() (domain.TransitLink, error)) (domain.TransitLink, error) {
	if link, err := r.GetLink(ctx, entry, exit); err == nil || !errors.Is(err, domain.ErrNotFound) {
		return link, err
	}
	fresh, err := create()
	if err != nil {
		return domain.TransitLink{}, err
	}
	ssKey, err := encryptSecret(fresh.SSKey)
	if err != nil {
		return domain.TransitLink{}, err
	}
	uuid, err := encryptSecret(fresh.RealityUUID)
	if err != nil {
		return domain.TransitLink{}, err
	}
	private, err := encryptSecret(fresh.RealityPrivateKey)
	if err != nil {
		return domain.TransitLink{}, err
	}
	row := transitLinkRow{
		EntryPanelID: entry, ExitPanelID: exit, Protocol: fresh.Protocol, ExitAddress: fresh.ExitAddress, Port: fresh.Port,
		SSMethod: fresh.SSMethod, SSKey: ssKey,
		RealityUUID: uuid, RealityPrivateKey: private, RealityPublicKey: fresh.RealityPublicKey,
		RealityShortID: fresh.RealityShortID, RealityServerName: fresh.RealityServerName, RealityTarget: fresh.RealityTarget,
		OutSentBound: jsonStrings{},
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return domain.TransitLink{}, err
	}
	return r.GetLink(ctx, entry, exit)
}

func (r *transitLinkRepo) GetLink(ctx context.Context, entry, exit int64) (domain.TransitLink, error) {
	var row transitLinkRow
	if err := r.db.WithContext(ctx).Where("entry_panel_id = ? AND exit_panel_id = ?", entry, exit).First(&row).Error; err != nil {
		return domain.TransitLink{}, wrapNotFound(err)
	}
	return row.toDomain()
}

// LinkEnds resolves link ids to {entry, exit} in one query (ids are chunked
// under every dialect's parameter limit). Unknown ids are simply absent.
func (r *transitLinkRepo) LinkEnds(ctx context.Context, ids []int64) (map[int64]domain.TransitLinkEnds, error) {
	out := make(map[int64]domain.TransitLinkEnds, len(ids))
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		part := ids[start:min(start+chunk, len(ids))]
		var rows []struct {
			ID           int64
			EntryPanelID int64
			ExitPanelID  int64
		}
		if err := r.db.WithContext(ctx).Model(&transitLinkRow{}).Select("id, entry_panel_id, exit_panel_id").Where("id IN ?", part).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("resolve transit links: %w", err)
		}
		for _, row := range rows {
			out[row.ID] = domain.TransitLinkEnds{EntryPanelID: row.EntryPanelID, ExitPanelID: row.ExitPanelID}
		}
	}
	return out, nil
}

// ListLinks returns every link, by id.
func (r *transitLinkRepo) ListLinks(ctx context.Context) ([]domain.TransitLink, error) {
	var rows []transitLinkRow
	if err := r.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.TransitLink, 0, len(rows))
	for _, row := range rows {
		link, err := row.toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, nil
}

// UpdateLinkSettings writes an administrator's settings. Credentials for both
// protocols exist from creation, so switching protocol needs nothing else.
func (r *transitLinkRepo) UpdateLinkSettings(ctx context.Context, id int64, settings domain.TransitLinkSettings) error {
	return r.db.WithContext(ctx).Model(&transitLinkRow{}).Where("id = ?", id).Updates(map[string]any{
		"protocol": settings.Protocol, "exit_address": settings.ExitAddress,
		"reality_server_name": settings.RealityServerName, "reality_target": settings.RealityTarget,
	}).Error
}

// RecordLinkSent stores what PSP put in one side's config. bound is nil for
// the exit. It writes only on a change.
func (r *transitLinkRepo) RecordLinkSent(ctx context.Context, id int64, side domain.LinkSide, digest string, bound []string) (bool, error) {
	db := r.db.WithContext(ctx).Model(&transitLinkRow{}).Where("id = ?", id)
	if side == domain.LinkSideIn {
		result := db.Where("in_sent <> ?", digest).Update("in_sent", digest)
		return result.RowsAffected > 0, result.Error
	}
	var row transitLinkRow
	if err := r.db.WithContext(ctx).Select("out_sent", "out_sent_bound").First(&row, id).Error; err != nil {
		return false, wrapNotFound(err)
	}
	if row.OutSent == digest && slices.Equal([]string(row.OutSentBound), bound) {
		return false, nil
	}
	return true, db.Updates(map[string]any{"out_sent": digest, "out_sent_bound": jsonStrings(bound)}).Error
}

// DeleteLink removes the link of a pair: its credentials and its port go with
// it, and a link made later for the pair starts from fresh ones.
func (r *transitLinkRepo) DeleteLink(ctx context.Context, entry, exit int64) (bool, error) {
	result := r.db.WithContext(ctx).Where("entry_panel_id = ? AND exit_panel_id = ?", entry, exit).Delete(&transitLinkRow{})
	return result.RowsAffected > 0, result.Error
}

// SetLinkApplied records what one side confirmed; "" withdraws it. It reports
// whether the stored value changed.
func (r *transitLinkRepo) SetLinkApplied(ctx context.Context, id int64, side domain.LinkSide, digest string) (bool, error) {
	column := "out_applied"
	if side == domain.LinkSideIn {
		column = "in_applied"
	}
	result := r.db.WithContext(ctx).Model(&transitLinkRow{}).Where("id = ? AND "+column+" <> ?", id, digest).Update(column, digest)
	return result.RowsAffected > 0, result.Error
}

// RecordLinkCounters folds one side's cumulative sample into its totals, with
// the same reset rules as forward counters.
func (r *transitLinkRepo) RecordLinkCounters(ctx context.Context, id int64, side domain.LinkSide, up, down int64, epoch uint64) error {
	if up < 0 || down < 0 {
		return errors.New("link counters cannot be negative")
	}
	prefix := "out_"
	if side == domain.LinkSideIn {
		prefix = "in_"
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row transitLinkRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, id).Error; err != nil {
			return err
		}
		total, lastUp, lastDown, lastEpoch := [2]int64{row.OutUpBytes, row.OutDownBytes}, row.OutLastRawUp, row.OutLastRawDown, row.OutCounterEpoch
		if side == domain.LinkSideIn {
			total, lastUp, lastDown, lastEpoch = [2]int64{row.InUpBytes, row.InDownBytes}, row.InLastRawUp, row.InLastRawDown, row.InCounterEpoch
		}
		deltaUp, deltaDown := up, down
		if epoch == lastEpoch {
			if up >= lastUp {
				deltaUp = up - lastUp
			}
			if down >= lastDown {
				deltaDown = down - lastDown
			}
		}
		return tx.Model(&transitLinkRow{}).Where("id = ?", id).Updates(map[string]any{
			prefix + "up_bytes": total[0] + deltaUp, prefix + "down_bytes": total[1] + deltaDown,
			prefix + "last_raw_up": up, prefix + "last_raw_down": down, prefix + "counter_epoch": epoch,
		}).Error
	})
}

// attachEgressState fills Node.EgressReady: both sides hold exactly what was
// last sent, and the node's listener is bound in it.
func attachEgressState(ctx context.Context, db *gorm.DB, nodes []*domain.Node) error {
	var pairs [][2]int64
	for _, node := range nodes {
		if node != nil {
			node.EgressReady = false
			if node.EgressPanelID != 0 {
				pairs = append(pairs, [2]int64{node.PanelID, node.EgressPanelID})
			}
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	var rows []transitLinkRow
	if err := db.Select("entry_panel_id", "exit_panel_id", "in_sent", "in_applied", "out_sent", "out_applied", "out_sent_bound").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("load egress state: %w", err)
	}
	byPair := make(map[[2]int64]transitLinkRow, len(rows))
	for _, row := range rows {
		byPair[[2]int64{row.EntryPanelID, row.ExitPanelID}] = row
	}
	for _, node := range nodes {
		if node == nil || node.EgressPanelID == 0 {
			continue
		}
		row, ok := byPair[[2]int64{node.PanelID, node.EgressPanelID}]
		node.EgressReady = ok && row.InSent != "" && row.InApplied == row.InSent &&
			row.OutSent != "" && row.OutApplied == row.OutSent &&
			slices.Contains([]string(row.OutSentBound), "lst_"+fmt.Sprint(node.ID))
	}
	return nil
}
