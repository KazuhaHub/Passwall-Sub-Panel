package sqlstore

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

const destStateRowID = 1

// bumpDestGeneration records that a DEFINITION changed: generation advances,
// last_write_at is stamped, and first_unpublished_at is stamped only if it is
// not already (the max-wait of the debounce counts from the FIRST write nobody
// has published yet, not from the latest one).
//
// It takes the caller's transaction on purpose. A definition write and its
// generation bump commit or roll back together: a bump without its write would
// publish an identical snapshot, and a write without its bump would never be
// published at all, because ensurePublished only looks at the generation.
//
// The row is created on first use rather than seeded at schema time, so a
// fresh install and an upgraded one reach it the same way, and the insert is a
// no-op once it exists (ON CONFLICT DO NOTHING / ON DUPLICATE KEY, portable
// through GORM).
func bumpDestGeneration(tx *gorm.DB, now time.Time) error {
	if err := ensureDestStateRow(tx); err != nil {
		return err
	}
	return tx.Model(&destPolicyStateRow{}).Where("id = ?", destStateRowID).Updates(map[string]any{
		"generation":           gorm.Expr("generation + 1"),
		"last_write_at":        now.UTC(),
		"first_unpublished_at": gorm.Expr("COALESCE(first_unpublished_at, ?)", now.UTC()),
	}).Error
}

func ensureDestStateRow(tx *gorm.DB) error {
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&destPolicyStateRow{ID: destStateRowID}).Error
}

// LOCK ORDER. Every writer of a destination definition takes the state row's
// lock FIRST (lockDestState) and only then any row lock of its own (a policy,
// a list), and takes nothing else in between: two writers then queue on one
// row instead of each holding a row the other wants, and a reader-then-writer
// pair (read "is this list in use", then write) cannot interleave with a
// policy write on PostgreSQL or MySQL, where READ COMMITTED would let it. The
// writers are policies (create, update, delete, reorder), lists (update,
// content, batch, delete), source rules, exemptions and a node's removal.
//
// lockDestState takes the state row's lock for the rest of the transaction.
// Writers that must not interleave — a policy that references lists against a
// list's deletion, two instantiations of one template — queue here, which is a
// lock every dialect has (SQLite serialises writers anyway).
func lockDestState(tx *gorm.DB) error {
	if err := ensureDestStateRow(tx); err != nil {
		return err
	}
	var row destPolicyStateRow
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", destStateRowID).Take(&row).Error
}

// ReconcileGeoIP records the digest of the GeoIP database the policy now
// reflects ("" for none), and advances the generation in the same transaction
// when bump is set. The caller compares the database's digest with Get's
// GeoIPSHA256, which is the PERSISTED value: a database that appeared,
// vanished or was replaced while the panel was down differs from it just the
// same.
func (r *DestStateRepo) ReconcileGeoIP(ctx context.Context, sha string, bump bool) error {
	if sha != "" && !isHexDigest(sha) {
		return fmt.Errorf("%w: geoip digest is not 64 lower-case hex characters", domain.ErrValidation)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureDestStateRow(tx); err != nil {
			return err
		}
		if err := tx.Model(&destPolicyStateRow{}).Where("id = ?", destStateRowID).Update("geoip_sha256", sha).Error; err != nil {
			return err
		}
		if bump {
			return bumpDestGeneration(tx, time.Now())
		}
		return nil
	})
}

// DestStateRepo owns dest_policy_state and dest_policy_snapshots: the
// publication bookkeeping, as opposed to the definitions it publishes.
type DestStateRepo struct{ db *gorm.DB }

// Get reads the state. A fresh install has no row yet and reads as all zeros.
func (r *DestStateRepo) Get(ctx context.Context) (*domain.DestPolicyState, error) {
	var row destPolicyStateRow
	res := r.db.WithContext(ctx).Where("id = ?", destStateRowID).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("read destination policy state: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return &domain.DestPolicyState{}, nil
	}
	return row.toDomain(), nil
}

func (row destPolicyStateRow) toDomain() *domain.DestPolicyState {
	return &domain.DestPolicyState{
		Generation: row.Generation, PublishedGeneration: row.PublishedGeneration,
		FirstUnpublishedAt: utcPtr(row.FirstUnpublishedAt), LastWriteAt: utcPtr(row.LastWriteAt),
		PublishedAt: utcPtr(row.PublishedAt), Paused: row.Paused, GeoIPSHA256: row.GeoIPSHA256, GeositeRelease: row.GeositeRelease,
		PublishError: domain.DecodeDestPublishError(row.PublishError), PublishErrorAt: utcPtr(row.PublishErrorAt),
	}
}

// Bump advances the generation for a change that is not a row of ours: the
// GeoIP database a source rule was expanded from changed under it.
func (r *DestStateRepo) Bump(ctx context.Context) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return bumpDestGeneration(tx, time.Now())
	})
}

// CompletePublish makes published_generation catch up to gen, as a
// compare-and-set against the generation the publisher started from: two
// publishers racing cannot both win, and a loser (false, nil) just reads the
// state again. first_unpublished_at clears only when nothing newer than gen
// exists — a write that landed while the snapshot was being built keeps its
// max-wait clock. A successful publish also clears the last publish error.
func (r *DestStateRepo) CompletePublish(ctx context.Context, gen, expectedPublished int64) (bool, error) {
	var won bool
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureDestStateRow(tx); err != nil {
			return err
		}
		// Locked read, compared here: the compare-and-set must not depend on
		// RowsAffected, which MySQL reports as rows CHANGED, not matched.
		var cur destPolicyStateRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", destStateRowID).Take(&cur).Error; err != nil {
			return err
		}
		if cur.PublishedGeneration != expectedPublished {
			won = false
			return nil
		}
		now := time.Now().UTC()
		if err := tx.Model(&destPolicyStateRow{}).Where("id = ?", destStateRowID).Updates(map[string]any{
			"published_generation": gen,
			"published_at":         now,
			"first_unpublished_at": gorm.Expr("CASE WHEN generation <= ? THEN NULL ELSE first_unpublished_at END", gen),
			"publish_error":        nil,
			"publish_error_at":     nil,
		}).Error; err != nil {
			return err
		}
		won = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("complete destination policy publish: %w", err)
	}
	return won, nil
}

// SetPaused flips the emergency switch. It is not a definition write — the
// generation does not move — because paused changes what the compiler does
// with the published snapshot, not the snapshot (the publisher reacts to the
// flag itself).
func (r *DestStateRepo) SetPaused(ctx context.Context, paused bool) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureDestStateRow(tx); err != nil {
			return err
		}
		return tx.Model(&destPolicyStateRow{}).Where("id = ?", destStateRowID).Update("paused", paused).Error
	})
}

// SetPublishError records why the last publish was refused, so the admin can
// read it from the status. It is cleared by the next CompletePublish.
func (r *DestStateRepo) SetPublishError(ctx context.Context, e *domain.DestPublishError, at time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureDestStateRow(tx); err != nil {
			return err
		}
		var stamp any
		if e != nil {
			stamp = at.UTC()
		}
		return tx.Model(&destPolicyStateRow{}).Where("id = ?", destStateRowID).Updates(map[string]any{
			"publish_error":    domain.EncodeDestPublishError(e),
			"publish_error_at": stamp,
		}).Error
	})
}

// SaveSnapshot stores the canonical serialisation of generation gen. A
// generation's snapshot is immutable: saving it twice keeps the first body, so
// two publishers that both built generation G cannot leave a body behind that
// the other one's digest does not describe.
func (r *DestStateRepo) SaveSnapshot(ctx context.Context, gen int64, body []byte) error {
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&destPolicySnapshotRow{Generation: gen, Body: body, CreatedAt: time.Now().UTC()}).Error
	if err != nil {
		return fmt.Errorf("save destination policy snapshot: %w", err)
	}
	return nil
}

// Snapshot reads generation gen's snapshot.
func (r *DestStateRepo) Snapshot(ctx context.Context, gen int64) (*domain.DestPolicySnapshot, error) {
	var row destPolicySnapshotRow
	res := r.db.WithContext(ctx).Where("generation = ?", gen).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, fmt.Errorf("read destination policy snapshot: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, domain.ErrNotFound
	}
	return &domain.DestPolicySnapshot{Generation: row.Generation, Body: row.Body, CreatedAt: row.CreatedAt.UTC()}, nil
}

// PruneSnapshotsExcept deletes every snapshot whose generation is not in keep
// and returns how many it deleted. Only the published snapshot and the one
// being published are worth keeping.
func (r *DestStateRepo) PruneSnapshotsExcept(ctx context.Context, keep ...int64) (int64, error) {
	q := r.db.WithContext(ctx)
	if len(keep) > 0 {
		q = q.Where("generation NOT IN ?", keep)
	} else {
		q = q.Where("1 = 1")
	}
	res := q.Delete(&destPolicySnapshotRow{})
	if res.Error != nil {
		return 0, fmt.Errorf("prune destination policy snapshots: %w", res.Error)
	}
	return res.RowsAffected, nil
}
