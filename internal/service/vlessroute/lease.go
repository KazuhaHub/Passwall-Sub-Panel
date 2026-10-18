package vlessroute

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/google/uuid"
)

// acquireBackendLease shares lease ownership and release semantics. Callers
// retain their bounded operation context and must re-read state after locking.
func (s *Service) acquireBackendLease(ctx context.Context, panelURL string, failIfBusy bool) (func(), bool, error) {
	backend := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimRight(panelURL, "/"))))
	owner := uuid.NewString()
	acquired, err := s.repos.NodeRoute.AcquireRouteLease(ctx, backend, owner, time.Now().UTC().Add(90*time.Second))
	if err != nil {
		return nil, false, err
	}
	if !acquired {
		if failIfBusy {
			return nil, false, fmt.Errorf("%w: another routing operation is running; retry after refreshing", domain.ErrConflict)
		}
		return nil, false, nil
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.repos.NodeRoute.ReleaseRouteLease(releaseCtx, backend, owner)
	}, true, nil
}
