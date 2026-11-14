package destpolicy

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Service is the policy compiler and publisher.
type Service struct {
	defs      Definitions
	state     StateStore
	agents    AgentStore
	lists     ListExpander
	countries CountryExpander
	settings  SettingsReader
	members   GroupMemberCounter
	metrics   Metrics
	now       func() time.Time

	// pubMu makes publication single-flight: every sync of every node asks
	// whether a publish is due, and only one of them should do the work.
	pubMu sync.Mutex
	// failedGen is the generation whose publish the pre-publish validation
	// refused. The same generation is not retried on every sync of every node
	// (the validation expands every list): a new write advances the generation
	// and is tried; only an explicit publish tries the same one again.
	failedGen atomic.Int64

	// baseMu serialises building the expanded snapshot.
	baseMu sync.Mutex

	mu sync.Mutex
	// baseCache holds the expanded snapshot of the published generation.
	baseCache *cachedBase
	// agentMeta is the delivery state of each agent, as the database holds it
	// (without the last-known-good body). Reads come from here so an idle sync
	// touches no table; every write goes through Service and updates it.
	agentMeta map[string]*domain.DestAgentPolicy
	// compiled is each agent's last result, valid while its key still matches.
	compiled map[string]*compiledEntry
	// digests remembers which rule-data version produced a digest, to tell an
	// admin which version a node runs from the digest it reports.
	digests map[string]digestInfo
	// bodyFailures counts consecutive failed reads of an agent's LKG.
	bodyFailures map[string]*bodyFailure
	// lastCore is the last non-empty observed core version per agent.
	lastCore map[string]string
}

type cachedBase struct {
	gen  int64
	doc  *snapshotDoc
	base *base
}

// digestInfo says what a policy digest was built from.
type digestInfo struct {
	generation int64
	geosite    string
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Definitions == nil || d.State == nil || d.Agents == nil || d.Lists == nil || d.Countries == nil {
		return nil, errors.New("destpolicy: definitions, state, agents, lists and countries are required")
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		defs: d.Definitions, state: d.State, agents: d.Agents, lists: d.Lists, countries: d.Countries,
		settings: d.Settings, members: d.GroupMembers, metrics: d.Metrics, now: now,
		agentMeta: map[string]*domain.DestAgentPolicy{}, compiled: map[string]*compiledEntry{},
		digests: map[string]digestInfo{},
	}, nil
}

func (s *Service) countCompile(result string) {
	if s.metrics.Compile != nil {
		s.metrics.Compile(result)
	}
}

// policyHasCapability reports whether caps carry the destination-policy
// capability.
func policyHasCapability(caps []string) bool {
	for _, c := range caps {
		if c == protocol.CapabilityDestinationPolicy {
			return true
		}
	}
	return false
}

// capsToken is the three capabilities that gate delivery, as a short token:
// "p" policy, "h" hits, "u" usage, "g" groups, "r" resolve. A change of the set is a reason to try a
// refused policy again; any other capability change is not (a node's task
// capabilities come and go).
func capsToken(caps []string) string {
	var tok []byte
	for _, c := range caps {
		if c == protocol.CapabilityDestinationPolicy {
			tok = append(tok, 'p')
		}
	}
	for _, c := range caps {
		if c == capabilityAuditHits {
			tok = append(tok, 'h')
		}
	}
	for _, c := range caps {
		if c == capabilityAuditUsage {
			tok = append(tok, 'u')
		}
	}
	for _, c := range caps {
		if c == capabilityDestinationGroups {
			tok = append(tok, 'g')
		}
	}
	for _, c := range caps {
		if c == capabilityDestinationResolve {
			tok = append(tok, 'r')
		}
	}
	return string(tok)
}

// capabilityDestinationGroups is the capability a node declares when it runs
// group rules (g*) as the wire specification says (on sing-box, with the
// protocol blocks ahead of a group's allows). Group rules are sent only to a
// node that declares it.
const capabilityDestinationGroups = protocol.CapabilityDestinationGroups

const (
	capabilityAuditHits  = protocol.CapabilityAuditHits
	capabilityAuditUsage = protocol.CapabilityAuditUsage
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
