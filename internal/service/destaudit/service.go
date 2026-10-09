package destaudit

import (
	"context"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Event contains only bounded diagnostic categories and counts.
type Event struct {
	AgentID, Kind, Outcome     string
	PanelID, ReceivedHour      int64
	Rows, DiagnosticKeys       int64
	NodeDropped, NodeUnmatched uint64
}

type Options struct {
	Now  func() time.Time
	Emit func(Event)
}

type Summary struct {
	Batches, Rows      int
	LossKeys, LossRows int64
}

type Service struct {
	q        *batchQueue
	controls map[int64]domain.DestAuditControl // protected by q.mu
	loss     *lossBuffer
	worker   *ingestWorker
	repo     ports.DestAuditStore
	now      func() time.Time
	emit     func(Event)
}

func New(context.Context, ports.DestAuditStore, Options) (*Service, error) {
	return nil, domain.ErrUnavailable
}
func (s *Service) Offer(string, int64, time.Time, protocol.AuditObservation) {}
func (s *Service) updateControls([]domain.DestAuditControl)                  {}
func (s *Service) StopOffers()                                               {}
func (s *Service) Run(context.Context) Summary                               { return Summary{} }
