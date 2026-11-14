package domain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Destination policy ("access control") definitions. A policy tells a native
// node which destinations its proxy users may not reach; the node compiles it
// into the core's routing rules. These types are the DEFINITIONS the admin
// edits: the wire document the node receives is derived from them by the
// policy compiler, never stored here.

// DestListKind is where a list's entries come from.
type DestListKind string

const (
	// DestListCustom is text the admin pasted; SourceText keeps it verbatim.
	DestListCustom DestListKind = "custom"
	// DestListRemote is fetched from an https URL.
	DestListRemote DestListKind = "remote"
	// DestListGeosite is a v2fly domain-list-community category.
	DestListGeosite DestListKind = "geosite"
)

// Valid reports whether k is one of the three kinds.
func (k DestListKind) Valid() bool {
	switch k {
	case DestListCustom, DestListRemote, DestListGeosite:
		return true
	}
	return false
}

// DestListSide says which end of the connection a list's entries describe.
type DestListSide string

const (
	// DestSideDest lists destinations: domains, destination CIDRs and
	// countries. The default.
	DestSideDest DestListSide = "dest"
	// DestSideSource lists client addresses and countries. Only source rules
	// may use such a list; a destination policy must not.
	DestSideSource DestListSide = "source"
)

// Valid reports whether s is a known side.
func (s DestListSide) Valid() bool { return s == DestSideDest || s == DestSideSource }

// DestList is a named set of match entries. Entries are the canonical lines
// the parser produced, one per line (see service/destlist), held as bytes: a
// list may carry tens of thousands of entries, far past MySQL's 64 KiB TEXT.
type DestList struct {
	ID   int64
	Name string
	Kind DestListKind
	Side DestListSide
	// SourceURL is set for DestListRemote only.
	SourceURL string
	// GeositeCategory / GeositeAttrs are set for DestListGeosite only. Attrs
	// is a sorted, comma-joined set of literal attribute names ("cn", "!cn");
	// empty means every entry of the category.
	GeositeCategory string
	GeositeAttrs    string
	Entries         []byte
	// SourceText is the admin's pasted text with comments and line numbers
	// intact, for DestListCustom only: the editor shows it back.
	SourceText    []byte
	EntryCount    int
	RegexpCount   int
	ContentSHA256 string
	// LastFetchedAt is when Entries were last derived successfully from their
	// source (nil: never).
	LastFetchedAt *time.Time
	// LastError is the last failed refresh's reason, "" after a success.
	LastError string
	// OwnerGroupID is non-zero for a list that belongs to one allowlist group
	// (a later stage); such a list may not be used by any other policy.
	OwnerGroupID int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Ready says whether the list can be used. A custom list is its own source,
// so it is always ready (an EMPTY custom list is refused where it is
// referenced). Anything fetched is ready once it has fetched at least one
// entry: a remote list that never arrived, or a category that no longer
// exists, matches nothing, and an allowlist built on it would deny everyone.
func (l DestList) Ready() bool {
	if l.Kind == DestListCustom {
		return true
	}
	return l.LastFetchedAt != nil && l.EntryCount > 0
}

// DestAction is what a policy does to a matching connection.
type DestAction string

const (
	DestActionBlock DestAction = "block"
	DestActionAllow DestAction = "allow"
)

// Valid reports whether a is a stored policy action. "observe" is a WIRE
// action only: the compiler emits it for a policy in the report state.
func (a DestAction) Valid() bool { return a == DestActionBlock || a == DestActionAllow }

// DestState is the admin's switch on a policy or a source rule.
type DestState string

const (
	DestStateOff DestState = "off"
	DestStateOn  DestState = "on"
	// DestStateReport records what would be blocked and blocks nothing: the
	// rule compiles to a wire "observe" in the last phase, so it can never change
	// a verdict. A block policy and a source rule may be in it; an allow policy
	// may not.
	DestStateReport DestState = "report"
)

// Valid reports whether s is a known state.
func (s DestState) Valid() bool { return s == DestStateOff || s == DestStateOn || s == DestStateReport }

// DestScope is who a policy applies to.
type DestScope string

const (
	DestScopeAll    DestScope = "all"
	DestScopeGroups DestScope = "groups"
	DestScopePanels DestScope = "panels"
)

// Valid reports whether s is a known scope.
func (s DestScope) Valid() bool {
	return s == DestScopeAll || s == DestScopeGroups || s == DestScopePanels
}

// DestPolicyInline is the policy's own built-in match, beside its lists.
type DestPolicyInline struct {
	CIDRs     []string `json:"cidrs,omitempty"`
	Ports     string   `json:"ports,omitempty"`
	Network   string   `json:"network,omitempty"`
	Protocols []string `json:"protocols,omitempty"`
	Private   bool     `json:"private,omitempty"`
}

// Empty reports whether no inline field is set.
func (i DestPolicyInline) Empty() bool {
	return len(i.CIDRs) == 0 && i.Ports == "" && i.Network == "" && len(i.Protocols) == 0 && !i.Private
}

// DestListContent is what a refresh derived from a list's source: the entries
// and the figures stored beside them. It is the whole of a refresh's write,
// which is why a refresh cannot touch a list's name or pasted text.
type DestListContent struct {
	Entries       []byte
	EntryCount    int
	RegexpCount   int
	ContentSHA256 string
	FetchedAt     time.Time
}

// DestListContentUpdate is one list's new content within a batch.
type DestListContentUpdate struct {
	ListID int64
	DestListContent
}

// DestListBatch is a set of list writes that must land together: a geosite
// update touches many lists derived from one data set, and a half-applied one
// would leave lists on two versions of the data.
type DestListBatch struct {
	// Contents replace entries (and stamp the fetch) of the lists named.
	Contents []DestListContentUpdate
	// Release, when set, is recorded as the geosite release the lists now
	// reflect, in the same transaction.
	Release string
	// Decide is called INSIDE the transaction, after the policies in force
	// have been read there, so the verdict is about the rows the write is
	// about to sit beside and not about a read taken before a concurrent
	// policy change. inUse is the set of list ids a non-off policy
	// references: only a change to one of them advances the generation,
	// because only then does the compiler see a different policy (F13).
	// It returns last_error notes to record on lists (entries untouched),
	// whether to advance the generation whatever changed (a change the lists
	// do not show: a category a custom list references), or an error that
	// aborts the whole batch.
	Decide func(inUse map[int64]bool, inForce []*DestPolicy) (notes map[int64]string, forceBump bool, err error)
}

// DestPolicy is one rule of the access-control policy.
type DestPolicy struct {
	ID          int64
	Name        string
	Description string
	Action      DestAction
	State       DestState
	ListIDs     []int64
	Inline      DestPolicyInline
	Scope       DestScope
	// GroupIDs is read when Scope is groups, PanelIDs when it is panels.
	GroupIDs []int64
	PanelIDs []int64
	// Priority orders policies of one action: lower first. The service
	// assigns it; it is never an admin-typed number.
	Priority     int
	CountsAsRisk bool
	// TemplateKey is the catalog template this policy was created from ("" for
	// a hand-made one).
	TemplateKey string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DestExemption makes one user immune to every destination rule.
type DestExemption struct {
	UserID    int64
	Reason    string
	CreatedBy int64
	CreatedAt time.Time
	// ExpiresAt is nil for a standing exemption.
	ExpiresAt *time.Time
}

// DestSourceRule denies connections to one node (inbound) by the client's
// country or address. It is not a policy: it says who the inbound is open to,
// not where a user may go, so it sits in front of everything else.
type DestSourceRule struct {
	NodeID int64
	// Countries are upper-case ISO 3166-1 alpha-2 codes, sorted.
	Countries []string
	// CIDRs are canonical (masked) prefixes, sorted.
	CIDRs     []string
	State     DestState
	UpdatedAt time.Time
}

// DestPublishError is why the last publish was refused: kind is the limit that
// was exceeded ("domains", "regexps", "cidrs", "source_cidrs", "rules",
// "bytes", ...) with how much was used against what limit, or "invalid" with
// the offending field.
type DestPublishError struct {
	Kind  string `json:"kind"`
	Used  int    `json:"used,omitempty"`
	Limit int    `json:"limit,omitempty"`
	Field string `json:"field,omitempty"`
}

// DestPolicyState is the single row (id 1) that debounces publication: every
// definition write bumps Generation, and a publish makes
// PublishedGeneration catch up.
type DestPolicyState struct {
	Generation          int64
	PublishedGeneration int64
	FirstUnpublishedAt  *time.Time
	LastWriteAt         *time.Time
	PublishedAt         *time.Time
	Paused              bool
	// GeoIPSHA256 is the digest of the GeoIP database the policy was last
	// reconciled with ("" when there was none). It lives here, not in memory,
	// so a database that appeared, vanished or was replaced while the panel
	// was down is still noticed.
	// GeositeRelease is the geosite release tag the geosite lists were last
	// derived from, written in the same transaction as the lists: after a
	// crash it says whether an interrupted update committed.
	GeositeRelease string
	GeoIPSHA256    string
	PublishError   *DestPublishError
	PublishErrorAt *time.Time
}

// Unpublished reports whether a definition changed since the last publish.
func (s DestPolicyState) Unpublished() bool { return s.Generation > s.PublishedGeneration }

// DestPolicySnapshot is one published canonical serialisation of every
// definition the compiler reads.
type DestPolicySnapshot struct {
	Generation int64
	Body       []byte
	CreatedAt  time.Time
}

// DestFallbackReason says why a node runs the previous policy instead of the
// current one.
type DestFallbackReason string

const (
	DestFallbackNone      DestFallbackReason = ""
	DestFallbackRejected  DestFallbackReason = "rejected"
	DestFallbackSniffing  DestFallbackReason = "sniffing"
	DestFallbackOverLimit DestFallbackReason = "over_limit"
)

// DestAgentPolicy is the per-node-agent state of policy delivery: what PSP
// wants the node to run, what it minted, what the node reported, and the last
// policy known to have applied (the fallback target).
type DestAgentPolicy struct {
	AgentID            string
	DesiredSHA256      string
	MintedSHA256       string
	MintedAt           *time.Time
	FallbackReason     DestFallbackReason
	RejectedGeneration int64
	// OverLimit is {kind, used, limit} as JSON, "" when not over a limit.
	OverLimit string
	// PrecheckListeners is the JSON list of listeners whose sniffing is
	// insufficient, "" when none.
	PrecheckListeners string
	AppliedSHA256     string
	// AppliedBody is the canonical policy that last applied (the LKG).
	AppliedBody      []byte
	AppliedAt        *time.Time
	AppliedRuleCount int
	AppliedGroups    []int64
	// AppliedCollect is the recording level of the policy in AppliedBody: what
	// the node runs while a newer minted policy is on its way to it.
	AppliedCollect    string
	CollectEffective  string
	ReportedSHA256    string
	ReportedState     string
	ReportedIssue     string
	ReportedListeners string
	ReportedAt        *time.Time
	// RejectedCaps is which of the three capabilities that gate delivery
	// (policy, hits, usage) the agent advertised when the rejection was
	// recorded, as a short token ("p", "ph", "phu"). A change of that set is
	// one of the three reasons to try the new policy again.
	RejectedCaps string
	// RejectedSHA256 is the digest of the policy the node refused, "" when no
	// refusal is being held. It is tracked apart from FallbackReason, which a
	// sniffing or over-limit episode overwrites: the refusal must outlive them.
	// MintedGeneration is the published generation the minted policy was minted
	// at; a refusal is recorded against THAT generation, not the one that
	// happens to be published when the report arrives.
	RejectedSHA256   string
	MintedGeneration int64
	// MintedCtx / RejectedCtx are a hash of what the node was running when the
	// policy was minted (engine, core version, inbound sniffing). A refusal is
	// recorded with the context it happened in: a change of it is a reason to
	// retry the refused policy even though its bytes are unchanged.
	MintedCtx   string
	RejectedCtx string
	UpdatedAt   time.Time
}

// DestAgentPolicyPatch is a column-level write to a DestAgentPolicy row: a nil
// field is left alone, a non-nil field is written, EVEN to its zero value (a
// pointer to a nil slice clears the last-known-good body). Several writers
// own disjoint column groups — the compiler (desired, minted, fallback), the
// report ingest (reported) and the last-known-good recorder (applied) — so a
// whole-row write would let one revert another.
type DestAgentPolicyPatch struct {
	DesiredSHA256      *string
	MintedSHA256       *string
	MintedAt           *time.Time
	FallbackReason     *DestFallbackReason
	RejectedGeneration *int64
	OverLimit          *string
	PrecheckListeners  *string
	AppliedSHA256      *string
	AppliedBody        *[]byte
	AppliedAt          *time.Time
	AppliedRuleCount   *int
	AppliedGroups      *[]int64
	AppliedCollect     *string
	CollectEffective   *string
	ReportedSHA256     *string
	ReportedState      *string
	ReportedIssue      *string
	ReportedListeners  *string
	ReportedAt         *time.Time
	RejectedCaps       *string
	RejectedSHA256     *string
	MintedGeneration   *int64
	MintedCtx          *string
	RejectedCtx        *string
}

// Valid reports whether r is one of the four stored reasons.
func (r DestFallbackReason) Valid() bool {
	switch r {
	case DestFallbackNone, DestFallbackRejected, DestFallbackSniffing, DestFallbackOverLimit:
		return true
	}
	return false
}

// ---- Naming rules ----

// DestNameMax / DestDescriptionMax mirror the column widths.
const (
	DestNameMax        = 128
	DestDescriptionMax = 512
)

// ValidDestName reports whether name is a usable list or policy name: trimmed,
// non-empty, within the column width.
func ValidDestName(name string) bool {
	return name != "" && name == strings.TrimSpace(name) && len(name) <= DestNameMax
}

// DestTemplateKeyMax mirrors the template_key column.
const DestTemplateKeyMax = 32

var destCountryRE = regexp.MustCompile(`^[A-Z]{2}$`)

// NormalizeDestCountries upper-cases, de-duplicates and sorts ISO alpha-2
// codes. The second result is false when any entry is not two ASCII letters.
func NormalizeDestCountries(in []string) ([]string, bool) {
	out := make([]string, 0, len(in))
	for _, c := range in {
		c = strings.ToUpper(strings.TrimSpace(c))
		if !destCountryRE.MatchString(c) {
			return nil, false
		}
		out = append(out, c)
	}
	slices.Sort(out)
	return slices.Compact(out), true
}

// EncodeDestPublishError / DecodeDestPublishError are the column form of a
// publish error: JSON, with NULL meaning none.
func EncodeDestPublishError(e *DestPublishError) *string {
	if e == nil {
		return nil
	}
	b, _ := json.Marshal(e)
	s := string(b)
	return &s
}

// DecodeDestPublishError reads the column form. A value that does not decode
// reads as "invalid" rather than as no error: an unreadable error must not
// look like a clean publish.
func DecodeDestPublishError(s *string) *DestPublishError {
	if s == nil || *s == "" {
		return nil
	}
	var e DestPublishError
	if err := json.Unmarshal([]byte(*s), &e); err != nil || e.Kind == "" {
		return &DestPublishError{Kind: "invalid", Field: "publish_error"}
	}
	return &e
}

// DestDefinitions is one consistent read of every definition the compiler
// publishes: the generation it was taken at, and the rows as they were then.
// Lists carry their entries (and no pasted text).
type DestDefinitions struct {
	Generation  int64
	Lists       []*DestList
	Policies    []*DestPolicy
	Exemptions  []*DestExemption
	SourceRules []*DestSourceRule
}

// ErrDestTemplateExists is a built-in rule group added a second time. It is an
// ErrAlreadyExists, kept distinct so the API can tell it from a policy name that
// is merely taken.
var ErrDestTemplateExists = fmt.Errorf("%w: template already added", ErrAlreadyExists)
