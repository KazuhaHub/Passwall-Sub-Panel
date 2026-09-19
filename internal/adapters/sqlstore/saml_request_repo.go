package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// samlRequestRepo is the durable store behind ports.SAMLRequestRepo: the
// server-side half of "we really did start this SAML login". See that interface
// for why the record exists and why the claim must be atomic.
type samlRequestRepo struct{ db *gorm.DB }

func (r *samlRequestRepo) Create(ctx context.Context, req *domain.SAMLLoginRequest) error {
	// A duplicate token_hash surfaces as the database's own unique-constraint
	// error rather than being upserted: the token is 32 random bytes, so a
	// collision means something upstream is broken and should be loud.
	row := samlLoginRequestRow{
		TokenHash:    req.TokenHash,
		BrowserHash:  req.BrowserHash,
		RequestID:    req.RequestID,
		ConfigDigest: req.ConfigDigest,
		ReturnTo:     req.ReturnTo,
		CreatedAt:    req.CreatedAt.UTC(),
		ExpiresAt:    req.ExpiresAt.UTC(),
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("saml request: create: %w", err)
	}
	return nil
}

// Consume claims the request and returns its payload, or returns
// domain.ErrSAMLRequestInvalid.
//
// The read and the claim are two statements, and that is safe because the claim
// re-checks every condition that matters in its WHERE clause. The read exists
// only to fetch the immutable payload (request ID, return path), which cannot
// change under us — the columns a caller could observe changing are consumed_at
// and expires_at, and both are decided by the claim.
//
// A failed claim leaves the row exactly as it was, so an attacker guessing at a
// stolen token cannot burn the legitimate browser's request.
func (r *samlRequestRepo) Consume(ctx context.Context, tokenHash, browserHash, configDigest string, now time.Time) (*domain.SAMLLoginRequest, error) {
	var row samlLoginRequestRow
	err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).Take(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		// Unknown token and wrong binding are reported identically on purpose:
		// distinguishing them would hand an attacker a free oracle for how far
		// along a stolen token it has got.
		return nil, domain.ErrSAMLRequestInvalid
	case err != nil:
		// An infrastructure failure is NOT reported as an invalid request. The
		// caller has to be able to tell "this login attempt is bad" from "we
		// could not check", because only the first is routine.
		return nil, fmt.Errorf("saml request: read: %w", err)
	}

	claim := r.db.WithContext(ctx).
		Model(&samlLoginRequestRow{}).
		Where("token_hash = ? AND browser_hash = ? AND config_digest = ? "+
			"AND consumed_at IS NULL AND expires_at > ?",
			tokenHash, browserHash, configDigest, now.UTC()).
		Updates(map[string]any{"consumed_at": now.UTC()})
	if claim.Error != nil {
		return nil, fmt.Errorf("saml request: claim: %w", claim.Error)
	}
	if claim.RowsAffected != 1 {
		// Already consumed, expired, or bound to a different browser or
		// configuration. Any concurrent claimant loses here, which is what makes
		// the claim single-use.
		return nil, domain.ErrSAMLRequestInvalid
	}

	consumed := now.UTC()
	row.ConsumedAt = &consumed
	return samlLoginRequestToDomain(&row), nil
}

func (r *samlRequestRepo) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("expires_at <= ?", now.UTC()).
		Delete(&samlLoginRequestRow{})
	return res.RowsAffected, res.Error
}

func samlLoginRequestToDomain(row *samlLoginRequestRow) *domain.SAMLLoginRequest {
	return &domain.SAMLLoginRequest{
		TokenHash:    row.TokenHash,
		BrowserHash:  row.BrowserHash,
		RequestID:    row.RequestID,
		ConfigDigest: row.ConfigDigest,
		ReturnTo:     row.ReturnTo,
		CreatedAt:    row.CreatedAt,
		ExpiresAt:    row.ExpiresAt,
		ConsumedAt:   row.ConsumedAt,
	}
}
