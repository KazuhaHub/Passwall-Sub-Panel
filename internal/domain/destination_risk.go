package domain

// DestRiskWindow is an internal, address-free projection of one read snapshot.
// Missing users have neither eligible observed counts nor current clients.
// Collector availability is proved separately from current node metadata.
type DestRiskWindow struct {
	Users map[int64]DestRiskUserWindow
}

type DestRiskUserWindow struct {
	Sources        []DestBlockSource
	ClientPanelIDs []int64
	Losses         DestAuditLosses
}
