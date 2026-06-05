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

	// Failed certs retry on the same cadence: re-enqueue each auto-renew failed
	// cert so a transient blip or a just-fixed config (e.g. corrected DNS creds)
	// gets another attempt at the next check interval instead of staying dead
	// until a manual retry. enqueueCertTask dedups, so a cert already queued or
	// running isn't piled on. A cert that ever obtained a leaf renews; one that
	// never issued (re)issues.
	failed, err := s.certs.ListByStatus(ctx, domain.CertStatusFailed)
	if err != nil {
		return err
	}
	for _, c := range failed {
		if !c.AutoRenew {
			continue
		}
		typ, summary := domain.SyncTaskCertIssue, "retry issue certificate "+c.Name
		if c.CertPEM != "" {
			typ, summary = domain.SyncTaskCertRenew, "retry renew certificate "+c.Name
		}
		if err := s.enqueueCertTask(ctx, typ, c.ID, summary); err != nil {
			log.Warn("enqueue cert retry", "cert_id", c.ID, "err", err)
		}
	}
	return nil
}
