package cert

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const (
	maxCertTaskAttempts = 100
	certTaskBackoff     = time.Minute
	certTargetType      = "cert"
)

// errPermanent marks a non-retryable issuance failure (missing/invalid config)
// so the task processor fails fast instead of burning the retry budget.
var errPermanent = errors.New("permanent certificate error")

// NodeConfigPusher schedules a retryable push of a node's local inbound config
// to 3X-UI. Implemented by node.Service.EnqueueConfigPush — the cert service
// deploys by writing the inline cert into the node snapshot and enqueueing this
// push, so deploy retries never re-issue the certificate.
type NodeConfigPusher interface {
	EnqueueConfigPush(ctx context.Context, nodeID int64) error
}

// AdminAlerter emails admins about certificate failures. Implemented by
// mailer.Service.AlertAdmins; optional (nil-tolerant) so the cert service runs
// fine before/without mail configured.
type AdminAlerter interface {
	AlertAdmins(ctx context.Context, kind domain.MailReminderKind, windowKey, subject, body string) (int, error)
}

// Service owns the PSP-managed certificate lifecycle: ACME issuance/renewal via
// the async sync-task queue, and inline deploy into bound node inbounds. It
// holds NO XUIPool — the actual push is delegated to NodeConfigPusher.
type Service struct {
	certs    ports.CertificateRepo
	dns      ports.DNSCredentialRepo
	accounts ports.ACMEAccountRepo
	issuer   ports.ACMEIssuer
	nodes    ports.NodeRepo
	tasks    ports.SyncTaskRepo
	pusher   NodeConfigPusher
	alerter  AdminAlerter

	directoryURL string // default ACME directory (LE prod/staging)
	email        string // ACME account contact

	mu              sync.RWMutex
	renewBeforeDays int // renewal threshold N (admin-configurable; the worker refreshes it)
}

func New(
	certs ports.CertificateRepo,
	dns ports.DNSCredentialRepo,
	accounts ports.ACMEAccountRepo,
	issuer ports.ACMEIssuer,
	nodes ports.NodeRepo,
	tasks ports.SyncTaskRepo,
	pusher NodeConfigPusher,
	directoryURL, email string,
	renewBeforeDays int,
) *Service {
	return &Service{
		certs: certs, dns: dns, accounts: accounts, issuer: issuer,
		nodes: nodes, tasks: tasks, pusher: pusher,
		directoryURL: directoryURL, email: email, renewBeforeDays: renewBeforeDays,
	}
}

// SetRenewBeforeDays lets the renewal worker push a fresh admin setting in
// without a restart.
func (s *Service) SetRenewBeforeDays(d int) {
	s.mu.Lock()
	s.renewBeforeDays = d
	s.mu.Unlock()
}

// SetAlerter wires the optional admin-failure mailer in (late-bound like the
// other cross-service notifiers in app.go).
func (s *Service) SetAlerter(a AdminAlerter) { s.alerter = a }

func (s *Service) renewThreshold() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.renewBeforeDays
}

// ---- certificate CRUD ----

func (s *Service) ListCerts(ctx context.Context) ([]*domain.TLSCertificate, error) {
	return s.certs.List(ctx)
}

func (s *Service) GetCert(ctx context.Context, id int64) (*domain.TLSCertificate, error) {
	return s.certs.GetByID(ctx, id)
}

// CreateCert persists a pending certificate and enqueues its first issuance.
func (s *Service) CreateCert(ctx context.Context, c *domain.TLSCertificate) error {
	c.Status = domain.CertStatusPending
	c.CertPEM, c.KeyPEM, c.Fingerprint, c.LastError = "", "", "", ""
	if err := s.certs.Create(ctx, c); err != nil {
		return err
	}
	return s.enqueueCertTask(ctx, domain.SyncTaskCertIssue, c.ID, "issue certificate "+c.Name)
}

// DeleteCert refuses while any node still references the certificate — a
// dangling cert_id would leave that node's TLS deploy unsatisfiable. Mirrors the
// xui_panel delete guard.
func (s *Service) DeleteCert(ctx context.Context, id int64) error {
	bound, err := s.nodes.ListByCertID(ctx, id)
	if err != nil {
		return err
	}
	if len(bound) > 0 {
		return fmt.Errorf("%w: certificate still bound to %d node(s); change their certificate source first", domain.ErrValidation, len(bound))
	}
	return s.certs.Delete(ctx, id)
}

// ManualRenew enqueues an immediate renewal of one certificate (admin button).
func (s *Service) ManualRenew(ctx context.Context, id int64) error {
	if _, err := s.certs.GetByID(ctx, id); err != nil {
		return err
	}
	return s.enqueueCertTask(ctx, domain.SyncTaskCertRenew, id, "renew certificate")
}

// ---- DNS credential CRUD ----

func (s *Service) ListDNSCreds(ctx context.Context) ([]*domain.DNSCredential, error) {
	return s.dns.List(ctx)
}
func (s *Service) CreateDNSCred(ctx context.Context, c *domain.DNSCredential) error {
	return s.dns.Create(ctx, c)
}
func (s *Service) UpdateDNSCred(ctx context.Context, c *domain.DNSCredential) error {
	// Secret values are write-only in the UI: the edit form prefills key names
	// with BLANK values, so an admin who changes only one secret would otherwise
	// blank out every untouched one. Merge a blank value as "keep the stored
	// secret" (same re-enter-to-change semantics as the SMTP password / GeoIP
	// token), so renewals don't break when an admin tweaks one field.
	existing, err := s.dns.GetByID(ctx, c.ID)
	if err != nil {
		return err
	}
	if c.Credentials == nil {
		c.Credentials = map[string]string{}
	}
	for k, v := range c.Credentials {
		if v == "" {
			if prev, ok := existing.Credentials[k]; ok {
				c.Credentials[k] = prev
			}
		}
	}
	return s.dns.Update(ctx, c)
}
func (s *Service) DeleteDNSCred(ctx context.Context, id int64) error {
	return s.dns.Delete(ctx, id)
}

// ---- async issuance/renewal (sync-task queue) ----

func (s *Service) enqueueCertTask(ctx context.Context, typ domain.SyncTaskType, certID int64, summary string) error {
	if s.tasks == nil {
		return nil
	}
	if _, err := s.tasks.GetActiveByTarget(ctx, typ, certTargetType, certID); err == nil {
		return nil // dedup: already queued
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return s.tasks.Create(ctx, &domain.SyncTask{
		Type:       typ,
		Status:     domain.SyncTaskPending,
		TargetType: certTargetType,
		TargetID:   certID,
		Summary:    summary,
		NextRunAt:  time.Now(),
	})
}

// ProcessDueTasks drives the cert_issue / cert_renew tasks. Mirrors the
// user/node task processors: claim, run, then succeed / retry (transient) /
// fail-fast (permanent or attempt cap). Wired into runSyncTaskLoop.
func (s *Service) ProcessDueTasks(ctx context.Context, limit int) error {
	tasks, err := s.tasks.ListDue(ctx, time.Now(), limit)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.Type != domain.SyncTaskCertIssue && task.Type != domain.SyncTaskCertRenew {
			continue
		}
		claimed, err := s.tasks.MarkRunning(ctx, task.ID)
		if err != nil {
			log.Warn("cert task mark-running", "task_id", task.ID, "err", err)
			continue
		}
		if !claimed {
			continue
		}
		if err := s.runCertTask(ctx, task); err != nil {
			if isPermanentCertError(err) || task.Attempts+1 >= maxCertTaskAttempts {
				log.Warn("cert task gave up", "task_id", task.ID, "cert_id", task.TargetID, "attempts", task.Attempts+1, "err", err.Error())
				s.markCertFailed(ctx, task.TargetID, err)
				if cerr := s.tasks.Cancel(ctx, task.ID); cerr != nil {
					log.Warn("cert task cancel", "task_id", task.ID, "err", cerr)
				}
				continue
			}
			next := time.Now().Add(certTaskBackoff)
			if merr := s.tasks.MarkRetry(ctx, task.ID, err.Error(), next); merr != nil {
				log.Warn("cert task mark-retry", "task_id", task.ID, "err", merr)
			}
			continue
		}
		if err := s.tasks.MarkSucceeded(ctx, task.ID); err != nil {
			log.Warn("cert task mark-succeeded", "task_id", task.ID, "err", err)
		}
	}
	return nil
}

func (s *Service) runCertTask(ctx context.Context, task *domain.SyncTask) error {
	cert, err := s.certs.GetByID(ctx, task.TargetID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil // deleted between enqueue and run — nothing to do
	}
	if err != nil {
		return err
	}
	// Only obtain when genuinely needed, so a retry caused by a DEPLOY failure
	// (after issuance already succeeded) re-deploys without re-issuing — never
	// burning the ACME rate limit.
	if s.shouldObtain(cert, task.Type) {
		req, err := s.buildACMERequest(ctx, cert)
		if err != nil {
			return err
		}
		res, err := s.issuer.Obtain(ctx, req)
		if err != nil {
			return fmt.Errorf("acme obtain: %w", err)
		}
		if err := s.saveAccount(ctx, cert, res); err != nil {
			log.Warn("cert save acme account", "cert_id", cert.ID, "err", err)
		}
		cert.CertPEM, cert.KeyPEM, cert.Fingerprint = res.CertPEM, res.KeyPEM, res.Fingerprint
		nb, na := res.NotBefore, res.NotAfter
		cert.NotBefore, cert.NotAfter = &nb, &na
		cert.Status, cert.LastError = domain.CertStatusActive, ""
		if err := s.certs.UpdateIssued(ctx, cert); err != nil {
			return err
		}
	}
	return s.deployToBoundNodes(ctx, cert)
}

// shouldObtain decides whether to run ACME for this task, vs reuse the
// already-issued certificate and only (re)deploy.
func (s *Service) shouldObtain(cert *domain.TLSCertificate, taskType domain.SyncTaskType) bool {
	if cert.Status != domain.CertStatusActive || cert.CertPEM == "" || cert.NotAfter == nil {
		return true // never successfully issued
	}
	if taskType == domain.SyncTaskCertRenew {
		// Obtain unless the cert looks freshly renewed (still has >2/3 of its
		// lifetime left). This is deliberately INDEPENDENT of the admin-mutable
		// renew threshold: a cert_renew task is enqueued only after
		// ScanDueRenewals already judged it due, so re-reading the threshold here
		// would just open a race (the admin can change it between enqueue and
		// run). The lifetime-fraction check makes a deploy-failure retry skip the
		// redundant ACME call (after a successful renewal NotAfter jumps out).
		if cert.NotBefore != nil && cert.NotAfter.After(*cert.NotBefore) {
			lifetime := cert.NotAfter.Sub(*cert.NotBefore)
			return cert.NotAfter.Sub(time.Now()) <= (2*lifetime)/3
		}
		return true // lifetime unknown — trust the scan's due decision
	}
	return false // cert_issue on an already-active cert — don't re-obtain
}

func (s *Service) buildACMERequest(ctx context.Context, cert *domain.TLSCertificate) (ports.ACMERequest, error) {
	cred, err := s.dns.GetByID(ctx, cert.DNSCredentialID)
	if errors.Is(err, domain.ErrNotFound) {
		return ports.ACMERequest{}, fmt.Errorf("%w: dns credential %d not found", errPermanent, cert.DNSCredentialID)
	}
	if err != nil {
		return ports.ACMERequest{}, err
	}
	req := ports.ACMERequest{
		Domains:        cert.Domains,
		Email:          s.email,
		DirectoryURL:   s.directoryURL,
		DNSProvider:    cred.Provider,
		DNSCredentials: cred.Credentials,
	}
	acct, err := s.accounts.GetByEmailDirectory(ctx, s.email, s.directoryURL)
	if err != nil {
		return ports.ACMERequest{}, err
	}
	if acct != nil {
		req.AccountKeyPEM = acct.AccountKey
		req.RegistrationJSON = acct.Registration
	}
	return req, nil
}

func (s *Service) saveAccount(ctx context.Context, cert *domain.TLSCertificate, res ports.ACMEResult) error {
	if res.AccountKeyPEM == "" {
		return nil
	}
	acct := &domain.ACMEAccount{
		Email:        s.email,
		Directory:    s.directoryURL,
		AccountKey:   res.AccountKeyPEM,
		Registration: res.RegistrationJSON,
	}
	existing, err := s.accounts.GetByEmailDirectory(ctx, s.email, s.directoryURL)
	if err != nil {
		return err
	}
	if existing != nil {
		acct.ID = existing.ID
	}
	if err := s.accounts.Save(ctx, acct); err != nil {
		return err
	}
	cert.ACMEAccountID = acct.ID
	return nil
}

// markCertFailed flips status→failed + records LastError, WITHOUT clobbering the
// stored cert/key (it re-reads then writes them back through UpdateIssued).
func (s *Service) markCertFailed(ctx context.Context, certID int64, cause error) {
	c, err := s.certs.GetByID(ctx, certID)
	if err != nil {
		return
	}
	c.Status = domain.CertStatusFailed
	c.LastError = cause.Error()
	if err := s.certs.UpdateIssued(ctx, c); err != nil {
		log.Warn("cert mark-failed", "cert_id", certID, "err", err)
	}
	if s.alerter == nil {
		return
	}
	subject := fmt.Sprintf("PSP: certificate %q failed", c.Name)
	// Imminent-expiry escalation: a failing cert that's also near expiry is a
	// higher-severity event than a routine failure on a fresh cert.
	if c.NotAfter != nil && time.Until(*c.NotAfter) < time.Duration(s.renewThreshold())*24*time.Hour {
		subject = fmt.Sprintf("⚠️ PSP: certificate %q is FAILING and near expiry", c.Name)
	}
	body := fmt.Sprintf("Certificate %q (%s) failed its last issuance/renewal:\n\n%s",
		c.Name, strings.Join(c.Domains, ", "), cause.Error())
	// Dedup per cert per day so a persistent failure alerts at most once a day.
	windowKey := fmt.Sprintf("cert:%d:%s", c.ID, time.Now().Format("2006-01-02"))
	if _, err := s.alerter.AlertAdmins(ctx, domain.MailReminderCertFailure, windowKey, subject, body); err != nil {
		log.Warn("cert alert admins", "cert_id", certID, "err", err)
	}
}

// SetNodeCertSource records a node's certificate source (manual / from_panel /
// psp_managed). For psp_managed with an already-active cert it deploys
// immediately, so binding an existing cert to a node takes effect without
// waiting for the next renewal. Non-managed sources clear cert_id.
func (s *Service) SetNodeCertSource(ctx context.Context, nodeID int64, source domain.CertSource, certID int64) error {
	if source != domain.CertSourceManaged {
		certID = 0
	}
	// Validate the node (and, for psp_managed, the cert) exist BEFORE writing the
	// binding, so a bad id can't leave a dangling cert_source/cert_id on a row.
	n, err := s.nodes.GetByID(ctx, nodeID)
	if err != nil {
		return err
	}
	var cert *domain.TLSCertificate
	if source == domain.CertSourceManaged && certID != 0 {
		cert, err = s.certs.GetByID(ctx, certID)
		if err != nil {
			return err
		}
	}
	if err := s.nodes.UpdateCertBinding(ctx, nodeID, source, certID); err != nil {
		return err
	}
	// Deploy an already-active cert immediately so binding takes effect without
	// waiting for the next renewal.
	if cert != nil && cert.Status == domain.CertStatusActive {
		if err := s.DeployToNode(ctx, n, cert); err != nil && !errors.Is(err, errNotTLS) {
			return err
		}
	}
	return nil
}

func isPermanentCertError(err error) bool {
	return errors.Is(err, errPermanent) || errors.Is(err, errNotTLS)
}
