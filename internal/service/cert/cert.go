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
	// A few quick retries absorb a transient blip (DNS propagation hiccup, ACME
	// server 5xx); after that the cert is marked failed and the renewal scan
	// re-enqueues it at the next check interval — so the effective retry cadence
	// for a persistently-failing cert is the admin's check interval, not a fast
	// loop that would burn the ACME failed-validation rate limit. Deploy
	// resilience is unaffected: the inline push runs as a separate node sync-task
	// with its own retry budget.
	maxCertTaskAttempts = 3
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
	events   ports.CertEventRepo // optional cert activity log

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
	renewBeforeDays int,
) *Service {
	return &Service{
		certs: certs, dns: dns, accounts: accounts, issuer: issuer,
		nodes: nodes, tasks: tasks, pusher: pusher,
		renewBeforeDays: renewBeforeDays,
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

// SetEventRepo wires the optional cert activity log in (late-bound like SetAlerter).
func (s *Service) SetEventRepo(r ports.CertEventRepo) { s.events = r }

// recordCertEvent appends one terminal issue/renew outcome to the activity log.
// Best-effort — a logging failure never affects the issuance result. Deploy is
// NOT recorded here (it runs as a node sync-task, visible on the Sync Tasks page).
func (s *Service) recordCertEvent(ctx context.Context, certID int64, taskType domain.SyncTaskType, success bool, message string) {
	if s.events == nil {
		return
	}
	kind := domain.CertEventRenew
	if taskType == domain.SyncTaskCertIssue {
		kind = domain.CertEventIssue
	}
	name := ""
	if c, err := s.certs.GetByID(ctx, certID); err == nil && c != nil {
		name = c.Name
	}
	if err := s.events.Create(ctx, &domain.CertEvent{
		CertID: certID, CertName: name, Kind: kind, Success: success, Message: message,
	}); err != nil {
		log.Warn("cert event record", "cert_id", certID, "err", err)
	}
}

// ListEvents returns the cert activity log (newest first) plus the total count.
func (s *Service) ListEvents(ctx context.Context, limit, offset int) ([]*domain.CertEvent, int64, error) {
	if s.events == nil {
		return nil, 0, nil
	}
	return s.events.ListPaged(ctx, limit, offset)
}

// ActiveTask returns the in-flight issue/renew sync-task for a cert (drives the
// detail view's "in progress" indicator), or (nil, nil) when none is queued.
func (s *Service) ActiveTask(ctx context.Context, certID int64) (*domain.SyncTask, error) {
	for _, typ := range []domain.SyncTaskType{domain.SyncTaskCertIssue, domain.SyncTaskCertRenew} {
		t, err := s.tasks.GetActiveByTarget(ctx, typ, certTargetType, certID)
		if err == nil {
			return t, nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
	}
	return nil, nil
}

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

// CreateCert persists a pending certificate and enqueues its first issuance. An
// ACME account is required (multi-account: the cert issues under the chosen CA
// account) and must exist before we enqueue an issuance that would just fail.
func (s *Service) CreateCert(ctx context.Context, c *domain.TLSCertificate) error {
	if c.ACMEAccountID == 0 {
		return fmt.Errorf("%w: an ACME account is required", domain.ErrValidation)
	}
	if _, err := s.accounts.GetByID(ctx, c.ACMEAccountID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: acme account not found", domain.ErrValidation)
		}
		return err
	}
	c.Status = domain.CertStatusPending
	c.CertPEM, c.KeyPEM, c.Fingerprint, c.LastError = "", "", "", ""
	if err := s.certs.Create(ctx, c); err != nil {
		return err
	}
	return s.enqueueCertTask(ctx, domain.SyncTaskCertIssue, c.ID, "issue certificate "+c.Name)
}

// ---- ACME account CRUD ----

func (s *Service) ListACMEAccounts(ctx context.Context) ([]*domain.ACMEAccount, error) {
	return s.accounts.List(ctx)
}

func (s *Service) GetACMEAccount(ctx context.Context, id int64) (*domain.ACMEAccount, error) {
	return s.accounts.GetByID(ctx, id)
}

func (s *Service) CreateACMEAccount(ctx context.Context, a *domain.ACMEAccount) error {
	if err := validateACMEAccount(a); err != nil {
		return err
	}
	dup, err := s.acmeAccountByIdentity(ctx, a.Email, a.Directory)
	if err != nil {
		return err
	}
	if dup != nil {
		return fmt.Errorf("%w: an ACME account for this email + directory already exists", domain.ErrAlreadyExists)
	}
	return s.accounts.Create(ctx, a)
}

// UpdateACMEAccount saves config edits. The EAB HMAC is write-only (blank = keep
// the stored secret, like DNS creds / SMTP password). If the registered identity
// (email / directory / EAB) changes, the stored account key + registration no
// longer match the CA account, so they're cleared to force a fresh registration.
func (s *Service) UpdateACMEAccount(ctx context.Context, a *domain.ACMEAccount) error {
	existing, err := s.accounts.GetByID(ctx, a.ID)
	if err != nil {
		return err
	}
	if a.EABHMACKey == "" {
		a.EABHMACKey = existing.EABHMACKey // keep stored secret on a blank re-save
	}
	// Validate AFTER the merge so a blank-HMAC re-save of an existing EAB account
	// (the admin didn't re-enter the masked secret) doesn't fail the all-or-nothing check.
	if err := validateACMEAccount(a); err != nil {
		return err
	}
	if dup, derr := s.acmeAccountByIdentity(ctx, a.Email, a.Directory); derr != nil {
		return derr
	} else if dup != nil && dup.ID != a.ID {
		return fmt.Errorf("%w: another ACME account for this email + directory already exists", domain.ErrAlreadyExists)
	}
	if err := s.accounts.Update(ctx, a); err != nil {
		return err
	}
	if existing.Email != a.Email || existing.Directory != a.Directory ||
		existing.EABKeyID != a.EABKeyID || existing.EABHMACKey != a.EABHMACKey {
		if err := s.accounts.ClearRegistration(ctx, a.ID); err != nil {
			return err
		}
	}
	return nil
}

// DeleteACMEAccount refuses while any certificate still issues under it — a
// dangling acme_account_id would leave that cert unrenewable. Mirrors the
// DeleteCert / xui_panel delete guards.
func (s *Service) DeleteACMEAccount(ctx context.Context, id int64) error {
	certs, err := s.certs.List(ctx)
	if err != nil {
		return err
	}
	bound := 0
	for _, c := range certs {
		if c.ACMEAccountID == id {
			bound++
		}
	}
	if bound > 0 {
		return fmt.Errorf("%w: ACME account still used by %d certificate(s); change their account first", domain.ErrValidation, bound)
	}
	return s.accounts.Delete(ctx, id)
}

// acmeAccountByIdentity returns the account matching (email, directory), or nil.
func (s *Service) acmeAccountByIdentity(ctx context.Context, email, directory string) (*domain.ACMEAccount, error) {
	all, err := s.accounts.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range all {
		if a.Email == email && a.Directory == directory {
			return a, nil
		}
	}
	return nil, nil
}

func validateACMEAccount(a *domain.ACMEAccount) error {
	if strings.TrimSpace(a.Email) == "" || strings.TrimSpace(a.Directory) == "" {
		return fmt.Errorf("%w: ACME account email and directory URL are required", domain.ErrValidation)
	}
	if !strings.HasPrefix(a.Directory, "http://") && !strings.HasPrefix(a.Directory, "https://") {
		return fmt.Errorf("%w: directory must be an http(s) URL", domain.ErrValidation)
	}
	// EAB is all-or-nothing: a kid without an HMAC (or vice-versa) can't register.
	if (a.EABKeyID != "") != (a.EABHMACKey != "") {
		return fmt.Errorf("%w: EAB requires both a Key ID and an HMAC key", domain.ErrValidation)
	}
	return nil
}

// UpdateCert edits a certificate's config (name / domains / ACME account / DNS
// credential / auto-renew) WITHOUT touching the already-issued PEM. An ACME
// account is required and must exist. If the SAN list changes, the issued cert no
// longer matches, so it's flipped to pending and a re-issue is enqueued.
func (s *Service) UpdateCert(ctx context.Context, in *domain.TLSCertificate) error {
	if in.ACMEAccountID == 0 {
		return fmt.Errorf("%w: an ACME account is required", domain.ErrValidation)
	}
	if _, err := s.accounts.GetByID(ctx, in.ACMEAccountID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: acme account not found", domain.ErrValidation)
		}
		return err
	}
	existing, err := s.certs.GetByID(ctx, in.ID)
	if err != nil {
		return err
	}
	domainsChanged := !sameDomains(existing.Domains, in.Domains)
	existing.Name = in.Name
	existing.Domains = in.Domains
	existing.ACMEAccountID = in.ACMEAccountID
	existing.DNSCredentialID = in.DNSCredentialID
	existing.AutoRenew = in.AutoRenew
	if domainsChanged {
		// The issued cert's SAN list no longer matches — re-issue under the
		// (possibly new) account. Changing only the account/cred/name does NOT
		// re-issue: the current cert stays valid; the next renewal uses the new
		// settings (or the admin can hit "renew" to re-issue now).
		existing.Status = domain.CertStatusPending
		existing.LastError = ""
	}
	if err := s.certs.Update(ctx, existing); err != nil {
		return err
	}
	if domainsChanged {
		return s.enqueueCertTask(ctx, domain.SyncTaskCertIssue, existing.ID, "re-issue certificate "+existing.Name)
	}
	return nil
}

// sameDomains reports whether two SAN lists hold the same names (order-insensitive).
func sameDomains(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, d := range a {
		seen[d]++
	}
	for _, d := range b {
		seen[d]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
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
				s.recordCertEvent(ctx, task.TargetID, task.Type, false, err.Error())
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
		s.recordCertEvent(ctx, task.TargetID, task.Type, true, "")
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
	acct, err := s.accounts.GetByID(ctx, cert.ACMEAccountID)
	if errors.Is(err, domain.ErrNotFound) {
		return ports.ACMERequest{}, fmt.Errorf("%w: acme account %d not found", errPermanent, cert.ACMEAccountID)
	}
	if err != nil {
		return ports.ACMERequest{}, err
	}
	cred, err := s.dns.GetByID(ctx, cert.DNSCredentialID)
	if errors.Is(err, domain.ErrNotFound) {
		return ports.ACMERequest{}, fmt.Errorf("%w: dns credential %d not found", errPermanent, cert.DNSCredentialID)
	}
	if err != nil {
		return ports.ACMERequest{}, err
	}
	return ports.ACMERequest{
		Domains:          cert.Domains,
		Email:            acct.Email,
		DirectoryURL:     acct.Directory,
		EABKeyID:         acct.EABKeyID,
		EABHMACKey:       acct.EABHMACKey,
		KeyType:          acct.KeyType,
		DNSProvider:      cred.Provider,
		DNSCredentials:   cred.Credentials,
		AccountKeyPEM:    acct.AccountKey,
		RegistrationJSON: acct.Registration,
	}, nil
}

// saveAccount writes the (possibly newly registered) account key + registration
// back to the cert's selected ACME account, so the account is reused across
// future issuances (staying under the CA's rate limits).
func (s *Service) saveAccount(ctx context.Context, cert *domain.TLSCertificate, res ports.ACMEResult) error {
	if res.AccountKeyPEM == "" || cert.ACMEAccountID == 0 {
		return nil
	}
	return s.accounts.UpdateRegistration(ctx, cert.ACMEAccountID, res.AccountKeyPEM, res.RegistrationJSON)
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
