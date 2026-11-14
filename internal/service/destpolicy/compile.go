package destpolicy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// PSP-side issue codes. The first two are the protocol's (the node reports
// them too); the other two exist only on the panel side.
const (
	IssueRejected          = protocol.IssueDestinationPolicyRejected
	IssueSniffInsufficient = protocol.IssueDestinationPolicySniffingInsufficient
	IssueOverLimit         = "destination_policy_over_limit"
	IssueLKGRejected       = "destination_policy_lkg_rejected"
	IssueLKGUnreadable     = "destination_policy_lkg_unreadable"
	// IssueReportOnlyNotDeployed says report-only rules were left out of what the
	// node is sent because they cannot be deployed there (sniffing, or a limit),
	// while the enforced rules are untouched.
	IssueReportOnlyNotDeployed = "destination_policy_report_only_not_deployed"
	xrayEngine                 = "xray"
	sniffMalformed             = "malformed"
)

// CompileInput is what one node's compile reads. Capabilities are the ones the
// node advertised in THIS report, not the ones stored on its row: a capability
// is a current fact, and the row can be a sync behind.
type CompileInput struct {
	PanelID      int64
	AgentID      string
	Capabilities []string
	Snapshot     *ports.NativeDesiredSnapshot
	// Config is the config the node is about to be sent (without a policy):
	// its core engine, which decides whether sniffing is prechecked.
	Config protocol.ConfigBody
	// CoreVersion is the core release the node reports it runs. The config's
	// selection says what it is asked to run; a catalog-driven resolution can
	// change the observed one with the selection untouched.
	CoreVersion string
	// AuditCollect is the panel's recording choice ("" is the default, hits).
	// The level the node is told to run is the lower of it and the node's
	// capabilities (EffectiveCollect).
	AuditCollect domain.AuditCollect
	// DestResolve is the panel's choice of how hostnames meet IP rules ("" is
	// as_is). The mode the node is told to run is EffectiveResolve of it and the
	// node's capabilities, held at as_is by an inbound that cannot run on_demand.
	DestResolve domain.DestResolve
}

// Result is a compile's answer.
type Result struct {
	// Policy is what goes into ConfigBody.Policy: nil for no policy.
	Policy *protocol.DestinationPolicy
	// Digest is PolicyDigest(Policy), "" for nil. Equal digests are equal
	// policies, which is what lets a caller cache anything derived from the
	// output (the config's canonical bytes) without comparing megabytes.
	Digest string
	// Issues are PSP-side issues to record against the agent: they are the
	// reasons the node does not run the new policy.
	Issues []domain.NodeAgentIssue
}

type compiledEntry struct {
	// uncached marks a result that must not be reused: it was decided under a
	// failure that is expected to pass.
	uncached bool
	key      string
	res      Result
	desire   string
}

// sniffFinding is one inbound whose sniffing cannot supply what the policy
// needs.
type sniffFinding struct {
	Listener string `json:"listener"`
	NodeID   int64  `json:"node_id"`
	Label    string `json:"label,omitempty"`
	Reason   string `json:"reason"`
}

// Compile produces the policy for one node. It is called on every sync of every
// node, so the common case — nothing changed — answers from memory: one
// single-row read of the publish state, no table read.
//
// The order of precedence: no capability, then paused, then "nothing in force",
// each answering nil. Only then are the node's own circumstances weighed:
// a policy too large for this node, a policy the node refused. In each of
// those the node keeps running the last policy that applied, trimmed to what is
// still relaxing (see fallbackPolicy), or no policy when there is none; a policy
// is never truncated to fit, because a truncated block list is a silent allow.
//
// Inbounds whose sniffing cannot supply the domain and protocol rules are not
// one of those: the node is sent the policy with those rules restricted to the
// inbounds that can (see restrictForSniffing), and the inbounds are reported.
func (s *Service) Compile(ctx context.Context, in CompileInput) (Result, error) {
	if !policyHasCapability(in.Capabilities) {
		return Result{}, nil
	}
	st, err := s.ensurePublished(ctx)
	if err != nil {
		return Result{}, err
	}
	now := s.now()
	meta, err := s.metaFor(ctx, in.AgentID)
	if err != nil {
		return Result{}, err
	}
	var cb *cachedBase
	for attempt := 0; ; attempt++ {
		if st.Paused || st.PublishedGeneration == 0 {
			return s.noPolicy(ctx, in, meta, "", "", now)
		}
		if cb, err = s.baseFor(ctx, st.PublishedGeneration); errors.Is(err, domain.ErrNotFound) && attempt == 0 {
			// The generation this compile read was superseded and its snapshot
			// pruned while it ran: read what is published now, and judge it from
			// the top (it may be paused or empty).
			if st, err = s.state.Get(ctx); err != nil {
				return Result{}, err
			}
			continue
		}
		break
	}
	if err != nil {
		return Result{}, err
	}
	if cb.doc.empty() {
		return s.noPolicy(ctx, in, meta, "", "", now)
	}

	view := nodeView{panelID: in.PanelID, nodeIDs: map[int64]bool{}, groupOf: nil, groupCap: slices.Contains(in.Capabilities, capabilityDestinationGroups)}
	var nodes []*domain.Node
	if in.Snapshot != nil {
		for _, n := range in.Snapshot.Nodes {
			if n != nil {
				view.nodeIDs[n.ID] = true
				nodes = append(nodes, n)
			}
		}
		seen := map[int64]bool{}
		for _, c := range in.Snapshot.Clients {
			if c.Client != nil && !seen[c.Client.UserID] {
				seen[c.Client.UserID] = true
				view.users = append(view.users, c.Client.UserID)
			}
		}
	}
	slices.Sort(view.users)
	if cb.base.needsGroups(view.groupCap) {
		if view.groupOf, err = s.defs.GroupIDsByUserIDs(ctx, view.users); err != nil {
			return Result{}, fmt.Errorf("destpolicy: read the roster's groups: %w", err)
		}
	}
	if view.groupCap && s.members != nil {
		// The per-node limit counts the members the panel's tags select, not the
		// roster (which shrinks when members are made ineligible): see
		// groupSubjectSlack.
		// Every allowlist group, with or without a member on the roster: when
		// the limit itself makes the panel ineligible all the members leave the
		// roster, and a count that needed one would then see the limit satisfied
		// and let them back in, on every sync.
		for _, g := range cb.base.groups {
			n, err := s.members.TagMatchedMembers(ctx, in.PanelID, g.id)
			if err != nil {
				return Result{}, fmt.Errorf("destpolicy: count the members of group %d: %w", g.id, err)
			}
			if n == 0 {
				continue // the panel's tags select none of its members
			}
			if view.tagMatched == nil {
				view.tagMatched = map[int64]int{}
			}
			view.tagMatched[g.id] = n
		}
	}
	engine := strings.ToLower(strings.TrimSpace(in.Config.Core.Engine))
	if engine == "" {
		engine = xrayEngine
	}
	collect := EffectiveCollect(in.AuditCollect, in.Capabilities)
	resolve := EffectiveResolve(in.DestResolve, in.Capabilities)
	// An empty observed core version (a report that left it out) is not a
	// change: the last non-empty one stands.
	in.CoreVersion = s.observedCore(in.AgentID, in.CoreVersion)
	key := compileKey(st.PublishedGeneration, in.Capabilities, engine, collect, resolve, view, nodes, meta, refusalContext(engine, in.Config.Core.Version, in.CoreVersion, nodes))

	s.mu.Lock()
	cached := s.compiled[in.AgentID]
	s.mu.Unlock()
	if cached != nil && cached.key == key {
		s.countCompile(ResultCacheHit)
		return cached.res, nil
	}

	started := time.Now()
	res, entry, err := s.compileFresh(ctx, in, meta, cb, view, nodes, engine, collect, resolve, st, now)
	if err != nil {
		return Result{}, err
	}
	// The key is taken again now: compiling may have written the agent's
	// delivery state (a fallback set or cleared), and the key includes it, so
	// the key from before the write would never match the next sync and every
	// state change would cost a second compile.
	if after, merr := s.metaFor(ctx, in.AgentID); merr == nil {
		key = compileKey(st.PublishedGeneration, in.Capabilities, engine, collect, resolve, view, nodes, after, refusalContext(engine, in.Config.Core.Version, in.CoreVersion, nodes))
	}
	entry.key = key
	if !entry.uncached {
		s.mu.Lock()
		s.compiled[in.AgentID] = entry
		s.mu.Unlock()
	}
	if s.metrics.CompileDuration != nil {
		s.metrics.CompileDuration(time.Since(started))
	}
	return res, nil
}

// noPolicy is the nil answer for a node that is paused or has nothing in
// force. It clears a fallback (there is nothing left to fall back from) and
// records that nothing is minted, but it never touches the last-known-good:
// pausing must not rewrite the record of what the node last applied. shed is
// why report-only rules were left out when that is what emptied the policy
// ("" for a pause or for nothing in force). precheck is the JSON list of
// inbounds whose sniffing could not supply a rule that was dropped for it ("" for
// none): with every rule dropped the node gets nothing, and the status still says
// which inbounds to fix.
func (s *Service) noPolicy(ctx context.Context, in CompileInput, meta *domain.DestAgentPolicy, shed, precheck string, now time.Time) (Result, error) {
	var patch domain.DestAgentPolicyPatch
	empty := ""
	if meta.DesiredSHA256 != "" || meta.MintedSHA256 != "" {
		patch.DesiredSHA256, patch.MintedSHA256 = &empty, &empty
	}
	if meta.CollectEffective != "" {
		// Nothing is minted, so nothing is being recorded.
		patch.CollectEffective = &empty
	}
	if meta.ResolveEffective != "" {
		patch.ResolveEffective = &empty
	}
	if meta.ResolveUnavailable != "" {
		patch.ResolveUnavailable = &empty
	}
	if meta.ReportOnlyShed != shed {
		// Nothing is sent; what was left out of it is still worth showing when
		// leaving it out is why nothing is sent.
		patch.ReportOnlyShed = &shed
	}
	if meta.FallbackReason != domain.DestFallbackNone || meta.OverLimit != "" {
		none := domain.DestFallbackNone
		patch.FallbackReason, patch.OverLimit = &none, &empty
	}
	if meta.PrecheckListeners != precheck {
		patch.PrecheckListeners = &precheck
	}
	if err := s.patchMeta(ctx, in.AgentID, patch); err != nil {
		return Result{}, err
	}
	s.mu.Lock()
	delete(s.compiled, in.AgentID)
	s.mu.Unlock()
	s.countCompile(ResultCacheHit)
	return Result{}, nil
}

// compileFresh is a compile that missed the cache.
func (s *Service) compileFresh(ctx context.Context, in CompileInput, meta *domain.DestAgentPolicy, cb *cachedBase,
	view nodeView, nodes []*domain.Node, engine string, collect protocol.CollectLevel, resolve protocol.ResolveMode, st *domain.DestPolicyState, now time.Time) (Result, *compiledEntry, error) {

	desired := cb.base.assemble(view)
	// Recording is part of the desired policy, so it is part of its digest: a
	// panel that switches recording off is a new policy for the node.
	withCollect(desired, collect)
	// How hostnames meet the IP rules is part of the desired policy too, set only
	// where a rule has IP rules; an inbound that cannot run it holds the node at
	// as_is below, once the rules are final.
	withResolve(desired, resolve)
	// REPORT-ONLY MUST NOT DISTURB ENFORCEMENT: observe rules that cannot be
	// deployed on this node (an inbound's sniffing cannot supply them, or they
	// are what overruns the budget) are left out, and the enforced rules go on
	// exactly as they would without them.
	var shedNote []domain.NodeAgentIssue
	// desiredShed is why the DESIRED policy lost its report-only rules; what the
	// node is actually sent may be a fallback, so the status field is computed
	// from the output below, never from this.
	desiredShed := ""
	// INBOUNDS THAT CANNOT SNIFF: the findings name the inbounds that cannot run
	// the ENFORCED domain and protocol rules (report-only ones are told apart by
	// report_only_not_deployed); the policy is then restricted, not held back.
	var findings []sniffFinding
	if engine == xrayEngine {
		// The node's own check, per listener, on the enforced rules that apply to it.
		findings = perListenerSniffFindings(enforcedOnly(desired), nodes)
	}
	// Whether the unrestricted policy was inside the limits: restricting adds
	// explicit inbounds, and an over-limit it causes says so.
	unrestrictedOver := desired != nil && usageOf(desired).over() != nil
	desired, restricted := restrictForSniffing(desired, nodes, engine)
	if restricted.observe {
		desiredShed = shedSniffing
		shedNote = []domain.NodeAgentIssue{{Code: IssueReportOnlyNotDeployed, Detail: reportOnlyNote(shedSniffing)}}
	}
	slack := groupSubjectSlack(desired, cb.base.groups, view.tagMatched)
	if shed, why, ok := shedObserve(desired, slack); ok {
		desired, desiredShed = shed, why
		shedNote = []domain.NodeAgentIssue{{Code: IssueReportOnlyNotDeployed, Detail: reportOnlyNote(why)}}
	}
	// XRAY'S domainStrategy IS ROUTER-WIDE: an inbound whose sniffing would
	// substitute the name the router resolves (routeOnly:true) holds the WHOLE
	// node at as_is. It is a downgrade, not a restriction: no rule is dropped or
	// scoped, and the node is flagged.
	desiredResolveHeld := settleResolve(desired, nodes, engine)
	if desired == nil && slack > 0 && (Usage{Subjects: slack}).over() != nil {
		// Nothing is on the roster to carry rules, but the members its tags
		// select would carry more than the limit allows. Answering "no policy"
		// here would clear the over-limit that made the panel ineligible, and
		// the members would come back and push it over again, every sync.
		desired = &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{}}
	}
	desiredDigest := protocol.PolicyDigest(desired)
	precheckJSON := ""
	if len(findings) > 0 {
		b, _ := json.Marshal(findings)
		precheckJSON = string(b)
	}
	if desired == nil {
		res, err := s.noPolicy(ctx, in, meta, desiredShed, precheckJSON, now)
		res.Issues = append(res.Issues, sniffIssues(findings)...)
		res.Issues = append(res.Issues, shedNote...)
		return res, &compiledEntry{res: res}, err
	}
	s.rememberDigest(desiredDigest, cb)

	usage := usageOf(desired)
	usage.Subjects += slack
	var overLimit *domain.DestPublishError
	if o := usage.over(); o != nil {
		overLimit = o
	}

	capsNow := capsToken(in.Capabilities)
	// A refusal is held in RejectedSHA256, apart from FallbackReason: a
	// sniffing or over-limit episode overwrites the reason, and must not erase
	// a refusal the node has not yet had a reason to retry.
	rejectedHeld := meta.RejectedSHA256 != ""
	// A newer generation is a reason to retry only if it changed what would be
	// sent: when the desired policy is the very one the node refused (and the
	// capabilities are as they were), it would be refused again, so only the
	// generation the refusal is recorded against advances.
	//
	// The refusal is also tied to the context it happened in (engine, core
	// version, inbound sniffing): the node that refused D because of its core
	// may accept D after the operator changes that, with D's bytes unchanged,
	// so a changed context is a reason to retry too.
	genNewer := st.PublishedGeneration > meta.RejectedGeneration
	ctxNow := refusalContext(engine, in.Config.Core.Version, in.CoreVersion, nodes)
	retryDue := rejectedHeld &&
		(capsNow != meta.RejectedCaps || ctxNow != meta.RejectedCtx || (genNewer && desiredDigest != meta.RejectedSHA256))

	reason := domain.DestFallbackNone
	switch {
	case overLimit != nil:
		reason = domain.DestFallbackOverLimit
	case rejectedHeld && !retryDue:
		reason = domain.DestFallbackRejected
	}

	var patch domain.DestAgentPolicyPatch
	setReason := func(r domain.DestFallbackReason) { patch.FallbackReason = &r }
	overJSON := ""
	if overLimit != nil {
		b, _ := json.Marshal(overLimit)
		overJSON = string(b)
	}
	if reason != meta.FallbackReason {
		setReason(reason)
	}
	if overJSON != meta.OverLimit {
		patch.OverLimit = &overJSON
	}
	if precheckJSON != meta.PrecheckListeners {
		patch.PrecheckListeners = &precheckJSON
	}
	mintGen := st.PublishedGeneration
	if retryDue {
		none := ""
		patch.RejectedSHA256 = &none
	} else if rejectedHeld && genNewer {
		g := st.PublishedGeneration
		patch.RejectedGeneration = &g
	}

	result := ResultCompiled
	unreadable, lkgDropped := false, false
	var fallbackIssues []domain.NodeAgentIssue
	var fallbackResolveHeld []sniffFinding
	var output *protocol.DestinationPolicy
	if reason == domain.DestFallbackNone {
		err := protocol.ValidateDestinationPolicy(desired)
		if err == nil && engine == xrayEngine && len(perListenerSniffFindings(desired, nodes)) > 0 {
			// The node checks sniffing per listener on the rules that apply to it;
			// a restricted policy that fails the same check is a compiler bug too.
			err = errors.New("a restricted policy still needs sniffing an inbound cannot supply")
		}
		if err != nil {
			// A node's policy that does not validate here is a bug in the
			// compiler, not an admin error: the pre-publish validation judged
			// the upper bound, and what is left is only a subset of it.
			// Deliver the fallback and remember it as refused, so the next
			// attempt waits for a new generation.
			log.Error("destpolicy: a compiled policy does not validate; falling back", "agent_id", in.AgentID, "err", err)
			reason, result = domain.DestFallbackRejected, ResultInvalid
			setReason(reason)
			gen, caps := st.PublishedGeneration, capsNow
			patch.RejectedGeneration, patch.RejectedCaps, patch.RejectedSHA256 = &gen, &caps, &desiredDigest
			patch.RejectedCtx = &ctxNow
		} else {
			output = desired
		}
	}
	fallbackShed := ""
	if reason != domain.DestFallbackNone {
		// The desired policy is not what goes out, so its shedding note is not
		// true of this node; only what the fallback itself sheds is.
		shedNote = nil
		var ferr error
		if s.bodyReadBackedOff(bodyKey(in.AgentID, meta.AppliedSHA256), now) {
			output, unreadable = nil, true
		} else if output, ferr = s.fallbackPolicy(ctx, in.AgentID, meta, cb, view, collect, resolve); ferr != nil {
			// A transient read error must not become "no policy": fail the
			// sync, cache nothing, and the node keeps what it has. But a read
			// that keeps failing cannot freeze the node (and its roster) for
			// ever: past the bound the fallback is nil, which is always
			// deployable, with an issue that says why, and the result is not
			// cached so the next sync reads again.
			if !s.bodyReadGaveUp(bodyKey(in.AgentID, meta.AppliedSHA256), now) {
				return Result{}, nil, ferr
			}
			output, unreadable = nil, true
		} else if !unreadable {
			s.bodyReadOK(bodyKey(in.AgentID, meta.AppliedSHA256))
		}
		// The fallback is restricted the same way as the desired policy would be:
		// the inbounds' sniffing may have changed since it applied. Report-only
		// rules that lost an inbound for it are the one thing said about it; if
		// even then it does not pass the node's check, send nothing (not a
		// refusal by the node, so the LKG record stays).
		if output != nil && engine == xrayEngine {
			var info sniffRestriction
			output, info = restrictForSniffing(output, nodes, engine)
			if info.observe {
				fallbackShed = shedSniffing
				shedNote = append(shedNote, domain.NodeAgentIssue{Code: IssueReportOnlyNotDeployed, Detail: reportOnlyNote(shedSniffing)})
			}
			// Restricting adds inbounds and can split a rule, so the result is
			// judged again: over a limit, invalid, or still needing sniffing an
			// inbound cannot supply, it is nil, which is always deployable.
			// The inbounds' sniffing may have changed since the LKG applied: one that
			// cannot run on_demand now holds it at as_is, as for the desired policy.
			fallbackResolveHeld = settleResolve(output, nodes, engine)
			if output != nil {
				switch verr := protocol.ValidateDestinationPolicy(output); {
				case verr != nil:
					log.Warn("destpolicy: the restricted fallback does not validate; sending none", "agent_id", in.AgentID, "err", verr)
					output, fallbackShed = nil, ""
				case usageOf(output).over() != nil:
					log.Warn("destpolicy: the restricted fallback is over a limit; sending none", "agent_id", in.AgentID)
					output, fallbackShed = nil, ""
				case len(perListenerSniffFindings(output, nodes)) > 0:
					log.Error("destpolicy: a restricted fallback still needs sniffing an inbound cannot supply; sending none", "agent_id", in.AgentID)
					output, fallbackShed = nil, ""
				}
			}
		}
		if result != ResultInvalid {
			switch reason {
			case domain.DestFallbackRejected:
				result = ResultFallbackReject
			default:
				result = ResultFallbackLimit
			}
		}
		if output == nil && result != ResultInvalid {
			result = ResultFallbackNil
		}
	}

	// THE INVARIANT, at the one place every path passes through: a digest the
	// node refused is never minted again until a retry is due. Any path that
	// arrives at the refused bytes — the desired policy, the recorded LKG, or an
	// LKG trimmed back to exactly the desired policy (only removals since it
	// applied) — mints nil instead. If the bytes came from the LKG, the record is
	// dropped too, or the same fallback would be rebuilt next sync.
	outDigest := protocol.PolicyDigest(output)
	if rejectedHeld && !retryDue && outDigest != "" && outDigest == meta.RejectedSHA256 {
		if reason != domain.DestFallbackNone {
			empty, none := "", []byte{}
			zero, noGroups := 0, []int64{}
			patch.AppliedSHA256, patch.AppliedBody, patch.AppliedRuleCount, patch.AppliedGroups = &empty, &none, &zero, &noGroups
			patch.AppliedCollect = &empty
			lkgDropped = true
		}
		output, outDigest = nil, ""
		// Nothing is sent because of the refusal, not because report-only rules
		// were left out of it.
		fallbackShed, desiredShed, shedNote = "", "", nil
		if result != ResultInvalid {
			result = ResultFallbackNil
		}
	}
	if outDigest != "" {
		s.rememberDigest(outDigest, cb)
	}
	// What the node is told to record, as delivered: empty when the policy it
	// gets records nothing, whatever the panel's setting says.
	outCollect := ""
	if output != nil {
		outCollect = string(output.Collect)
	}
	if outCollect != meta.CollectEffective {
		patch.CollectEffective = &outCollect
	}
	// The mode as delivered, and why a panel that asked for on_demand did not get
	// it (an inbound's sniffing): the status page's resolve_effective and
	// resolve_unavailable. Only what the sent policy was held back by counts.
	outResolve, resolveHeld := "", []sniffFinding(nil)
	if output != nil {
		outResolve = string(output.Resolve)
		resolveHeld = desiredResolveHeld
		if reason != domain.DestFallbackNone {
			resolveHeld = fallbackResolveHeld
		}
	}
	if outResolve != meta.ResolveEffective {
		patch.ResolveEffective = &outResolve
	}
	outUnavailable := ""
	if len(resolveHeld) > 0 {
		outUnavailable = resolveRouteOnly
	}
	if outUnavailable != meta.ResolveUnavailable {
		patch.ResolveUnavailable = &outUnavailable
	}
	// Why report-only rules were left out of what this node is sent, as the
	// status page shows it (cleared when nothing was).
	sentShed := ""
	switch {
	case reason == domain.DestFallbackNone:
		sentShed = desiredShed
	default:
		sentShed = fallbackShed
	}
	if sentShed != meta.ReportOnlyShed {
		patch.ReportOnlyShed = &sentShed
	}
	if desiredDigest != meta.DesiredSHA256 {
		patch.DesiredSHA256 = &desiredDigest
	}
	if outDigest != meta.MintedSHA256 {
		patch.MintedSHA256, patch.MintedAt = &outDigest, &now
	}
	if ctxNow != meta.MintedCtx {
		patch.MintedCtx = &ctxNow
	}
	if outDigest != meta.MintedSHA256 || retryDue || mintGen > meta.MintedGeneration {
		// The generation this mint belongs to: a refusal of it is recorded
		// against THIS number, not whichever is published when it arrives.
		patch.MintedGeneration = &mintGen
	}
	if err := s.patchMeta(ctx, in.AgentID, patch); err != nil {
		return Result{}, nil, err
	}

	res := Result{Policy: output, Digest: outDigest}
	res.Issues = s.issuesFor(reason, meta, desiredDigest, overLimit)
	if overLimit != nil && restricted.changed && !unrestrictedOver {
		for i := range res.Issues {
			if res.Issues[i].Code == IssueOverLimit {
				res.Issues[i].Detail += " after restricting rules to the inbounds that can sniff"
			}
		}
	}
	res.Issues = append(res.Issues, sniffIssues(findings)...)
	res.Issues = append(res.Issues, fallbackIssues...)
	res.Issues = append(res.Issues, shedNote...)
	res.Issues = append(res.Issues, resolveIssues(resolveHeld)...)
	if lkgDropped {
		res.Issues = append(res.Issues, domain.NodeAgentIssue{Code: IssueLKGRejected, Detail: "the fallback equals the refused policy"})
	}
	if unreadable {
		res.Issues = append(res.Issues, domain.NodeAgentIssue{Code: IssueLKGUnreadable, Detail: "the last known good policy could not be read"})
	}
	s.countCompile(result)
	return res, &compiledEntry{res: res, desire: desiredDigest, uncached: unreadable}, nil
}

func (s *Service) rememberDigest(digest string, cb *cachedBase) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.digests) > 4096 {
		clear(s.digests)
	}
	s.digests[digest] = digestInfo{generation: cb.gen, geosite: cb.base.geosite}
}

// issuesFor are the PSP-side issues a node's current state raises. They are
// re-raised on every compile (recording an issue again only advances its
// last-seen time), so they stay open exactly as long as the condition does.
// Nothing in them names a user or a destination.
func (s *Service) issuesFor(reason domain.DestFallbackReason, meta *domain.DestAgentPolicy, desiredDigest string,
	over *domain.DestPublishError) []domain.NodeAgentIssue {
	short := desiredDigest
	if len(short) > 12 {
		short = short[:12]
	}
	switch reason {
	case domain.DestFallbackOverLimit:
		// No live count in the detail: an issue is identified by its detail, and a
		// number that moves with every roster change would open a new issue each
		// time. The figures are in the status.
		return []domain.NodeAgentIssue{{Code: IssueOverLimit, Detail: over.Kind + " over limit"}}
	case domain.DestFallbackRejected:
		if meta.ReportedIssue == IssueSniffInsufficient && meta.ReportedListeners != "" {
			var keys []string
			if json.Unmarshal([]byte(meta.ReportedListeners), &keys) == nil && len(keys) > 0 {
				out := make([]domain.NodeAgentIssue, 0, len(keys))
				for _, k := range keys {
					out = append(out, domain.NodeAgentIssue{Code: IssueSniffInsufficient, Key: k, Detail: "reported by the node"})
				}
				return out
			}
		}
		return []domain.NodeAgentIssue{{Code: IssueRejected, Detail: "digest " + short}}
	}
	return nil
}

// sniffIssues are the inbounds that cannot run the policy's domain and protocol
// rules, one issue each. The node still runs everything else; the issue is what
// the status page flags.
func sniffIssues(findings []sniffFinding) []domain.NodeAgentIssue {
	out := make([]domain.NodeAgentIssue, 0, len(findings))
	for _, f := range findings {
		out = append(out, domain.NodeAgentIssue{Code: IssueSniffInsufficient, Key: f.Listener, Detail: f.Reason})
	}
	return out
}

// Why report-only rules were shed.
const (
	shedLimit    = "limit"
	shedSniffing = "sniffing"
)

func reportOnlyNote(why string) string {
	return "report-only rules not deployed on this node: " + why
}

// sheddable is a rule report-only shedding may take out: an observe rule that is
// not a group's. A group's rules are enforcement even when its catch-all only
// records: leaving the allows without their catch-all is not a valid policy, and
// a report group that cannot be deployed is the node's fallback, not a rule to
// drop.
func sheddable(r protocol.DestinationRule) bool {
	if r.Action != protocol.RuleObserve {
		return false
	}
	kind, _, ok := ruleOrigin(r.ID)
	return !ok || kind != 'g'
}

// groupSubjectSlack is what the per-node limit adds for allowlist groups on top
// of the subjects the policy carries: each group's members that its tags select
// but the roster does not hold, once per rule the group has (with no member on
// the roster the group has no rule in the policy, so its rules are counted from
// its lists). Counting the
// roster alone would let a node flip over and under the limit as members leave
// its roster for being ineligible and come back.
func groupSubjectSlack(p *protocol.DestinationPolicy, groups []baseGroup, tagMatched map[int64]int) int {
	if len(tagMatched) == 0 {
		return 0
	}
	held := map[int64]int{}
	if p != nil {
		for _, r := range p.Rules {
			if kind, n, ok := ruleOrigin(r.ID); ok && kind == 'g' && r.CatchAll {
				held[n] = len(r.Subjects)
			}
		}
	}
	slack := 0
	for _, g := range groups {
		tm, ok := tagMatched[g.id]
		if !ok || tm <= held[g.id] {
			continue
		}
		// The rules the group has, which depend on its lists and not on who is
		// on the roster: a group with no member on it has none in the policy.
		rules := 1
		for _, a := range []groupAllow{g.x1, g.x2} {
			if !a.empty() {
				rules++
			}
		}
		slack += (tm - held[g.id]) * rules
	}
	return slack
}

// enforcedOnly is p without its observe rules (nil when nothing else is left,
// as assemble has it), recording level re-derived because an observe rule is
// one of the things that records.
func enforcedOnly(p *protocol.DestinationPolicy) *protocol.DestinationPolicy {
	if p == nil {
		return nil
	}
	out := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{}, Exempt: slices.Clone(p.Exempt)}
	for _, r := range p.Rules {
		if !sheddable(r) {
			out.Rules = append(out.Rules, r)
		}
	}
	if len(out.Rules) == 0 {
		return nil
	}
	withCollect(out, p.Collect)
	// Without its observe rules the policy may have no IP rule left to resolve for.
	withResolve(out, p.Resolve)
	return out
}

// shedObserve drops p's observe rules when, and only when, they are what keeps
// the policy from being deployed on this node: a limit it passes only because of
// them. It reports ok=false (and p unchanged) when there is nothing to shed or
// when the enforced rules alone would still not deploy, which is today's
// fallback. (Sniffing is not a reason: see restrictForSniffing.)
func shedObserve(p *protocol.DestinationPolicy, extraSubjects int) (*protocol.DestinationPolicy, string, bool) {
	if p == nil || !slices.ContainsFunc(p.Rules, sheddable) {
		return p, "", false
	}
	usageOver := func(q *protocol.DestinationPolicy) bool {
		u := usageOf(q)
		u.Subjects += extraSubjects
		return u.over() != nil
	}
	if !usageOver(p) {
		return p, "", false
	}
	enf := enforcedOnly(p)
	if enf != nil && usageOver(enf) {
		return p, "", false
	}
	return enf, shedLimit, true
}

// compileKey is everything a node's compiled output depends on besides the
// agent's own delivery state. Two compiles with the same key give the same
// output, which is what lets the next sync skip the work.
// refusalContext hashes what a refusal depends on besides the policy's bytes:
// the engine, the core release and each enabled inbound's sniffing.
func refusalContext(engine, coreVersion, observedCore string, nodes []*domain.Node) string {
	h := sha256.New()
	fmt.Fprintf(h, "engine=%s;core=%s;observed=%s;", engine, coreVersion, observedCore)
	ordered := slices.Clone(nodes)
	slices.SortFunc(ordered, func(a, b *domain.Node) int { return int(a.ID - b.ID) })
	for _, n := range ordered {
		fmt.Fprintf(h, "n%d:%t:%s;", n.ID, n.Enabled, n.Sniffing)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func compileKey(gen int64, caps []string, engine string, collect protocol.CollectLevel, resolve protocol.ResolveMode, v nodeView, nodes []*domain.Node, meta *domain.DestAgentPolicy, refusalCtx string) string {
	h := sha256.New()
	fmt.Fprintf(h, "ctx=%s;", refusalCtx)
	// Emitted only for on_demand, so an as_is node's key is what it was.
	if resolve == protocol.ResolveOnDemand {
		fmt.Fprintf(h, "resolve=%s;", resolve)
	}
	fmt.Fprintf(h, "gen=%d;caps=%s;engine=%s;collect=%s;panel=%d;fb=%s;rg=%d;rc=%s;rs=%s;rx=%s;applied=%s;", gen, capsToken(caps), engine, collect, v.panelID,
		meta.FallbackReason, meta.RejectedGeneration, meta.RejectedCaps, meta.RejectedSHA256, meta.RejectedCtx, meta.AppliedSHA256)
	ids := make([]int64, 0, len(nodes))
	byID := map[int64]*domain.Node{}
	for _, n := range nodes {
		ids = append(ids, n.ID)
		byID[n.ID] = n
	}
	slices.Sort(ids)
	for _, id := range ids {
		fmt.Fprintf(h, "n%d:%t:%s;", id, byID[id].Enabled, byID[id].Sniffing)
	}
	for _, u := range v.users {
		fmt.Fprintf(h, "u%d:%d;", u, v.groupOf[u])
	}
	tm := make([]int64, 0, len(v.tagMatched))
	for gid := range v.tagMatched {
		tm = append(tm, gid)
	}
	slices.Sort(tm)
	for _, gid := range tm {
		fmt.Fprintf(h, "tm%d:%d;", gid, v.tagMatched[gid])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ---- the last-known-good fallback ----

// fallbackPolicy is the policy a node runs while it cannot run the new one: the
// last policy it applied (the "last known good"), TRIMMED. The trimming only
// ever loosens, so it never blocks anything the new definitions would allow.
// It is NOT free of new needs, though: the inbounds' sniffing may have changed
// since it applied, so Compile restricts the result for sniffing afterwards,
// which adds inbounds, and judges it again (validation and limits; failing
// either, nil is sent):
//
//   - a rule of a policy that has since been deleted, switched off, or scoped
//     away from this panel goes;
//   - a source rule goes when none of its inbounds still has one in force (and
//     the rule keeps only the inbounds that do);
//   - subjects that are no longer on this node's roster go, and a rule whose
//     subjects were all removed goes with them;
//   - a group's rules stay only for a node that runs group rules, while the
//     group is in force in the same state, and only for its current members;
//   - the exempt list is the union of the old one and the current one, both
//     limited to the roster.
//
// No LKG, an LKG that does not decode, or one that is invalid after trimming,
// is nil: no policy is always deployable and matches a node that is not
// enforcing, which is the truth.
func (s *Service) fallbackPolicy(ctx context.Context, agentID string, meta *domain.DestAgentPolicy, cb *cachedBase, v nodeView, collect protocol.CollectLevel, resolve protocol.ResolveMode) (*protocol.DestinationPolicy, error) {
	if meta.AppliedSHA256 == "" {
		return nil, nil
	}
	body, err := s.agents.AppliedBody(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("destpolicy: read the last-known-good policy: %w", err)
	}
	if len(body) == 0 {
		return nil, nil
	}
	var lkg protocol.DestinationPolicy
	if err := json.Unmarshal(body, &lkg); err != nil {
		return nil, nil
	}
	policies := map[int64]*snapPolicy{}
	for i := range cb.doc.Policy {
		policies[cb.doc.Policy[i].ID] = &cb.doc.Policy[i]
	}
	// The wire action each source rule has NOW: a node whose source rule went
	// from enforced to report-only (or back) is a different rule.
	sourceActions := map[int64]protocol.RuleAction{}
	for _, src := range cb.doc.Sources {
		sourceActions[src.Node] = protocol.RuleBlock
		if src.State == string(domain.DestStateReport) {
			sourceActions[src.Node] = protocol.RuleObserve
		}
	}
	roster := map[protocol.SubjectKey]bool{}
	for _, u := range v.users {
		roster[protocol.NewSubjectKey(u)] = true
	}
	// Allowlist groups in force now and the action each one's catch-all has on
	// the wire, against what the LKG ran.
	groupsNow := map[int64]protocol.RuleAction{}
	for _, g := range cb.doc.Groups {
		groupsNow[g.ID] = protocol.RuleBlock
		if g.State == string(domain.DestStateReport) {
			groupsNow[g.ID] = protocol.RuleObserve
		}
	}
	lkgCatchAll := map[int64]protocol.RuleAction{}
	for _, r := range lkg.Rules {
		if kind, n, ok := ruleOrigin(r.ID); ok && kind == 'g' && r.CatchAll {
			lkgCatchAll[n] = r.Action
		}
	}

	out := &protocol.DestinationPolicy{Rules: []protocol.DestinationRule{}}
	for _, r := range lkg.Rules {
		kind, n, ok := ruleOrigin(r.ID)
		if !ok {
			continue
		}
		switch kind {
		case 'p':
			p := policies[n]
			if p == nil || (p.Scope == string(domain.DestScopePanels) && !slices.Contains(p.Panels, v.panelID)) {
				continue
			}
			// A policy that has changed action, or moved between enforced and
			// report-only, is a different rule now.
			if r.Action != wireAction(p) {
				continue
			}
			if p.Scope == string(domain.DestScopeGroups) {
				// Only the CURRENT members of the policy's groups stay subject
				// to it; a rule that applied to everyone is narrowed to them.
				var members []protocol.SubjectKey
				for _, u := range v.users {
					if slices.Contains(p.Groups, v.groupOf[u]) {
						members = append(members, protocol.NewSubjectKey(u))
					}
				}
				if len(r.Subjects) == 0 {
					r.Subjects = members
				} else {
					r.Subjects = slices.DeleteFunc(slices.Clone(r.Subjects), func(k protocol.SubjectKey) bool { return !slices.Contains(members, k) })
				}
				if len(r.Subjects) == 0 {
					continue
				}
				sortKeys(r.Subjects)
			}
		case 'g':
			// A group stays only for a node that runs group rules, while it is
			// still in force in the same state; its subjects are the LKG's that
			// are still its members on the roster. Every rule of a group has the
			// same subjects, so the group is kept or dropped whole.
			now, in := groupsNow[n]
			if !v.groupCap || !in || lkgCatchAll[n] != now {
				continue
			}
			var keep []protocol.SubjectKey
			for _, k := range r.Subjects {
				if id, err := k.RowID(); err == nil && v.groupOf[id] == n {
					keep = append(keep, k)
				}
			}
			if len(keep) == 0 {
				continue
			}
			r.Subjects = keep
		case 's':
			var keep []protocol.ListenerKey
			for _, k := range r.Inbounds {
				if id, err := k.RowID(); err == nil && sourceActions[id] == r.Action && v.nodeIDs[id] {
					keep = append(keep, k)
				}
			}
			if len(keep) == 0 {
				continue
			}
			r.Inbounds = keep
		default:
			continue
		}
		if len(r.Subjects) > 0 {
			var keep []protocol.SubjectKey
			for _, subject := range r.Subjects {
				if roster[subject] {
					keep = append(keep, subject)
				}
			}
			if len(keep) == 0 {
				continue
			}
			r.Subjects = keep
		}
		out.Rules = append(out.Rules, r)
	}
	if len(out.Rules) == 0 {
		return nil, nil
	}
	exempt := map[protocol.SubjectKey]bool{}
	for _, subject := range lkg.Exempt {
		if roster[subject] {
			exempt[subject] = true
		}
	}
	for _, u := range cb.base.exempt {
		if k := protocol.NewSubjectKey(u); roster[k] {
			exempt[k] = true
		}
	}
	for k := range exempt {
		out.Exempt = append(out.Exempt, k)
	}
	sortKeys(out.Exempt)
	// Recording follows the panel's choice NOW, but only downwards: raising it
	// would mint bytes the node never applied (a different digest), and if the
	// node refused those too the LKG would be dropped and the node would stop
	// enforcing. So the level is the lower of what the LKG ran and the current
	// one.
	withCollect(out, lowerCollect(lkg.Collect, collect))
	// The same for how hostnames meet the IP rules: the lower of what the LKG ran
	// and the current choice, re-derived from the rules that are left.
	withResolve(out, lowerResolve(lkg.Resolve, resolve))
	if err := protocol.ValidateDestinationPolicy(out); err != nil {
		log.Warn("destpolicy: the trimmed last-known-good policy does not validate; sending none", "agent_id", agentID, "err", err)
		return nil, nil
	}
	return out, nil
}

// wireAction is the action a policy's rule has on the wire: a report-only block
// policy is an observe rule.
func wireAction(p *snapPolicy) protocol.RuleAction {
	if p.State == string(domain.DestStateReport) {
		return protocol.RuleObserve
	}
	return protocol.RuleAction(p.Action)
}

// ruleOrigin reads a rule id: 's', 'p' or 'g', and the number after it (the
// node of a source rule, the policy of a policy rule).
func ruleOrigin(id string) (kind byte, n int64, ok bool) {
	if len(id) < 2 {
		return 0, 0, false
	}
	rest, _, _ := strings.Cut(id[1:], "x")
	v, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return id[0], v, true
}

// ---- delivery state ----

func (s *Service) metaFor(ctx context.Context, agentID string) (*domain.DestAgentPolicy, error) {
	s.mu.Lock()
	m := s.agentMeta[agentID]
	s.mu.Unlock()
	if m != nil {
		return m, nil
	}
	row, err := s.agents.GetMeta(ctx, agentID)
	if errors.Is(err, domain.ErrNotFound) {
		row, err = &domain.DestAgentPolicy{AgentID: agentID}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("destpolicy: read agent state: %w", err)
	}
	s.mu.Lock()
	s.agentMeta[agentID] = row
	s.mu.Unlock()
	return row, nil
}

// patchMeta writes the columns a patch names and mirrors them into the cached
// copy. An empty patch writes nothing: the delivery state is only written when
// a value changed.
func (s *Service) patchMeta(ctx context.Context, agentID string, p domain.DestAgentPolicyPatch) error {
	if p == (domain.DestAgentPolicyPatch{}) {
		return nil
	}
	if err := s.agents.Patch(ctx, agentID, p); err != nil {
		return fmt.Errorf("destpolicy: write agent state: %w", err)
	}
	s.mu.Lock()
	cur := s.agentMeta[agentID]
	next := domain.DestAgentPolicy{AgentID: agentID}
	if cur != nil {
		next = *cur
	}
	before := next.FallbackReason
	if p.DesiredSHA256 != nil {
		next.DesiredSHA256 = *p.DesiredSHA256
	}
	if p.MintedSHA256 != nil {
		next.MintedSHA256 = *p.MintedSHA256
	}
	if p.MintedAt != nil {
		t := p.MintedAt.UTC()
		next.MintedAt = &t
	}
	if p.FallbackReason != nil {
		next.FallbackReason = *p.FallbackReason
	}
	if p.RejectedGeneration != nil {
		next.RejectedGeneration = *p.RejectedGeneration
	}
	if p.OverLimit != nil {
		next.OverLimit = *p.OverLimit
	}
	if p.PrecheckListeners != nil {
		next.PrecheckListeners = *p.PrecheckListeners
	}
	if p.ReportOnlyShed != nil {
		next.ReportOnlyShed = *p.ReportOnlyShed
	}
	if p.AppliedSHA256 != nil {
		next.AppliedSHA256 = *p.AppliedSHA256
	}
	if p.AppliedAt != nil {
		t := p.AppliedAt.UTC()
		next.AppliedAt = &t
	}
	if p.AppliedRuleCount != nil {
		next.AppliedRuleCount = *p.AppliedRuleCount
	}
	if p.AppliedGroups != nil {
		next.AppliedGroups = slices.Clone(*p.AppliedGroups)
	}
	if p.AppliedCollect != nil {
		next.AppliedCollect = *p.AppliedCollect
	}
	if p.CollectEffective != nil {
		next.CollectEffective = *p.CollectEffective
	}
	if p.ResolveEffective != nil {
		next.ResolveEffective = *p.ResolveEffective
	}
	if p.ResolveUnavailable != nil {
		next.ResolveUnavailable = *p.ResolveUnavailable
	}
	if p.ReportedSHA256 != nil {
		next.ReportedSHA256 = *p.ReportedSHA256
	}
	if p.ReportedState != nil {
		next.ReportedState = *p.ReportedState
	}
	if p.ReportedIssue != nil {
		next.ReportedIssue = *p.ReportedIssue
	}
	if p.ReportedListeners != nil {
		next.ReportedListeners = *p.ReportedListeners
	}
	if p.ReportedAt != nil {
		t := p.ReportedAt.UTC()
		next.ReportedAt = &t
	}
	if p.RejectedCaps != nil {
		next.RejectedCaps = *p.RejectedCaps
	}
	if p.RejectedSHA256 != nil {
		next.RejectedSHA256 = *p.RejectedSHA256
	}
	if p.MintedGeneration != nil {
		next.MintedGeneration = *p.MintedGeneration
	}
	if p.MintedCtx != nil {
		next.MintedCtx = *p.MintedCtx
	}
	if p.RejectedCtx != nil {
		next.RejectedCtx = *p.RejectedCtx
	}
	s.agentMeta[agentID] = &next
	after := next.FallbackReason
	s.mu.Unlock()
	if s.fallbackChanged != nil && blocksAllowlist(before) != blocksAllowlist(after) {
		s.fallbackChanged(ctx, agentID)
	}
	return nil
}

// blocksAllowlist says whether a fallback reason makes a node unable to
// enforce a group allowlist: a policy the node refused or one over a limit
// means its groups' rules are not running. The sniffing fallback is not one:
// it depends on the roster, which the allowlist shrinks, and eligibility
// judges the inbounds' sniffing directly instead (group.Service.Eligible).
func blocksAllowlist(r domain.DestFallbackReason) bool {
	return r == domain.DestFallbackRejected || r == domain.DestFallbackOverLimit
}

// baseFor returns the expanded snapshot of generation gen, building it on a
// miss. One build at a time: every node's first sync after a publish asks for
// the same thing.
func (s *Service) baseFor(ctx context.Context, gen int64) (*cachedBase, error) {
	s.baseMu.Lock()
	defer s.baseMu.Unlock()
	s.mu.Lock()
	c := s.baseCache
	s.mu.Unlock()
	if c != nil && c.gen == gen {
		return c, nil
	}
	snap, err := s.state.Snapshot(ctx, gen)
	if err != nil {
		return nil, fmt.Errorf("destpolicy: read snapshot %d: %w", gen, err)
	}
	doc, err := parseSnapshotDoc(snap.Body)
	if err != nil {
		return nil, err
	}
	b, err := s.buildBase(ctx, doc)
	if err != nil {
		return nil, err
	}
	c = &cachedBase{gen: gen, doc: doc, base: b}
	s.mu.Lock()
	s.baseCache = c
	s.mu.Unlock()
	return c, nil
}

// PruneAgents forgets the cached delivery state and compile results of every
// agent not in live (a deleted agent's entries would otherwise stay for the
// life of the process). The refresh loop calls it with the agents that exist.
func (s *Service) PruneAgents(live map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.agentMeta {
		if !live[id] {
			delete(s.agentMeta, id)
		}
	}
	for id := range s.compiled {
		if !live[id] {
			delete(s.compiled, id)
		}
	}
	for id := range s.lastCore {
		if !live[id] {
			delete(s.lastCore, id)
		}
	}
	for id := range s.bodyFailures {
		if agent, _, _ := strings.Cut(id, "|"); !live[agent] {
			delete(s.bodyFailures, id)
		}
	}
}

// The bound on a persistently failing LKG read: this many consecutive failed
// attempts, or this long since the first.
const (
	bodyReadMaxAttempts = 3
	bodyReadMaxAge      = 5 * time.Minute
)

type bodyFailure struct {
	n     int
	since time.Time
	last  time.Time
}

// bodyKey keys the failure bookkeeping by agent AND the LKG it is about: when
// the record changes (a new LKG applied, or it was dropped) the counter and the
// back-off start over.
func bodyKey(agentID, applied string) string { return agentID + "|" + applied }

// bodyReadBackedOff reports whether the bound is already exceeded and the last
// read was under a minute ago: the read is then not attempted at all, so an
// unreadable LKG costs one query a minute, not one per sync.
func (s *Service) bodyReadBackedOff(agentID string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.bodyFailures[agentID]
	if f == nil || !(f.n >= bodyReadMaxAttempts || now.Sub(f.since) >= bodyReadMaxAge) {
		return false
	}
	return now.Sub(f.last) < time.Minute
}

// bodyReadGaveUp records a failed read of agentID's LKG and reports whether the
// bound is reached.
func (s *Service) bodyReadGaveUp(agentID string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bodyFailures == nil {
		s.bodyFailures = map[string]*bodyFailure{}
	}
	f := s.bodyFailures[agentID]
	if f == nil {
		f = &bodyFailure{since: now}
		s.bodyFailures[agentID] = f
	}
	f.n++
	f.last = now
	return f.n >= bodyReadMaxAttempts || now.Sub(f.since) >= bodyReadMaxAge
}

func (s *Service) bodyReadOK(agentID string) {
	s.mu.Lock()
	delete(s.bodyFailures, agentID)
	s.mu.Unlock()
}

func (s *Service) observedCore(agentID, reported string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if reported == "" {
		return s.lastCore[agentID]
	}
	if s.lastCore == nil {
		s.lastCore = map[string]string{}
	}
	s.lastCore[agentID] = reported
	return reported
}
