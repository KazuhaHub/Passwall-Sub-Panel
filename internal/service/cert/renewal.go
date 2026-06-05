// Package cert owns the PSP-managed TLS certificate lifecycle: ACME issuance
// and renewal (through the async sync-task queue), inline deploy into bound
// node inbounds, and the renewal scan.
package cert

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// renewDue reports whether a certificate should be renewed now.
//
// Threshold T defaults to the admin-configured "renew N days before expiry".
// But if N exceeds 2/3 of the certificate's total lifetime — a short-lived
// cert where a fixed N-day rule would renew almost immediately and thrash
// (Let's Encrypt is moving toward 45-day and shorter certs) — T falls back to
// 1/3 of the lifetime. Renewal is due when the remaining validity is <= T.
//
// A zero NotAfter (unknown expiry) is never due. A zero NotBefore (lifetime
// unknown) skips the fallback and uses the plain N-day rule.
func renewDue(notBefore, notAfter, now time.Time, renewBeforeDays int) bool {
	if notAfter.IsZero() {
		return false
	}
	threshold := time.Duration(renewBeforeDays) * 24 * time.Hour
	if !notBefore.IsZero() && notAfter.After(notBefore) {
		lifetime := notAfter.Sub(notBefore)
		if threshold > (2*lifetime)/3 {
			threshold = lifetime / 3
		}
	}
	return notAfter.Sub(now) <= threshold
}

// ScanDueRenewals enqueues a cert_renew task for every active, auto-renew
// certificate whose validity has crossed the renewal threshold. Called by the
// background renewal loop; the heavy ACME work runs in ProcessDueTasks.
func (s *Service) ScanDueRenewals(ctx context.Context) error {
	certs, err := s.certs.ListByStatus(ctx, domain.CertStatusActive)
	if err != nil {
		return err
	}
	threshold := s.renewThreshold()
	now := time.Now()
	for _, c := range certs {
		if !c.AutoRenew || c.NotAfter == nil {
			continue
		}
		nb := time.Time{}
		if c.NotBefore != nil {
			nb = *c.NotBefore
		}
		if renewDue(nb, *c.NotAfter, now, threshold) {
			if err := s.enqueueCertTask(ctx, domain.SyncTaskCertRenew, c.ID, "renew certificate "+c.Name); err != nil {
				log.Warn("enqueue cert renew", "cert_id", c.ID, "err", err)
			}
		}
	}
	return nil
}
