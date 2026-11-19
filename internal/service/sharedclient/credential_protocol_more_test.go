package sharedclient

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestHysteria2StaleIDFailsConfirmEvenWithMatchingAuth(t *testing.T) {
	metrics.Reset()
	// Render uses AppliedUUID (the stored id) as the Hy2 password, so a stale
	// id must stay loud even though Auth reads back fine.
	xui := &idDriftXUI{staleXUI: staleXUI{stale: "x"}}
	svc := New(&fakeClients{attachments: credAttachments(20)}, fakePool{c: xui}, credNodes(map[int64]string{20: "hysteria2"}))
	if err := syncLifecycle(svc, context.Background(), credClient(), domain.UserLifecycle{Enable: true}); err == nil {
		t.Fatal("a Hy2 client whose id does not read back must fail the confirm")
	}
}

func TestHysteria2StaleIDDefeatsTheSkip(t *testing.T) {
	spec := ports.ClientSpec{ID: "uuid", Auth: "uuid", Password: "pw", Enable: true}
	cur := &ports.ClientDetail{ID: "stale-id", Auth: "uuid", Password: "other", Enable: true}
	hy2 := domain.CredentialFieldsFor(domain.ProtoHysteria2)
	if got := lifecycleWriteReason(cur, spec, true, true, hy2, 0); got != "id" {
		t.Fatalf("reason = %q, want id: a stale id on a Hy2 client must force a write", got)
	}
	cur.ID = "uuid"
	if got := lifecycleWriteReason(cur, spec, true, true, hy2, 0); got != "" {
		t.Fatalf("reason = %q, want skip: Hy2 does not read the password", got)
	}
}

func TestTUICComparesIDAndPassword(t *testing.T) {
	metrics.Reset()
	nodes := credNodes(map[int64]string{21: "tuic"})
	pw := &staleXUI{stale: "wrong"}
	svc := New(&fakeClients{attachments: credAttachments(21)}, fakePool{c: pw}, nodes)
	if err := syncLifecycle(svc, context.Background(), credClient(), domain.UserLifecycle{Enable: true}); err == nil {
		t.Fatal("a TUIC client with a wrong read-back password must fail")
	}
	c := credClient()
	id := &idDriftXUI{staleXUI: staleXUI{stale: c.Password}}
	svc = New(&fakeClients{attachments: credAttachments(21)}, fakePool{c: id}, nodes)
	if err := syncLifecycle(svc, context.Background(), c, domain.UserLifecycle{Enable: true}); err == nil {
		t.Fatal("a TUIC client with a wrong read-back id must fail")
	}
}

func TestSeparatorAttachmentIsIgnored(t *testing.T) {
	metrics.Reset()
	nodes := credNodes(map[int64]string{12: "vless"})
	sep := enabledNode(30, 10, 30)
	sep.Kind = domain.NodeKindSeparator
	nodes.byID[30] = sep
	xui := &staleXUI{stale: "STALE"}
	svc := New(&fakeClients{attachments: credAttachments(12, 30)}, fakePool{c: xui}, nodes)
	if err := syncLifecycle(svc, context.Background(), credClient(), domain.UserLifecycle{Enable: true}); err != nil {
		t.Fatalf("a separator must not widen the compared fields: %v", err)
	}
}

func TestSS2022MethodNodeComparesPassword(t *testing.T) {
	metrics.Reset()
	nodes := credNodes(map[int64]string{17: "shadowsocks"})
	nodes.byID[17].InboundSettings = `{"method":"2022-blake3-aes-256-gcm"}`
	xui := &staleXUI{stale: "wrong"}
	svc := New(&fakeClients{attachments: credAttachments(17)}, fakePool{c: xui}, nodes)
	if err := syncLifecycle(svc, context.Background(), credClient(), domain.UserLifecycle{Enable: true}); err == nil {
		t.Fatal("an SS-2022 client with a wrong read-back password must fail")
	}
}

type errNodes struct {
	fakeNodes
	failID int64
}

func (e errNodes) GetByID(ctx context.Context, id int64) (*domain.Node, error) {
	if id == e.failID {
		return nil, errors.New("db down")
	}
	return e.fakeNodes.GetByID(ctx, id)
}

func TestNodeLookupErrorFailsClosed(t *testing.T) {
	metrics.Reset()
	nodes := errNodes{fakeNodes: credNodes(map[int64]string{12: "vless", 13: "vless"}), failID: 13}
	xui := &staleXUI{stale: "STALE"}
	svc := New(&fakeClients{attachments: credAttachments(12, 13)}, fakePool{c: xui}, nodes)
	if err := syncLifecycle(svc, context.Background(), credClient(), domain.UserLifecycle{Enable: true}); err == nil {
		t.Fatal("a failed node lookup must compare every field, not none")
	}
}

type countingNodes struct {
	fakeNodes
	mu    sync.Mutex
	calls map[int64]int
}

func (c *countingNodes) GetByID(ctx context.Context, id int64) (*domain.Node, error) {
	c.mu.Lock()
	c.calls[id]++
	c.mu.Unlock()
	return c.fakeNodes.GetByID(ctx, id)
}

func TestLifecyclePushResolvesEachNodeOnce(t *testing.T) {
	metrics.Reset()
	nodes := &countingNodes{fakeNodes: credNodes(map[int64]string{12: "vless", 17: "shadowsocks"}), calls: map[int64]int{}}
	xui := &staleXUI{stale: "x"}
	svc := New(&fakeClients{attachments: credAttachments(12, 17)}, fakePool{c: xui}, nodes)
	c := credClient()
	c.Password = "x" // matches the read-back so the push succeeds
	c.NodeID, c.LineID = 12, "direct"
	if err := syncLifecycle(svc, context.Background(), c, domain.UserLifecycle{Enable: true}); err != nil {
		t.Fatal(err)
	}
	if len(nodes.calls) == 0 {
		t.Fatal("no node lookups recorded")
	}
	for id, n := range nodes.calls {
		if n != 1 {
			t.Errorf("node %d fetched %d times in one push, want 1", id, n)
		}
	}
}
