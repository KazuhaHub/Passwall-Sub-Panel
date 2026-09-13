package domain

// PanelUpdateChannel is the administrator's Passwall Node release preference.
// It is independent of agent/core identity and never initiates an upgrade.
type PanelUpdateChannel string

const (
	PanelUpdateStable PanelUpdateChannel = "stable"
	PanelUpdateBeta   PanelUpdateChannel = "beta"
)

func (channel PanelUpdateChannel) Valid() bool {
	return channel == PanelUpdateStable || channel == PanelUpdateBeta
}

// Effective safely resolves legacy/unknown preferences without rewriting the
// stored value. An older build must not turn a future channel into beta consent.
func (channel PanelUpdateChannel) Effective() PanelUpdateChannel {
	if channel == PanelUpdateBeta {
		return PanelUpdateBeta
	}
	return PanelUpdateStable
}
