package domain

// DestTestContext is a consistent readonly projection for a destination-only
// simulation. Credentials, listener configs, list originals and runtime bodies
// are not needed. Executable definitions come only from the published snapshot.
type DestTestContext struct {
	State                   DestPolicyState
	Snapshot                DestPolicySnapshot
	Published               bool
	SelectedPanelID         int64
	UserIDs                 []int64
	UserGroups              map[int64]int64
	PolicyNames, GroupNames map[int64]string
	Panels                  []DestTestPanel
}

type DestTestPanel struct {
	ID      int64
	Name    string
	Kind    PanelKind
	Agent   *NodeAgent
	Runtime *DestAgentPolicy
}
