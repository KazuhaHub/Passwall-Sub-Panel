package cert

import (
	"context"
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
func (r *fakeDNSRepo) Delete(_ context.Context, id int64) error               { return nil }
func (r *fakeDNSRepo) List(_ context.Context) ([]*domain.DNSCredential, error) { return nil, nil }
func (r *fakeDNSRepo) GetByID(_ context.Context, id int64) (*domain.DNSCredential, error) {
	c, ok := r.creds[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return c, nil
}

type fakeAccountRepo struct{ acct *domain.ACMEAccount }

func (r *fakeAccountRepo) GetByEmailDirectory(_ context.Context, _, _ string) (*domain.ACMEAccount, error) {
	return r.acct, nil
}
func (r *fakeAccountRepo) Save(_ context.Context, a *domain.ACMEAccount) error {
	a.ID = 1
	r.acct = a
	return nil
}

type fakeIssuer struct {
	calls  int
	result ports.ACMEResult
	err    error
}

func (i *fakeIssuer) Obtain(_ context.Context, _ ports.ACMERequest) (ports.ACMEResult, error) {
	i.calls++
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
	s := newTestService(certs, &fakeDNSRepo{}, &fakeAccountRepo{}, &fakeIssuer{}, nodes, &fakeTaskRepo{}, pusher)

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
	s := newTestService(newFakeCertRepo(), &fakeDNSRepo{}, &fakeAccountRepo{}, &fakeIssuer{}, nodes, &fakeTaskRepo{}, pusher)

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
func (r *fakeTaskRepo) MarkRunning(_ context.Context, _ int64) (bool, error)        { return true, nil }
func (r *fakeTaskRepo) MarkSucceeded(_ context.Context, id int64) error             { r.succeeded = append(r.succeeded, id); return nil }
func (r *fakeTaskRepo) MarkRetry(_ context.Context, id int64, _ string, _ time.Time) error { r.retried = append(r.retried, id); return nil }
func (r *fakeTaskRepo) Cancel(_ context.Context, id int64) error                    { r.canceled = append(r.canceled, id); return nil }

type fakePusher struct{ pushed []int64 }

func (p *fakePusher) EnqueueConfigPush(_ context.Context, nodeID int64) error {
	p.pushed = append(p.pushed, nodeID)
	return nil
}

func newTestService(certs *fakeCertRepo, dns *fakeDNSRepo, acct *fakeAccountRepo, issuer *fakeIssuer, nodes *fakeNodeRepo, tasks *fakeTaskRepo, pusher *fakePusher) *Service {
	return New(certs, dns, acct, issuer, nodes, tasks, pusher, "https://acme.example/dir", "admin@example.com", 30)
}

// ---- tests ----

func TestCreateCertEnqueuesIssueTask(t *testing.T) {
	certs := newFakeCertRepo()
	tasks := &fakeTaskRepo{}
	s := newTestService(certs, &fakeDNSRepo{}, &fakeAccountRepo{}, &fakeIssuer{}, &fakeNodeRepo{}, tasks, &fakePusher{})

	c := &domain.TLSCertificate{Name: "w", Domains: []string{"*.example.com"}}
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

func TestRunCertTaskIssuesPersistsAndDeploys(t *testing.T) {
	certs := newFakeCertRepo()
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"*.example.com"}, DNSCredentialID: 7, Status: domain.CertStatusPending}
	certs.Create(context.Background(), c)

	dns := &fakeDNSRepo{creds: map[int64]*domain.DNSCredential{7: {ID: 7, Provider: "cloudflare", Credentials: map[string]string{"CF_DNS_API_TOKEN": "t"}}}}
	na := time.Now().Add(80 * 24 * time.Hour)
	issuer := &fakeIssuer{result: ports.ACMEResult{CertPEM: "CERT", KeyPEM: "KEY", AccountKeyPEM: "ACCT", Fingerprint: "fp", NotAfter: na, NotBefore: time.Now()}}
	node := &domain.Node{ID: 11, PanelID: 1, InboundID: 2, StreamSettings: `{"security":"tls","tlsSettings":{}}`}
	nodes := &fakeNodeRepo{byCert: map[int64][]*domain.Node{c.ID: {node}}, byID: map[int64]*domain.Node{11: node}}
	tasks := &fakeTaskRepo{due: []*domain.SyncTask{{ID: 1, Type: domain.SyncTaskCertIssue, TargetType: certTargetType, TargetID: c.ID}}}
	pusher := &fakePusher{}
	s := newTestService(certs, dns, &fakeAccountRepo{}, issuer, nodes, tasks, pusher)

	if err := s.ProcessDueTasks(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if issuer.calls != 1 {
		t.Fatalf("issuer called %d times, want 1", issuer.calls)
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

func TestRunCertTaskMissingDNSCredFailsPermanently(t *testing.T) {
	certs := newFakeCertRepo()
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"x.com"}, DNSCredentialID: 99, Status: domain.CertStatusPending}
	certs.Create(context.Background(), c)
	tasks := &fakeTaskRepo{due: []*domain.SyncTask{{ID: 1, Type: domain.SyncTaskCertIssue, TargetType: certTargetType, TargetID: c.ID}}}
	issuer := &fakeIssuer{}
	s := newTestService(certs, &fakeDNSRepo{creds: map[int64]*domain.DNSCredential{}}, &fakeAccountRepo{}, issuer, &fakeNodeRepo{}, tasks, &fakePusher{})

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

// A retry of an already-issued cert (e.g. after a transient deploy failure) must
// NOT call ACME again — it only re-deploys.
func TestRunCertTaskSkipsReissueWhenAlreadyActive(t *testing.T) {
	certs := newFakeCertRepo()
	na := time.Now().Add(80 * 24 * time.Hour)
	nb := time.Now()
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"x.com"}, DNSCredentialID: 7, Status: domain.CertStatusActive, CertPEM: "CERT", KeyPEM: "KEY", NotAfter: &na, NotBefore: &nb}
	certs.Create(context.Background(), c)
	node := &domain.Node{ID: 11, PanelID: 1, InboundID: 2, StreamSettings: `{"security":"tls","tlsSettings":{}}`}
	nodes := &fakeNodeRepo{byCert: map[int64][]*domain.Node{c.ID: {node}}, byID: map[int64]*domain.Node{11: node}}
	tasks := &fakeTaskRepo{due: []*domain.SyncTask{{ID: 1, Type: domain.SyncTaskCertIssue, TargetType: certTargetType, TargetID: c.ID}}}
	issuer := &fakeIssuer{}
	pusher := &fakePusher{}
	s := newTestService(certs, &fakeDNSRepo{}, &fakeAccountRepo{}, issuer, nodes, tasks, pusher)

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
	s := newTestService(newFakeCertRepo(), dns, &fakeAccountRepo{}, &fakeIssuer{}, &fakeNodeRepo{}, &fakeTaskRepo{}, &fakePusher{})

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

func TestScanDueRenewalsEnqueuesOnlyDue(t *testing.T) {
	certs := newFakeCertRepo()
	day := 24 * time.Hour
	mk := func(name string, remainingDays int, autoRenew bool) {
		na := time.Now().Add(time.Duration(remainingDays) * day)
		nb := na.Add(-90 * day)
		certs.Create(context.Background(), &domain.TLSCertificate{Name: name, Domains: []string{name}, Status: domain.CertStatusActive, AutoRenew: autoRenew, NotBefore: &nb, NotAfter: &na})
	}
	mk("due", 20, true)        // 20d left, threshold 30 → due
	mk("notdue", 60, true)     // 60d left → not due
	mk("noauto", 5, false)     // due window but auto-renew off → skip
	tasks := &fakeTaskRepo{}
	s := newTestService(certs, &fakeDNSRepo{}, &fakeAccountRepo{}, &fakeIssuer{}, &fakeNodeRepo{}, tasks, &fakePusher{})

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
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"x.com"}, DNSCredentialID: 7, Status: domain.CertStatusPending}
	certs.Create(context.Background(), c)
	dns := &fakeDNSRepo{creds: map[int64]*domain.DNSCredential{7: {ID: 7, Provider: "cloudflare", Credentials: map[string]string{"CF_DNS_API_TOKEN": "t"}}}}
	na := time.Now().Add(80 * 24 * time.Hour)
	issuer := &fakeIssuer{result: ports.ACMEResult{CertPEM: "CERT", KeyPEM: "KEY", Fingerprint: "fp", NotAfter: na, NotBefore: time.Now()}}
	node := &domain.Node{ID: 11, PanelID: 1, InboundID: 2, StreamSettings: `{"security":"tls","tlsSettings":{}}`}
	nodes := &fakeNodeRepo{byCert: map[int64][]*domain.Node{c.ID: {node}}, byID: map[int64]*domain.Node{11: node}}
	tasks := &fakeTaskRepo{due: []*domain.SyncTask{{ID: 1, Type: domain.SyncTaskCertIssue, TargetType: certTargetType, TargetID: c.ID}}}
	s := newTestService(certs, dns, &fakeAccountRepo{}, issuer, nodes, tasks, &fakePusher{})
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
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"x.com"}, DNSCredentialID: 99, Status: domain.CertStatusPending}
	certs.Create(context.Background(), c)
	tasks := &fakeTaskRepo{due: []*domain.SyncTask{{ID: 1, Type: domain.SyncTaskCertIssue, TargetType: certTargetType, TargetID: c.ID}}}
	s := newTestService(certs, &fakeDNSRepo{creds: map[int64]*domain.DNSCredential{}}, &fakeAccountRepo{}, &fakeIssuer{}, &fakeNodeRepo{}, tasks, &fakePusher{})
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
	s := newTestService(certs, &fakeDNSRepo{}, &fakeAccountRepo{}, &fakeIssuer{}, &fakeNodeRepo{}, tasks, &fakePusher{})

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
