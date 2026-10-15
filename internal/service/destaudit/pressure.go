package destaudit

// AdmissionPressure is the reservation state at one queue_full decision. The
// immutable numeric value belongs to that Offer's existing authenticated event;
// it carries no additional identities or payload and is zero on other outcomes.
// Batches and Bytes include the active batch until its worker releases it.
type AdmissionPressure struct {
	CandidateBytes int
	Batches        int
	Bytes          int
	OwnerBatches   int
	BatchLimit     int
	ByteLimit      int
	OwnerLimit     int
}

// LanePressure reports retained reservations, including a processing batch.
// It contains no panel, agent, batch, subject or destination identifiers.
type LanePressure struct {
	Kind       string `json:"kind"`
	Waiting    int    `json:"waiting"`
	Active     int    `json:"active"`
	Batches    int    `json:"batches"`
	Bytes      int    `json:"canonical_bytes"`
	BatchLimit int    `json:"batch_limit"`
	ByteLimit  int    `json:"byte_limit"`
	OwnerLimit int    `json:"owner_limit"`
	Owners     int    `json:"owners"`
	MaxOwner   int    `json:"max_owner_batches"`
}

// Pressure is a coherent, read-only queue view. It does not release canceled
// processing reservations, move the worker, inspect rows or touch storage.
func (s *Service) Pressure() [4]LanePressure {
	s.q.mu.Lock()
	defer s.q.mu.Unlock()
	var result [4]LanePressure
	for i, kind := range [...]string{"block", "observe", "trial", "usage"} {
		lane := s.q.lanes[kind]
		view := LanePressure{Kind: kind, Waiting: len(lane.waiting), Batches: lane.batches, Bytes: lane.bytes, BatchLimit: lane.limits.batches, ByteLimit: lane.limits.bytes, OwnerLimit: lane.limits.perAgent, Owners: len(lane.agents)}
		if lane.active != nil {
			view.Active = 1
		}
		for _, count := range lane.agents {
			view.MaxOwner = max(view.MaxOwner, count)
		}
		result[i] = view
	}
	return result
}
