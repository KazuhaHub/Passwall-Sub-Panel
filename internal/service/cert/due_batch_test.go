package cert

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ProcessDueBatch reports what the loop needs to decide whether to take another
// batch at once: how many tasks it ran and how many of those did not succeed.
func TestProcessDueBatchCountsRunAndFailed(t *testing.T) {
	certs := newFakeCertRepo()
	c := &domain.TLSCertificate{Name: "w", Domains: []string{"x.com"}, ACMEAccountID: 1, DNSCredentialID: 99, Status: domain.CertStatusPending}
	certs.Create(context.Background(), c)
	tasks := &fakeTaskRepo{due: []*domain.SyncTask{
		{ID: 1, Type: domain.SyncTaskCertIssue, TargetType: certTargetType, TargetID: c.ID},   // DNS credential missing: fails
		{ID: 2, Type: domain.SyncTaskCertIssue, TargetType: certTargetType, TargetID: 424242}, // certificate gone: nothing to do, succeeds
	}}
	s := newTestService(certs, &fakeDNSRepo{creds: map[int64]*domain.DNSCredential{}}, seededAccount(), &fakeIssuer{}, &fakeNodeRepo{}, tasks, &fakePusher{})

	ran, failed, err := s.ProcessDueBatch(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if ran != 2 || failed != 1 {
		t.Fatalf("ran=%d failed=%d, want 2 and 1", ran, failed)
	}
}
