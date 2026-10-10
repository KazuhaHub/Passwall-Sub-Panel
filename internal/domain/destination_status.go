package domain

// Status reads carry metadata only. Verified collection facts contain no
// executable body and are tied to the exact candidate digest.
type DestCollectionFacts struct {
	Collect  string
	Revision uint64
	Hits     bool
	Trial    bool
}

type DestStatusPanel struct {
	DestTestPanel
	Version         string
	Collect         AuditCollect
	CollectRevision uint64
	Facts           DestCollectionFacts
	Listeners       map[string]DestStatusListener
}

type DestStatusListener struct {
	Listener string `json:"listener"`
	Label    string `json:"label"`
	NodeID   *int64 `json:"node_id"`
}

type DestStatusContext struct {
	State      DestPolicyState
	Panels     []DestStatusPanel
	GroupNames map[int64]string
}
