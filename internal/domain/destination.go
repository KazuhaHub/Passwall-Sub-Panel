package domain

import "time"

type AuditCollect string

const (
	AuditCollectOff          AuditCollect = "off"
	AuditCollectHits         AuditCollect = "hits"
	AuditCollectHitsAndUsage AuditCollect = "hits_and_usage"
)

func (c AuditCollect) Valid() bool {
	return c == AuditCollectOff || c == AuditCollectHits || c == AuditCollectHitsAndUsage
}

// NormalizeAuditCollect initializes a newly created or legacy-unset panel.
// Explicit writes must validate before normalization so invalid input fails.
func NormalizeAuditCollect(c AuditCollect) AuditCollect {
	if c.Valid() {
		return c
	}
	return AuditCollectHits
}

type DestListKind string

type DestParseSample struct {
	Line   int    `json:"line"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

type DestParseReport struct {
	Accepted     int               `json:"accepted"`
	Ignored      int               `json:"ignored"`
	IgnoredBroad int               `json:"ignored_broad"`
	Rewritten    int               `json:"rewritten"`
	Samples      []DestParseSample `json:"samples"`
}

const (
	DestListCustom  DestListKind = "custom"
	DestListRemote  DestListKind = "remote"
	DestListGeosite DestListKind = "geosite"
)

// DestList stores normalized entries separately from the administrator's text.
// SourceText preserves comments and line numbers; it never goes to an agent.
type DestList struct {
	ID                                       int64
	Name                                     string
	Kind                                     DestListKind
	SourceURL, GeositeCategory, GeositeAttrs string
	Entries, SourceText                      []byte
	ParseReport                              *DestParseReport
	EntryCount, RegexpCount                  int
	ContentSHA256                            string
	LastFetchedAt                            *time.Time
	LastError                                string
	OwnerGroupID                             int64
	CreatedAt, UpdatedAt                     time.Time
}

// DestListRefresh contains only fields a background fetch is allowed to write.
// A nonempty LastError preserves the previously usable entries and fetch time.
type DestListRefresh struct {
	Entries                  []byte
	ParseReport              *DestParseReport
	EntryCount, RegexpCount  int
	ContentSHA256, LastError string
}

type DestReference struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type DestListInUseError struct{ UsedBy []DestReference }

func (e *DestListInUseError) Error() string { return "dest_list_in_use" }
func (e *DestListInUseError) Unwrap() error { return ErrConflict }

type DestAction string

const (
	DestAllow   DestAction = "allow"
	DestBlock   DestAction = "block"
	DestObserve DestAction = "observe"
)

type DestScope string

const (
	DestScopeAll    DestScope = "all"
	DestScopeGroups DestScope = "groups"
)

type DestInline struct {
	CIDRs     []string `json:"cidrs,omitempty"`
	Ports     string   `json:"ports,omitempty"`
	Network   string   `json:"network,omitempty"`
	Protocols []string `json:"protocols,omitempty"`
	Private   bool     `json:"private,omitempty"`
}

type DestPolicy struct {
	ID                    int64
	Name                  string
	Action                DestAction
	ListIDs               []int64
	Inline                DestInline
	Scope                 DestScope
	GroupIDs              []int64
	Priority              int
	Enabled, CountsAsRisk bool
	TemplateKey           string
	CreatedAt, UpdatedAt  time.Time
}

type DestExemption struct {
	UserID    int64
	Reason    string
	CreatedBy int64
	CreatedAt time.Time
	ExpiresAt *time.Time
}

type DestGroupMode struct {
	GroupID                 int64
	Mode, Stage             string
	ListIDs                 []int64
	BaseListID, ExtraListID int64
	StageChangedAt          *time.Time
	UpdatedAt               time.Time
}

type DestPublishError struct {
	Kind  string `json:"kind"`
	Used  int64  `json:"used,omitempty"`
	Limit int64  `json:"limit,omitempty"`
	Field string `json:"field,omitempty"`
}

// DestPolicyState separates definition writes from the last atomic publication.
// Initial timestamps are nil, not fabricated zero dates on strict SQL servers.
type DestPolicyState struct {
	Generation, PublishedGeneration              int64
	FirstUnpublishedAt, LastWriteAt, PublishedAt *time.Time
	Paused                                       bool
	PublishError                                 *DestPublishError
	PublishErrorAt                               *time.Time
}

type DestPolicySnapshot struct {
	Generation int64
	Body       []byte
	CreatedAt  time.Time
}

// DestDefinitions is one consistent database read, before publication. Member
// and roster data deliberately live outside the definition generation.
type DestDefinitions struct {
	State      DestPolicyState
	Lists      []DestList
	Policies   []DestPolicy
	Exemptions []DestExemption
	Groups     []DestGroupMode
}

type DestCandidateKind string

// DestPolicyMint describes the executable candidate's source. Exact candidate
// bytes and digest are derived from the config body inside the mint boundary.
type DestPolicyMint struct {
	Kind                                     DestCandidateKind
	Generation                               int64
	Context, DesiredSHA256, CollectEffective string
}

const (
	DestCandidateDesired  DestCandidateKind = "desired"
	DestCandidateFallback DestCandidateKind = "fallback"
	DestCandidateEmpty    DestCandidateKind = "empty"
	DestCandidatePaused   DestCandidateKind = "paused"
)

// DestAgentPolicy keeps the exact candidate independently of the confirmed LKG.
// Empty and paused candidates must never overwrite AppliedBody.
type DestAgentPolicy struct {
	AgentID                                      string
	DesiredSHA256, MintedSHA256                  string
	MintedBody                                   []byte
	MintedKind                                   DestCandidateKind
	MintedGeneration                             int64
	MintedContext                                string
	MintedAt                                     *time.Time
	FallbackReason                               string
	RejectedGeneration                           int64
	RejectedContext                              string
	FallbackExhausted                            bool
	OverLimit                                    *DestPublishError
	PrecheckListeners                            []string
	AppliedSHA256                                string
	AppliedBody                                  []byte
	AppliedAt                                    *time.Time
	AppliedRuleCount                             int
	AppliedGroups                                []int64
	CollectEffective                             string
	ReportedSHA256, ReportedState, ReportedIssue string
	ReportedListeners                            []string
	ReportedAt                                   *time.Time
	UpdatedAt                                    time.Time
}
