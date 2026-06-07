package cert

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// ---- fakes ----

type fakeCertRepo struct {
	seq   int64
	certs map[int64]*domain.TLSCertificate
}

func newFakeCertRepo() *fakeCertRepo { return &fakeCertRepo{certs: map[int64]*domain.TLSCertificate{}} }

func (r *fakeCertRepo) Create(_ context.Context, c *domain.TLSCertificate) error {
	r.seq++
	c.ID = r.seq
	cp := *c
	r.certs[c.ID] = &cp
	return nil
}
func (r *fakeCertRepo) Update(_ context.Context, c *domain.TLSCertificate) error {
	cp := *c
	r.certs[c.ID] = &cp
	return nil
}
func (r *fakeCertRepo) UpdateIssued(_ context.Context, c *domain.TLSCertificate) error {
	cp := *c
	r.certs[c.ID] = &cp
	return nil
}
func (r *fakeCertRepo) Delete(_ context.Context, id int64) error { delete(r.certs, id); return nil }
func (r *fakeCertRepo) GetByID(_ context.Context, id int64) (*domain.TLSCertificate, error) {
	c, ok := r.certs[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *c
	return &cp, nil
}
func (r *fakeCertRepo) List(_ context.Context) ([]*domain.TLSCertificate, error) {
	return r.byStatus(""), nil
}
func (r *fakeCertRepo) ListByStatus(_ context.Context, st domain.CertStatus) ([]*domain.TLSCertificate, error) {
	return r.byStatus(st), nil
}
func (r *fakeCertRepo) byStatus(st domain.CertStatus) []*domain.TLSCertificate {
	out := []*domain.TLSCertificate{}
	for _, c := range r.certs {
		if st == "" || c.Status == st {
			cp := *c
			out = append(out, &cp)
		}
	}
	return out
}

type fakeDNSRepo struct {
	creds       map[int64]*domain.DNSCredential
	lastUpdated *domain.DNSCredential
}

func (r *fakeDNSRepo) Create(_ context.Context, c *domain.DNSCredential) error { return nil }
func (r *fakeDNSRepo) Update(_ context.Context, c *domain.DNSCredential) error {
	r.lastUpdated = c
	return nil
}
func (r *fakeDNSRepo) Delete(_ context.Context, id int64) error                { return nil }
func (r *fakeDNSRepo) List(_ context.Context) ([]*domain.DNSCredential, error) { return nil, nil }
func (r *fakeDNSRepo) GetByID(_ context.Context, id int64) (*domain.DNSCredential, error) {
	c, ok := r.creds[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return c, nil
}

// fakeAccountRepo is a map-backed ACMEAccountRepo. Update preserves the lazy
// machine fields (account key + registration) like the real repo, so the
// service's identity-change ClearRegistration behaviour is observable.
type fakeAccountRepo struct {
	seq   int64
	accts map[int64]*domain.ACMEAccount
}

func newFakeAccountRepo(seed ...*domain.ACMEAccount) *fakeAccountRepo {
	r := &fakeAccountRepo{accts: map[int64]*domain.ACMEAccount{}}
	for _, a := range seed {
		_ = r.Create(context.Background(), a)
	}
	return r
}
func (r *fakeAccountRepo) Create(_ context.Context, a *domain.ACMEAccount) error {
	r.seq++
	a.ID = r.seq
	cp := *a
	r.accts[a.ID] = &cp
	return nil
}
func (r *fakeAccountRepo) Update(_ context.Context, a *domain.ACMEAccount) error {
	cur, ok := r.accts[a.ID]
	if !ok {
		return domain.ErrNotFound
	}
	cp := *a
	cp.AccountKey, cp.Registration = cur.AccountKey, cur.Registration // config-only update
	r.accts[a.ID] = &cp
	return nil
}
func (r *fakeAccountRepo) Delete(_ context.Context, id int64) error { delete(r.accts, id); return nil }
func (r *fakeAccountRepo) GetByID(_ context.Context, id int64) (*domain.ACMEAccount, error) {
	a, ok := r.accts[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *a
	return &cp, nil
}
func (r *fakeAccountRepo) List(_ context.Context) ([]*domain.ACMEAccount, error) {
	out := []*domain.ACMEAccount{}
	for _, a := range r.accts {
		cp := *a
		out = append(out, &cp)
	}
	return out, nil
}
func (r *fakeAccountRepo) UpdateRegistration(_ context.Context, id int64, key, reg string) error {
	if a, ok := r.accts[id]; ok {
		a.AccountKey, a.Registration = key, reg
	}
	return nil
}
func (r *fakeAccountRepo) ClearRegistration(_ context.Context, id int64) error {
	if a, ok := r.accts[id]; ok {
		a.AccountKey, a.Registration = "", ""
	}
	return nil
}

// seededAccount returns a repo with one ready-to-use account (id 1) for the
// issuance-path tests, plus that account's id.
func seededAccount() *fakeAccountRepo {
	return newFakeAccountRepo(&domain.ACMEAccount{Name: "le", Email: "admin@example.com", Directory: "https://acme.example/dir"})
}

type fakeIssuer struct {
	calls   int
	lastReq ports.ACMERequest
	result  ports.ACMEResult
	err     error
}

func (i *fakeIssuer) Obtain(_ context.Context, req ports.ACMERequest) (ports.ACMEResult, error) {
	i.calls++
	i.lastReq = req
	return i.result, i.err
}

type fakeNodeRepo struct {
	ports.NodeRepo
	byCert        map[int64][]*domain.Node
	byID          map[int64]*domain.Node
	configUpdates int
	lastSource    domain.CertSource
	lastCertID    int64
}

func (r *fakeNodeRepo) ListByCertID(_ context.Context, certID int64) ([]*domain.Node, error) {
	return r.byCert[certID], nil
}
func (r *fakeNodeRepo) UpdateInboundConfig(_ context.Context, _ *domain.Node) error {
	r.configUpdates++
	return nil
}
func (r *fakeNodeRepo) GetByID(_ context.Context, id int64) (*domain.Node, error) {
	n, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *n
	return &cp, nil
}
func (r *fakeNodeRepo) UpdateCertBinding(_ context.Context, _ int64, source domain.CertSource, certID int64) error {
	r.lastSource = source
	r.lastCertID = certID
	return nil
}

func TestSetNodeCertSourceManagedDeploysActiveCert(t *testing.T) {
	certs := newFakeCertRepo()
	na := time.Now().Add(80 * 24 * time.Hour)
	nb := time.Now()
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"x"}, Status: domain.CertStatusActive, CertPEM: "CERT", KeyPEM: "KEY", NotAfter: &na, NotBefore: &nb}
	certs.Create(context.Background(), c)
	node := &domain.Node{ID: 11, PanelID: 1, InboundID: 2, StreamSettings: `{"security":"tls","tlsSettings":{}}`}
	nodes := &fakeNodeRepo{byID: map[int64]*domain.Node{11: node}}
	pusher := &fakePusher{}
	s := newTestService(certs, &fakeDNSRepo{}, newFakeAccountRepo(), &fakeIssuer{}, nodes, &fakeTaskRepo{}, pusher)

	if err := s.SetNodeCertSource(context.Background(), 11, domain.CertSourceManaged, c.ID); err != nil {
		t.Fatal(err)
	}
	if nodes.lastSource != domain.CertSourceManaged || nodes.lastCertID != c.ID {
		t.Fatalf("binding not set: source=%q id=%d", nodes.lastSource, nodes.lastCertID)
	}
	if len(pusher.pushed) != 1 {
		t.Fatalf("an active cert should deploy on bind, pushed=%v", pusher.pushed)
	}
}

func TestSetNodeCertSourceManualClearsAndDoesNotDeploy(t *testing.T) {
	node := &domain.Node{ID: 11, PanelID: 1, InboundID: 2, StreamSettings: `{"security":"tls","tlsSettings":{}}`}
	nodes := &fakeNodeRepo{byID: map[int64]*domain.Node{11: node}}
	pusher := &fakePusher{}
	s := newTestService(newFakeCertRepo(), &fakeDNSRepo{}, newFakeAccountRepo(), &fakeIssuer{}, nodes, &fakeTaskRepo{}, pusher)

	if err := s.SetNodeCertSource(context.Background(), 11, domain.CertSourceManual, 5); err != nil {
		t.Fatal(err)
	}
	if nodes.lastSource != domain.CertSourceManual || nodes.lastCertID != 0 {
		t.Fatalf("manual must clear cert_id: source=%q id=%d", nodes.lastSource, nodes.lastCertID)
	}
	if len(pusher.pushed) != 0 {
		t.Fatal("manual must not deploy")
	}
}

type fakeTaskRepo struct {
	ports.SyncTaskRepo
	created   []*domain.SyncTask
	due       []*domain.SyncTask
	succeeded []int64
	canceled  []int64
	retried   []int64
}

func (r *fakeTaskRepo) GetActiveByTarget(_ context.Context, _ domain.SyncTaskType, _ string, _ int64) (*domain.SyncTask, error) {
	return nil, domain.ErrNotFound // never deduped in these tests
}
func (r *fakeTaskRepo) Create(_ context.Context, t *domain.SyncTask) error {
	t.ID = int64(len(r.created) + 1)
	r.created = append(r.created, t)
	return nil
}
func (r *fakeTaskRepo) ListDue(_ context.Context, _ time.Time, _ int) ([]*domain.SyncTask, error) {
	return r.due, nil
}
func (r *fakeTaskRepo) MarkRunning(_ context.Context, _ int64) (bool, error)               { return true, nil }
func (r *fakeTaskRepo) MarkSucceeded(_ context.Context, id int64) error                    { r.succeeded = append(r.succeeded, id); return nil }
func (r *fakeTaskRepo) MarkRetry(_ context.Context, id int64, _ string, _ time.Time) error { r.retried = append(r.retried, id); return nil }
func (r *fakeTaskRepo) Cancel(_ context.Context, id int64) error                           { r.canceled = append(r.canceled, id); return nil }

type fakePusher struct{ pushed []int64 }

func (p *fakePusher) EnqueueConfigPush(_ context.Context, nodeID int64) error {
	p.pushed = append(p.pushed, nodeID)
	return nil
}

func newTestService(certs *fakeCertRepo, dns *fakeDNSRepo, acct *fakeAccountRepo, issuer *fakeIssuer, nodes *fakeNodeRepo, tasks *fakeTaskRepo, pusher *fakePusher) *Service {
	return New(certs, dns, acct, issuer, nodes, tasks, pusher, 30)
}

// ---- tests ----

func TestCreateCertEnqueuesIssueTask(t *testing.T) {
	certs := newFakeCertRepo()
	tasks := &fakeTaskRepo{}
	s := newTestService(certs, &fakeDNSRepo{}, seededAccount(), &fakeIssuer{}, &fakeNodeRepo{}, tasks, &fakePusher{})

	c := &domain.TLSCertificate{Name: "w", Domains: []string{"*.example.com"}, ACMEAccountID: 1}
	if err := s.CreateCert(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if c.ID == 0 || certs.certs[c.ID].Status != domain.CertStatusPending {
		t.Fatalf("cert not created pending: %#v", certs.certs[c.ID])
	}
	if len(tasks.created) != 1 || tasks.created[0].Type != domain.SyncTaskCertIssue || tasks.created[0].TargetID != c.ID {
		t.Fatalf("issue task not enqueued: %#v", tasks.created)
	}
}

// An ACME account is required: creating a cert without one (or with one that
// doesn't exist) must fail validation before anything is enqueued.
func TestCreateCertRequiresACMEAccount(t *testing.T) {
	certs := newFakeCertRepo()
	tasks := &fakeTaskRepo{}
	s := newTestService(certs, &fakeDNSRepo{}, newFakeAccountRepo(), &fakeIssuer{}, &fakeNodeRepo{}, tasks, &fakePusher{})

	if err := s.CreateCert(context.Background(), &domain.TLSCertificate{Name: "w", Domains: []string{"x"}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("missing acme account must be a validation error, got %v", err)
	}
	if err := s.CreateCert(context.Background(), &domain.TLSCertificate{Name: "w", Domains: []string{"x"}, ACMEAccountID: 99}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("nonexistent acme account must be a validation error, got %v", err)
	}
	if len(tasks.created) != 0 {
		t.Fatalf("nothing should be enqueued on validation failure: %#v", tasks.created)
	}
}

// Reassigning a cert's ACME account (e.g. a cert created before multi-account,
// acme_account_id=0) must preserve the issued PEM and NOT re-issue.
func TestUpdateCertReassignAccountKeepsIssuedNoReissue(t *testing.T) {
	certs := newFakeCertRepo()
	na := time.Now().Add(80 * 24 * time.Hour)
	nb := time.Now()
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"x.com"}, ACMEAccountID: 0, Status: domain.CertStatusActive, CertPEM: "CERT", KeyPEM: "KEY", NotAfter: &na, NotBefore: &nb}
	certs.Create(context.Background(), c)
	tasks := &fakeTaskRepo{}
	s := newTestService(certs, &fakeDNSRepo{}, seededAccount(), &fakeIssuer{}, &fakeNodeRepo{}, tasks, &fakePusher{})

	if err := s.UpdateCert(context.Background(), &domain.TLSCertificate{ID: c.ID, Name: "w2", Domains: []string{"x.com"}, ACMEAccountID: 1, AutoRenew: true}); err != nil {
		t.Fatal(err)
	}
	got := certs.certs[c.ID]
	if got.ACMEAccountID != 1 || got.Name != "w2" {
		t.Fatalf("config not updated: %#v", got)
	}
	if got.CertPEM != "CERT" || got.Status != domain.CertStatusActive {
		t.Fatalf("must preserve issued PEM/status on a config-only edit: %#v", got)
	}
	if len(tasks.created) != 0 {
		t.Fatalf("account-only change must not re-issue: %#v", tasks.created)
	}
}

// Changing the SAN list must flip the cert to pending and enqueue a re-issue.
func TestUpdateCertReissuesOnDomainsChange(t *testing.T) {
	certs := newFakeCertRepo()
	na := time.Now().Add(80 * 24 * time.Hour)
	nb := time.Now()
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"x.com"}, ACMEAccountID: 1, Status: domain.CertStatusActive, CertPEM: "CERT", NotAfter: &na, NotBefore: &nb}
	certs.Create(context.Background(), c)
	tasks := &fakeTaskRepo{}
	s := newTestService(certs, &fakeDNSRepo{}, seededAccount(), &fakeIssuer{}, &fakeNodeRepo{}, tasks, &fakePusher{})

	if err := s.UpdateCert(context.Background(), &domain.TLSCertificate{ID: c.ID, Name: "w", Domains: []string{"x.com", "y.com"}, ACMEAccountID: 1, AutoRenew: true}); err != nil {
		t.Fatal(err)
	}
	got := certs.certs[c.ID]
	if got.Status != domain.CertStatusPending {
		t.Fatalf("domains change must flip to pending, got %s", got.Status)
	}
	if len(tasks.created) != 1 || tasks.created[0].Type != domain.SyncTaskCertIssue {
		t.Fatalf("domains change must enqueue a re-issue: %#v", tasks.created)
	}
}

func TestUpdateCertRequiresACMEAccount(t *testing.T) {
	certs := newFakeCertRepo()
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"x"}, ACMEAccountID: 1, Status: domain.CertStatusActive}
	certs.Create(context.Background(), c)
	s := newTestService(certs, &fakeDNSRepo{}, seededAccount(), &fakeIssuer{}, &fakeNodeRepo{}, &fakeTaskRepo{}, &fakePusher{})
	if err := s.UpdateCert(context.Background(), &domain.TLSCertificate{ID: c.ID, Name: "w", Domains: []string{"x"}, ACMEAccountID: 0}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("update without an acme account must be a validation error, got %v", err)
	}
}

func TestRunCertTaskIssuesPersistsAndDeploys(t *testing.T) {
	certs := newFakeCertRepo()
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"*.example.com"}, ACMEAccountID: 1, DNSCredentialID: 7, Status: domain.CertStatusPending}
	certs.Create(context.Background(), c)

	dns := &fakeDNSRepo{creds: map[int64]*domain.DNSCredential{7: {ID: 7, Provider: "cloudflare", Credentials: map[string]string{"CF_DNS_API_TOKEN": "t"}}}}
	na := time.Now().Add(80 * 24 * time.Hour)
	issuer := &fakeIssuer{result: ports.ACMEResult{CertPEM: "CERT", KeyPEM: "KEY", AccountKeyPEM: "ACCT", RegistrationJSON: "REG", Fingerprint: "fp", NotAfter: na, NotBefore: time.Now()}}
	node := &domain.Node{ID: 11, PanelID: 1, InboundID: 2, StreamSettings: `{"security":"tls","tlsSettings":{}}`}
	nodes := &fakeNodeRepo{byCert: map[int64][]*domain.Node{c.ID: {node}}, byID: map[int64]*domain.Node{11: node}}
	tasks := &fakeTaskRepo{due: []*domain.SyncTask{{ID: 1, Type: domain.SyncTaskCertIssue, TargetType: certTargetType, TargetID: c.ID}}}
	pusher := &fakePusher{}
	accts := seededAccount()
	s := newTestService(certs, dns, accts, issuer, nodes, tasks, pusher)

	if err := s.ProcessDueTasks(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if issuer.calls != 1 {
		t.Fatalf("issuer called %d times, want 1", issuer.calls)
	}
	// The request must carry the selected account's identity, not a global default.
	if issuer.lastReq.Email != "admin@example.com" || issuer.lastReq.DirectoryURL != "https://acme.example/dir" {
		t.Fatalf("request did not use the cert's ACME account: %+v", issuer.lastReq)
	}
	// The freshly registered key/registration must be written back to that account.
	if a, _ := accts.GetByID(context.Background(), 1); a.AccountKey != "ACCT" || a.Registration != "REG" {
		t.Fatalf("account registration not persisted back: %+v", a)
	}
	got := certs.certs[c.ID]
	if got.Status != domain.CertStatusActive || got.CertPEM != "CERT" || got.Fingerprint != "fp" || got.NotAfter == nil {
		t.Fatalf("cert not persisted active: %#v", got)
	}
	if nodes.configUpdates != 1 || len(pusher.pushed) != 1 || pusher.pushed[0] != 11 {
		t.Fatalf("deploy not performed: configUpdates=%d pushed=%v", nodes.configUpdates, pusher.pushed)
	}
	if len(tasks.succeeded) != 1 {
		t.Fatalf("task not marked succeeded: %#v", tasks.succeeded)
	}
}

// The account's EAB + key type must flow through to the issuer request.
func TestRunCertTaskPassesEABAndKeyType(t *testing.T) {
	certs := newFakeCertRepo()
	c := &domain.TLSCertificate{Name: "z", Domains: []string{"z.com"}, ACMEAccountID: 1, DNSCredentialID: 7, Status: domain.CertStatusPending}
	certs.Create(context.Background(), c)
	accts := newFakeAccountRepo(&domain.ACMEAccount{Name: "zerossl", Email: "ops@x.com", Directory: "https://acme.zerossl.com/v2/DV90", EABKeyID: "kid123", EABHMACKey: "hmac456", KeyType: "RSA4096"})
	dns := &fakeDNSRepo{creds: map[int64]*domain.DNSCredential{7: {ID: 7, Provider: "cloudflare", Credentials: map[string]string{"CF_DNS_API_TOKEN": "t"}}}}
	na := time.Now().Add(80 * 24 * time.Hour)
	issuer := &fakeIssuer{result: ports.ACMEResult{CertPEM: "C", KeyPEM: "K", Fingerprint: "fp", NotAfter: na, NotBefore: time.Now()}}
	node := &domain.Node{ID: 11, PanelID: 1, InboundID: 2, StreamSettings: `{"security":"tls","tlsSettings":{}}`}
	nodes := &fakeNodeRepo{byCert: map[int64][]*domain.Node{c.ID: {node}}, byID: map[int64]*domain.Node{11: node}}
	tasks := &fakeTaskRepo{due: []*domain.SyncTask{{ID: 1, Type: domain.SyncTaskCertIssue, TargetType: certTargetType, TargetID: c.ID}}}
	s := newTestService(certs, dns, accts, issuer, nodes, tasks, &fakePusher{})

	if err := s.ProcessDueTasks(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	r := issuer.lastReq
	if r.EABKeyID != "kid123" || r.EABHMACKey != "hmac456" || r.KeyType != "RSA4096" {
		t.Fatalf("EAB/keytype not threaded into the request: %+v", r)
	}
}

func TestRunCertTaskMissingDNSCredFailsPermanently(t *testing.T) {
	certs := newFakeCertRepo()
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"x.com"}, ACMEAccountID: 1, DNSCredentialID: 99, Status: domain.CertStatusPending}
	certs.Create(context.Background(), c)
	tasks := &fakeTaskRepo{due: []*domain.SyncTask{{ID: 1, Type: domain.SyncTaskCertIssue, TargetType: certTargetType, TargetID: c.ID}}}
	issuer := &fakeIssuer{}
	s := newTestService(certs, &fakeDNSRepo{creds: map[int64]*domain.DNSCredential{}}, seededAccount(), issuer, &fakeNodeRepo{}, tasks, &fakePusher{})

	if err := s.ProcessDueTasks(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if issuer.calls != 0 {
		t.Fatal("issuer must not be called when dns cred is missing")
	}
	if certs.certs[c.ID].Status != domain.CertStatusFailed || certs.certs[c.ID].LastError == "" {
		t.Fatalf("cert not marked failed: %#v", certs.certs[c.ID])
	}
	if len(tasks.canceled) != 1 || len(tasks.retried) != 0 {
		t.Fatalf("permanent error must cancel (not retry): canceled=%v retried=%v", tasks.canceled, tasks.retried)
	}
}

// A missing ACME account is a permanent issuance error (the cert references an
// account that was deleted out from under it).
func TestRunCertTaskMissingACMEAccountFailsPermanently(t *testing.T) {
	certs := newFakeCertRepo()
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"x.com"}, ACMEAccountID: 42, DNSCredentialID: 7, Status: domain.CertStatusPending}
	certs.Create(context.Background(), c)
	dns := &fakeDNSRepo{creds: map[int64]*domain.DNSCredential{7: {ID: 7, Provider: "cloudflare"}}}
	tasks := &fakeTaskRepo{due: []*domain.SyncTask{{ID: 1, Type: domain.SyncTaskCertIssue, TargetType: certTargetType, TargetID: c.ID}}}
	issuer := &fakeIssuer{}
	s := newTestService(certs, dns, newFakeAccountRepo(), issuer, &fakeNodeRepo{}, tasks, &fakePusher{})

	if err := s.ProcessDueTasks(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if issuer.calls != 0 {
		t.Fatal("issuer must not be called when the acme account is missing")
	}
	if certs.certs[c.ID].Status != domain.CertStatusFailed {
		t.Fatalf("cert not marked failed: %#v", certs.certs[c.ID])
	}
	if len(tasks.canceled) != 1 {
		t.Fatalf("a missing account is permanent (cancel, not retry): canceled=%v retried=%v", tasks.canceled, tasks.retried)
	}
}

// A retry of an already-issued cert (e.g. after a transient deploy failure) must
// NOT call ACME again — it only re-deploys.
func TestRunCertTaskSkipsReissueWhenAlreadyActive(t *testing.T) {
	certs := newFakeCertRepo()
	na := time.Now().Add(80 * 24 * time.Hour)
	nb := time.Now()
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"x.com"}, ACMEAccountID: 1, DNSCredentialID: 7, Status: domain.CertStatusActive, CertPEM: "CERT", KeyPEM: "KEY", NotAfter: &na, NotBefore: &nb}
	certs.Create(context.Background(), c)
	node := &domain.Node{ID: 11, PanelID: 1, InboundID: 2, StreamSettings: `{"security":"tls","tlsSettings":{}}`}
	nodes := &fakeNodeRepo{byCert: map[int64][]*domain.Node{c.ID: {node}}, byID: map[int64]*domain.Node{11: node}}
	tasks := &fakeTaskRepo{due: []*domain.SyncTask{{ID: 1, Type: domain.SyncTaskCertIssue, TargetType: certTargetType, TargetID: c.ID}}}
	issuer := &fakeIssuer{}
	pusher := &fakePusher{}
	s := newTestService(certs, &fakeDNSRepo{}, seededAccount(), issuer, nodes, tasks, pusher)

	if err := s.ProcessDueTasks(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if issuer.calls != 0 {
		t.Fatalf("already-active cert must not re-issue, got %d calls", issuer.calls)
	}
	if len(pusher.pushed) != 1 {
		t.Fatalf("should still re-deploy: pushed=%v", pusher.pushed)
	}
}

// The cert_renew obtain-gate must be independent of the admin-mutable renew
// threshold (no scan→run race): re-issue when life is mostly spent, skip ACME
// when the cert is freshly renewed so a deploy-retry only re-pushes.
func TestShouldObtainRenewFreshnessGate(t *testing.T) {
	s := &Service{}
	now := time.Now()
	day := 24 * time.Hour
	mk := func(remainingDays, lifetimeDays int) *domain.TLSCertificate {
		na := now.Add(time.Duration(remainingDays) * day)
		nb := na.Add(-time.Duration(lifetimeDays) * day)
		return &domain.TLSCertificate{Status: domain.CertStatusActive, CertPEM: "x", NotBefore: &nb, NotAfter: &na}
	}
	if !s.shouldObtain(mk(30, 90), domain.SyncTaskCertRenew) {
		t.Fatal("cert with 1/3 life left should obtain")
	}
	if s.shouldObtain(mk(85, 90), domain.SyncTaskCertRenew) {
		t.Fatal("freshly renewed cert (>2/3 life left) must not re-obtain")
	}
	if !s.shouldObtain(&domain.TLSCertificate{Status: domain.CertStatusPending}, domain.SyncTaskCertRenew) {
		t.Fatal("never-issued cert should obtain")
	}
}

// Editing a DNS credential and leaving a secret blank means "keep the stored
// value" — blanking untouched secrets would silently break renewals.
func TestUpdateDNSCredKeepsBlankSecrets(t *testing.T) {
	dns := &fakeDNSRepo{creds: map[int64]*domain.DNSCredential{
		3: {ID: 3, Provider: "cloudflare", Credentials: map[string]string{"CF_DNS_API_TOKEN": "stored", "CF_API_EMAIL": "a@b.c"}},
	}}
	s := newTestService(newFakeCertRepo(), dns, newFakeAccountRepo(), &fakeIssuer{}, &fakeNodeRepo{}, &fakeTaskRepo{}, &fakePusher{})

	in := &domain.DNSCredential{ID: 3, Provider: "cloudflare", Credentials: map[string]string{"CF_DNS_API_TOKEN": "", "CF_API_EMAIL": "new@b.c"}}
	if err := s.UpdateDNSCred(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	got := dns.lastUpdated.Credentials
	if got["CF_DNS_API_TOKEN"] != "stored" {
		t.Fatalf("blank secret must keep stored value, got %q", got["CF_DNS_API_TOKEN"])
	}
	if got["CF_API_EMAIL"] != "new@b.c" {
		t.Fatalf("changed secret must apply, got %q", got["CF_API_EMAIL"])
	}
}

// ---- ACME account CRUD ----

func TestCreateACMEAccountDuplicateRejected(t *testing.T) {
	accts := newFakeAccountRepo()
	s := newTestService(newFakeCertRepo(), &fakeDNSRepo{}, accts, &fakeIssuer{}, &fakeNodeRepo{}, &fakeTaskRepo{}, &fakePusher{})
	a := &domain.ACMEAccount{Name: "a", Email: "x@y.z", Directory: "https://acme/dir"}
	if err := s.CreateACMEAccount(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	dup := &domain.ACMEAccount{Name: "b", Email: "x@y.z", Directory: "https://acme/dir"}
	if err := s.CreateACMEAccount(context.Background(), dup); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("same (email,directory) must be rejected as duplicate, got %v", err)
	}
}

func TestCreateACMEAccountValidatesEABPairing(t *testing.T) {
	s := newTestService(newFakeCertRepo(), &fakeDNSRepo{}, newFakeAccountRepo(), &fakeIssuer{}, &fakeNodeRepo{}, &fakeTaskRepo{}, &fakePusher{})
	// A kid without an HMAC can't register → must be rejected.
	a := &domain.ACMEAccount{Name: "z", Email: "x@y.z", Directory: "https://acme/dir", EABKeyID: "kid"}
	if err := s.CreateACMEAccount(context.Background(), a); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a lone EAB kid must be a validation error, got %v", err)
	}
}

// A blank EAB HMAC on update means "keep the stored secret" (write-only), and an
// edit that does NOT change the registered identity must NOT clear the account's
// stored registration.
func TestUpdateACMEAccountKeepsHMACAndRegistrationOnRename(t *testing.T) {
	accts := newFakeAccountRepo(&domain.ACMEAccount{Name: "old", Email: "x@y.z", Directory: "https://acme/dir", EABKeyID: "kid", EABHMACKey: "secret"})
	// Pretend it already registered.
	accts.UpdateRegistration(context.Background(), 1, "ACCTKEY", "REG")
	s := newTestService(newFakeCertRepo(), &fakeDNSRepo{}, accts, &fakeIssuer{}, &fakeNodeRepo{}, &fakeTaskRepo{}, &fakePusher{})

	// Rename only; HMAC left blank (not re-entered).
	if err := s.UpdateACMEAccount(context.Background(), &domain.ACMEAccount{ID: 1, Name: "new", Email: "x@y.z", Directory: "https://acme/dir", EABKeyID: "kid", EABHMACKey: ""}); err != nil {
		t.Fatal(err)
	}
	got, _ := accts.GetByID(context.Background(), 1)
	if got.Name != "new" {
		t.Fatalf("rename not applied: %q", got.Name)
	}
	if got.EABHMACKey != "secret" {
		t.Fatalf("blank HMAC must keep stored secret, got %q", got.EABHMACKey)
	}
	if got.AccountKey != "ACCTKEY" || got.Registration != "REG" {
		t.Fatalf("a non-identity edit must not clear registration: %+v", got)
	}
}

// Changing the registered identity (here: the directory) must clear the stored
// account key + registration so the next issuance re-registers.
func TestUpdateACMEAccountClearsRegistrationOnIdentityChange(t *testing.T) {
	accts := newFakeAccountRepo(&domain.ACMEAccount{Name: "a", Email: "x@y.z", Directory: "https://le/dir"})
	accts.UpdateRegistration(context.Background(), 1, "ACCTKEY", "REG")
	s := newTestService(newFakeCertRepo(), &fakeDNSRepo{}, accts, &fakeIssuer{}, &fakeNodeRepo{}, &fakeTaskRepo{}, &fakePusher{})

	if err := s.UpdateACMEAccount(context.Background(), &domain.ACMEAccount{ID: 1, Name: "a", Email: "x@y.z", Directory: "https://zerossl/dir"}); err != nil {
		t.Fatal(err)
	}
	got, _ := accts.GetByID(context.Background(), 1)
	if got.AccountKey != "" || got.Registration != "" {
		t.Fatalf("identity change must clear registration, got %+v", got)
	}
}

// Deleting an ACME account still used by a certificate must be refused.
func TestDeleteACMEAccountGuardsBoundCerts(t *testing.T) {
	certs := newFakeCertRepo()
	certs.Create(context.Background(), &domain.TLSCertificate{Name: "w", Domains: []string{"x"}, ACMEAccountID: 1})
	accts := newFakeAccountRepo(&domain.ACMEAccount{Name: "a", Email: "x@y.z", Directory: "https://acme/dir"})
	s := newTestService(certs, &fakeDNSRepo{}, accts, &fakeIssuer{}, &fakeNodeRepo{}, &fakeTaskRepo{}, &fakePusher{})

	if err := s.DeleteACMEAccount(context.Background(), 1); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("delete must be refused while a cert references the account, got %v", err)
	}
	if _, err := accts.GetByID(context.Background(), 1); err != nil {
		t.Fatal("account must not have been deleted")
	}
}

func TestScanDueRenewalsEnqueuesOnlyDue(t *testing.T) {
	certs := newFakeCertRepo()
	day := 24 * time.Hour
	mk := func(name string, remainingDays int, autoRenew bool) {
		na := time.Now().Add(time.Duration(remainingDays) * day)
		nb := na.Add(-90 * day)
		certs.Create(context.Background(), &domain.TLSCertificate{Name: name, Domains: []string{name}, Status: domain.CertStatusActive, AutoRenew: autoRenew, NotBefore: &nb, NotAfter: &na})
	}
	mk("due", 20, true)    // 20d left, threshold 30 → due
	mk("notdue", 60, true) // 60d left → not due
	mk("noauto", 5, false) // due window but auto-renew off → skip
	tasks := &fakeTaskRepo{}
	s := newTestService(certs, &fakeDNSRepo{}, newFakeAccountRepo(), &fakeIssuer{}, &fakeNodeRepo{}, tasks, &fakePusher{})

	if err := s.ScanDueRenewals(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(tasks.created) != 1 {
		t.Fatalf("want exactly 1 renew task, got %d: %#v", len(tasks.created), tasks.created)
	}
	if tasks.created[0].Type != domain.SyncTaskCertRenew {
		t.Fatalf("wrong task type: %s", tasks.created[0].Type)
	}
}

type fakeCertEventRepo struct{ events []*domain.CertEvent }

func (r *fakeCertEventRepo) Create(_ context.Context, e *domain.CertEvent) error {
	cp := *e
	r.events = append(r.events, &cp)
	return nil
}
func (r *fakeCertEventRepo) ListPaged(_ context.Context, _, _ int) ([]*domain.CertEvent, int64, error) {
	return r.events, int64(len(r.events)), nil
}
func (r *fakeCertEventRepo) PruneOlderThan(_ context.Context, _ time.Time) (int64, error) { return 0, nil }

func TestRunCertTaskRecordsSuccessEvent(t *testing.T) {
	certs := newFakeCertRepo()
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"x.com"}, ACMEAccountID: 1, DNSCredentialID: 7, Status: domain.CertStatusPending}
	certs.Create(context.Background(), c)
	dns := &fakeDNSRepo{creds: map[int64]*domain.DNSCredential{7: {ID: 7, Provider: "cloudflare", Credentials: map[string]string{"CF_DNS_API_TOKEN": "t"}}}}
	na := time.Now().Add(80 * 24 * time.Hour)
	issuer := &fakeIssuer{result: ports.ACMEResult{CertPEM: "CERT", KeyPEM: "KEY", Fingerprint: "fp", NotAfter: na, NotBefore: time.Now()}}
	node := &domain.Node{ID: 11, PanelID: 1, InboundID: 2, StreamSettings: `{"security":"tls","tlsSettings":{}}`}
	nodes := &fakeNodeRepo{byCert: map[int64][]*domain.Node{c.ID: {node}}, byID: map[int64]*domain.Node{11: node}}
	tasks := &fakeTaskRepo{due: []*domain.SyncTask{{ID: 1, Type: domain.SyncTaskCertIssue, TargetType: certTargetType, TargetID: c.ID}}}
	s := newTestService(certs, dns, seededAccount(), issuer, nodes, tasks, &fakePusher{})
	events := &fakeCertEventRepo{}
	s.SetEventRepo(events)

	if err := s.ProcessDueTasks(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if len(events.events) != 1 || !events.events[0].Success || events.events[0].Kind != domain.CertEventIssue {
		t.Fatalf("want 1 success issue event, got %#v", events.events)
	}
}

func TestRunCertTaskRecordsFailEvent(t *testing.T) {
	certs := newFakeCertRepo()
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"x.com"}, ACMEAccountID: 1, DNSCredentialID: 99, Status: domain.CertStatusPending}
	certs.Create(context.Background(), c)
	tasks := &fakeTaskRepo{due: []*domain.SyncTask{{ID: 1, Type: domain.SyncTaskCertIssue, TargetType: certTargetType, TargetID: c.ID}}}
	s := newTestService(certs, &fakeDNSRepo{creds: map[int64]*domain.DNSCredential{}}, seededAccount(), &fakeIssuer{}, &fakeNodeRepo{}, tasks, &fakePusher{})
	events := &fakeCertEventRepo{}
	s.SetEventRepo(events)

	if err := s.ProcessDueTasks(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if len(events.events) != 1 || events.events[0].Success || events.events[0].Message == "" {
		t.Fatalf("want 1 fail event with a message, got %#v", events.events)
	}
}

// Failed certs (auto-renew on) are re-enqueued each scan so they retry at the
// check interval; a failed cert with auto-renew off is left alone.
func TestScanDueRenewalsRetriesFailedCerts(t *testing.T) {
	certs := newFakeCertRepo()
	certs.Create(context.Background(), &domain.TLSCertificate{Name: "broken", Domains: []string{"x"}, Status: domain.CertStatusFailed, AutoRenew: true})
	certs.Create(context.Background(), &domain.TLSCertificate{Name: "off", Domains: []string{"y"}, Status: domain.CertStatusFailed, AutoRenew: false})
	tasks := &fakeTaskRepo{}
	s := newTestService(certs, &fakeDNSRepo{}, newFakeAccountRepo(), &fakeIssuer{}, &fakeNodeRepo{}, tasks, &fakePusher{})

	if err := s.ScanDueRenewals(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(tasks.created) != 1 {
		t.Fatalf("want exactly 1 retry task (only the auto-renew failed cert), got %d: %#v", len(tasks.created), tasks.created)
	}
	if tasks.created[0].Type != domain.SyncTaskCertIssue {
		t.Fatalf("a failed never-issued cert should re-issue, got %s", tasks.created[0].Type)
	}
}
