package domain

// RetiredPanelClients distinguishes access removed from an entire panel from
// clients replaced within a panel that still serves the account. Whole-panel
// removal only tightens access; replacement needs successful provisioning.
type RetiredPanelClients struct {
	Emails       []string
	PanelRemoved bool
}

type SharedClientRetirements map[int64]RetiredPanelClients
