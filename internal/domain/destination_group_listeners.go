package domain

// Only enabled listeners are projected. No credentials or transport config are
// needed for destination-policy sniffing preflight.
type DestGroupListener struct {
	NodeID   int64 `gorm:"column:id"`
	PanelID  int64
	Sniffing string
}
