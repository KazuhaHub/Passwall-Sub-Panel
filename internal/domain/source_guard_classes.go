package domain

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

// Factor classes (ADR 0044).
//
// On one landing listener, the lines that end on it (the direct line, managed
// and external forwards, CDN lines; a tunnel line has a listener of its own)
// are grouped by effective traffic factor. The class holding the direct line's
// factor is the DIRECT CLASS: its lines may be reached from anywhere, so they
// need no sources. Every other class is a PRICED CLASS whose source set is the
// union of its lines' effective sets, and whose identities are accepted from
// that set only. The direct class's identities are refused from every priced
// class's set. Two classes whose sets overlap cannot be told apart, which is
// why a landing that guards refuses to save them.

// Codes of the save-time refusals of the source guard.
const (
	// CodeSourceGuardFactorMismatch refuses a line whose factor differs from the
	// direct line's on a landing that cannot guard.
	CodeSourceGuardFactorMismatch = "source_guard_factor_mismatch"
	// CodeSourceGuardNeedsCIDRs refuses an external line whose factor differs
	// from the direct line's and that names no source addresses.
	CodeSourceGuardNeedsCIDRs = "source_guard_needs_cidrs"
	// CodeSourceGuardOverlap refuses two factor classes that arrive from
	// overlapping addresses.
	CodeSourceGuardOverlap = "source_guard_overlap"
	// CodeSourceGuardTooManyClasses refuses more priced classes on a listener
	// than a client's deny list can name.
	CodeSourceGuardTooManyClasses = "source_guard_too_many_classes"
	// CodeSourceGuardTooManyCIDRs refuses a class whose addresses do not fit one
	// source set.
	CodeSourceGuardTooManyCIDRs = "source_guard_too_many_cidrs"
	// CodeSourceGuardListenerTransport refuses a listener or engine change after
	// which the core would take the source from a header the client controls
	// (sing-box on a non-TCP transport), while lines at another factor than the
	// direct line end on the listener.
	CodeSourceGuardListenerTransport = "source_guard_listener_transport"
	// CodeSourceGuardListenerTrust is the same for an Xray listener that trusts
	// X-Forwarded-For or accepts the PROXY protocol.
	CodeSourceGuardListenerTrust = "source_guard_listener_trust"
)

// MaxPricedClasses is how many priced classes one listener may have: a
// direct-class client names all of them in one deny list.
const MaxPricedClasses = nodeprotocol.MaxSourceGuardDenyFrom

// ListenerGuardable reports whether a listener's source address is the
// connection's own, so that a guard on it means something. It is not when the
// core would take the source from something the client controls (mirroring
// Passwall Node's source guard):
//
//   - sing-box takes it from X-Forwarded-For on every transport (ws, httpupgrade,
//     http, grpc) and cannot be told not to: only plain TCP, with or without
//     TLS or REALITY, is guardable;
//   - Xray listeners that carry a non-empty trustedXForwardedFor anywhere in
//     their stream or inbound settings (Xray reads it from sockopt; the node
//     refuses it at any depth, keys matched ignoring case);
//   - Xray listeners that set acceptProxyProtocol anywhere in their stream or
//     inbound settings. The PROXY header a Passwall front adds is not counted: fronts
//     refuse listeners that set it themselves.
//
// engines are the cores the server runs or is about to; every one must accept
// the listener.
func ListenerGuardable(n *Node, engines []NodeCoreEngine) bool {
	if n == nil {
		return false
	}
	if len(engines) == 0 {
		engines = []NodeCoreEngine{NodeCoreXray}
	}
	var stream map[string]any
	if raw := strings.TrimSpace(n.StreamSettings); raw != "" {
		_ = json.Unmarshal([]byte(raw), &stream)
	}
	var inbound map[string]any
	if raw := strings.TrimSpace(n.InboundSettings); raw != "" {
		_ = json.Unmarshal([]byte(raw), &inbound)
	}
	// Xray reads config keys case-insensitively, so every lookup here does too.
	// A stream settings object may also arrive wrapped in a streamSettings key.
	streams := []map[string]any{stream}
	for _, doc := range []map[string]any{stream, inbound} {
		for _, v := range ciValues(doc, "streamSettings") {
			if m, ok := v.(map[string]any); ok {
				streams = append(streams, m)
			}
		}
	}
	for _, engine := range engines {
		switch NormalizeNodeCoreEngine(engine) {
		case NodeCoreSingBox:
			for _, st := range streams {
				for _, v := range ciValues(st, "network") {
					network, _ := v.(string)
					switch strings.ToLower(strings.TrimSpace(network)) {
					case "", "tcp", "raw":
					default:
						return false
					}
				}
			}
		default:
			for _, st := range streams {
				if trustsForwardedFor(st) || acceptsProxyAnywhere(st) {
					return false
				}
			}
			if acceptsProxyAnywhere(inbound) || trustsForwardedFor(inbound) {
				return false
			}
		}
	}
	return true
}

// ciValues returns the values of every key of doc equal to key ignoring case.
func ciValues(doc map[string]any, key string) []any {
	var out []any
	for k, v := range doc {
		if strings.EqualFold(k, key) {
			out = append(out, v)
		}
	}
	return out
}

// PricedLines are the lines ending on the node's listener whose factor differs
// from the direct line's: the ones that need the listener to guard.
func (n *Node) PricedLines() []RelayLine {
	if n == nil || n.IsTunnelChild() || n.IsSeparator() {
		return nil
	}
	direct := n.EffectiveTrafficFactor()
	var out []RelayLine
	for _, line := range n.Relays {
		if line.Guarded() && line.ID != "" && line.EffectiveTrafficFactor() != direct {
			out = append(out, line)
		}
	}
	return out
}

// ListenerGuardRefusal refuses a change (a listener edit, an engine switch)
// that makes a listener unguardable while it has priced lines: before and after
// are the cores the server runs before and after the change. A listener that
// was not guardable already is not refused again. Nil when the change is fine.
func ListenerGuardRefusal(previous, next *Node, before, after []NodeCoreEngine) *CodedValidationError {
	priced := next.PricedLines()
	if len(priced) == 0 || !ListenerGuardable(previous, before) || ListenerGuardable(next, after) {
		return nil
	}
	labels := make([]string, len(priced))
	for i, line := range priced {
		labels[i] = line.Label()
	}
	list := strings.Join(labels, ", ")
	code, why := CodeSourceGuardListenerTrust, "its settings would trust X-Forwarded-For or a PROXY header"
	for _, engine := range after {
		if NormalizeNodeCoreEngine(engine) == NodeCoreSingBox && !ListenerGuardable(next, []NodeCoreEngine{NodeCoreSingBox}) {
			code, why = CodeSourceGuardListenerTransport, "sing-box reads the source address from X-Forwarded-For on this transport"
		}
	}
	return &CodedValidationError{
		Code: code, Params: map[string]string{"node": next.DisplayName, "lines": list},
		Msg: fmt.Sprintf("this change would stop %s from telling connections apart by where they come from (%s), while its lines %s cost something other than the direct line and rely on it: give those lines the direct factor or remove them first",
			next.DisplayName, why, list),
	}
}

// trustsForwardedFor finds a non-empty trustedXForwardedFor anywhere in v,
// ignoring case. Xray reads it from sockopt; Passwall Node refuses a listener
// that carries it at any depth, so the panel must call the same listeners
// unguardable or it would send guards the node rejects.
func trustsForwardedFor(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if strings.EqualFold(k, "trustedXForwardedFor") {
				switch trusted := x.(type) {
				case []any:
					if len(trusted) > 0 {
						return true
					}
				case string:
					// Any non-empty string, as the node reads it: trimming
					// here would call guardable a listener it refuses.
					if trusted != "" {
						return true
					}
				}
			}
			if trustsForwardedFor(x) {
				return true
			}
		}
	case []any:
		for _, x := range t {
			if trustsForwardedFor(x) {
				return true
			}
		}
	}
	return false
}

// acceptsProxyAnywhere finds acceptProxyProtocol set to true anywhere in the
// stream settings: Xray spells it in sockopt and in each transport's settings.
func acceptsProxyAnywhere(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if strings.EqualFold(k, "acceptProxyProtocol") {
				if on, _ := x.(bool); on {
					return true
				}
			}
			if acceptsProxyAnywhere(x) {
				return true
			}
		}
	case []any:
		for _, x := range t {
			if acceptsProxyAnywhere(x) {
				return true
			}
		}
	}
	return false
}

// SourceClass is one group of lines of a landing listener with the same factor.
type SourceClass struct {
	Factor int
	// Direct is the class holding the direct line's factor.
	Direct bool
	// Key is the wire key of the class's source set; "" for the direct class,
	// which has none.
	Key string
	// LineIDs are the enabled lines of the class, in line order.
	LineIDs []string
	// CIDRs are the known addresses of the lines of the class: the union of
	// their effective sets, canonical and sorted. For the direct class they are
	// only what the overlap rule compares; they are never sent.
	CIDRs []string
	// Withheld is why the class's set is not sent (SourceWithheldOverlap,
	// SourceWithheldLimit), or "" when it is. A class with no addresses has no
	// set either, but that is unknown, not withheld.
	Withheld string
	// OverlapsDirect is set on a priced class whose addresses overlap the direct
	// class's: refusing the direct class from them would refuse its own lines.
	OverlapsDirect bool
}

// Emitted is whether the class's set goes to the landing.
func (c SourceClass) Emitted() bool { return !c.Direct && c.Withheld == "" && len(c.CIDRs) > 0 }

// Denies is whether direct-class identities are refused from the class's set,
// which then has to be sent even when the class is withheld (overlapping another
// priced class): the identity of a direct client must not be usable through a
// priced forward that keeps running. Not when the set is unknown, does not fit
// (SourceWithheldLimit), or overlaps the direct class's own addresses.
func (c SourceClass) Denies() bool {
	return !c.Direct && len(c.CIDRs) > 0 && c.Withheld != SourceWithheldLimit && !c.OverlapsDirect
}

// SourceGuardProblem is one refusal of a save, with a stable identity so a
// refusal that was already true before the save can be told from a new one.
type SourceGuardProblem struct {
	ID     string
	LineID string
	Err    *CodedValidationError
}

// SourceGuardAnalysis is what a landing's lines come to under the factor
// classes.
type SourceGuardAnalysis struct {
	// Guards is whether the landing can enforce at all (CanGuard).
	Guards bool
	Direct SourceClass
	// Priced are the other classes, by factor.
	Priced []SourceClass
	// Withheld says why each enabled line must not be published, by line id.
	Withheld map[string]string
	// Problems are what a save is refused for.
	Problems []SourceGuardProblem
}

// PricedClassOf returns the priced class a factor falls in, if any.
func (a SourceGuardAnalysis) PricedClassOf(factor int) (SourceClass, bool) {
	for _, c := range a.Priced {
		if c.Factor == factor {
			return c, true
		}
	}
	return SourceClass{}, false
}

// SourceSetKeyOf is the wire key of the source set of a landing's class.
func SourceSetKeyOf(nodeID int64, factorMilli int) nodeprotocol.SourceSetKey {
	return nodeprotocol.SourceSetKey("src_" + SourceSetSuffix(nodeID, factorMilli))
}

// ParseSourceSetKey reads a landing and a factor back out of a set key; ok is
// false for a key that is not one of ours.
func ParseSourceSetKey(key string) (nodeID int64, factorMilli int, ok bool) {
	suffix, found := strings.CutPrefix(key, "src_")
	if !found {
		return 0, 0, false
	}
	idText, factorText, found := strings.Cut(suffix, "_f")
	if !found {
		return 0, 0, false
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != idText {
		return 0, 0, false
	}
	factor, err := strconv.Atoi(factorText)
	if err != nil || factor < 0 || strconv.Itoa(factor) != factorText {
		return 0, 0, false
	}
	return id, factor, true
}

// AnalyzeSourceGuard groups the landing's lines into factor classes and says
// what the guard makes of them: which classes are sent, which lines are
// withheld, and what a save of this node is refused for. It is pure: every
// consumer (the plan sent to the landing, the lines a subscription may show, the
// save rules, the editor) reads this one answer.
func (n *Node) AnalyzeSourceGuard(in SourceInputs) SourceGuardAnalysis {
	a := SourceGuardAnalysis{Guards: in.CanGuard(n), Withheld: map[string]string{}}
	if n == nil {
		return a
	}
	direct := n.EffectiveTrafficFactor()
	a.Direct = SourceClass{Factor: direct, Direct: true}
	a.addFactorProblems(n, direct, in.Servers[n.PanelID].Capable)
	if !a.Guards {
		// Nothing tells the lines apart: whatever costs something else than the
		// direct line cannot be published.
		for _, line := range n.Relays {
			if line.Guarded() && line.ID != "" && line.EffectiveTrafficFactor() != direct {
				a.Withheld[line.ID] = SourceWithheldFactor
			}
		}
		return a
	}

	type build struct {
		class    SourceClass
		prefixes []netip.Prefix
	}
	classes := map[int]*build{direct: {class: a.Direct}}
	var factors []int
	for _, line := range n.Relays {
		if !line.Guarded() || !line.Enabled || line.ID == "" {
			continue
		}
		factor := line.EffectiveTrafficFactor()
		b, ok := classes[factor]
		if !ok {
			b = &build{class: SourceClass{Factor: factor, Key: string(SourceSetKeyOf(n.ID, factor))}}
			classes[factor] = b
			factors = append(factors, factor)
		}
		b.class.LineIDs = append(b.class.LineIDs, line.ID)
		cidrs := line.EffectiveSourceCIDRs(in)
		if len(cidrs) == 0 && factor != direct {
			a.Withheld[line.ID] = SourceWithheldUnknown
		}
		for _, text := range cidrs {
			if p, err := netip.ParsePrefix(text); err == nil {
				b.prefixes = append(b.prefixes, p)
			}
		}
	}
	sort.Ints(factors)

	// The union of each class, deduplicated.
	order := append([]int{direct}, factors...)
	for _, factor := range order {
		b := classes[factor]
		seen := make(map[netip.Prefix]struct{}, len(b.prefixes))
		unique := b.prefixes[:0]
		for _, p := range b.prefixes {
			if _, dup := seen[p]; !dup {
				seen[p] = struct{}{}
				unique = append(unique, p)
			}
		}
		b.prefixes = unique
		if len(unique) > 0 {
			b.class.CIDRs = prefixStrings(slices.Clone(unique))
		}
	}

	// Limits, then overlap.
	if len(factors) > MaxPricedClasses {
		for _, factor := range factors {
			classes[factor].class.Withheld = SourceWithheldLimit
		}
		a.Problems = append(a.Problems, a.problemClasses(n, classes[factors[0]].class, len(factors)))
	}
	for _, factor := range factors {
		c := &classes[factor].class
		if len(c.CIDRs) > MaxSourceCIDRs && c.Withheld == "" {
			c.Withheld = SourceWithheldLimit
			a.Problems = append(a.Problems, a.problemCIDRs(n, *c, len(c.CIDRs)))
		}
	}
	sets := make([][]netip.Prefix, len(order))
	for i, factor := range order {
		sets[i] = classes[factor].prefixes
	}
	for _, pair := range overlappingSets(sets) {
		x, y := classes[order[pair[0]]], classes[order[pair[1]]]
		for _, b := range []*build{x, y} {
			if !b.class.Direct && b.class.Withheld == "" {
				b.class.Withheld = SourceWithheldOverlap
			}
			if !b.class.Direct && (x.class.Direct || y.class.Direct) {
				b.class.OverlapsDirect = true
			}
		}
		a.Problems = append(a.Problems, a.problemOverlap(n, x.class, y.class))
	}

	// Lines of a class that is not sent are withheld with the class's reason,
	// ahead of unknown.
	for _, factor := range factors {
		c := classes[factor].class
		if c.Withheld != "" {
			for _, id := range c.LineIDs {
				a.Withheld[id] = c.Withheld
			}
		}
	}
	a.Direct = classes[direct].class
	for _, factor := range factors {
		a.Priced = append(a.Priced, classes[factor].class)
	}
	return a
}

// addFactorProblems are the refusals about a line's own prices: a landing that
// cannot guard needs every line to cost what the direct line costs, and an
// external line that costs something else must name its addresses.
func (a *SourceGuardAnalysis) addFactorProblems(n *Node, direct int, serverCapable bool) {
	for _, line := range n.Relays {
		factor := line.EffectiveTrafficFactor()
		if !line.Guarded() || factor == direct {
			continue
		}
		params := map[string]string{
			"node": n.DisplayName, "line": line.Label(),
			"line_factor": strconv.Itoa(factor), "direct_factor": strconv.Itoa(direct),
		}
		switch {
		case !a.Guards && serverCapable:
			params["reason"] = "listener"
			a.Problems = append(a.Problems, SourceGuardProblem{
				ID: fmt.Sprintf("mismatch:%s:%d:%d", line.ID, factor, direct), LineID: line.ID,
				Err: &CodedValidationError{
					Code: CodeSourceGuardFactorMismatch, Params: params,
					Msg: fmt.Sprintf("line %q of %s costs %d but the direct line costs %d: this node's listener lets clients choose the source address the core sees (its transport or settings make the core read it from X-Forwarded-For or a PROXY header), so connections cannot be told apart by where they come from and every line ending on the node must cost the same. Use a plain TCP (TLS or REALITY) listener to price lines apart",
						line.Label(), n.DisplayName, factor, direct),
				},
			})
		case !a.Guards:
			a.Problems = append(a.Problems, SourceGuardProblem{
				ID: fmt.Sprintf("mismatch:%s:%d:%d", line.ID, factor, direct), LineID: line.ID,
				Err: &CodedValidationError{
					Code: CodeSourceGuardFactorMismatch, Params: params,
					Msg: fmt.Sprintf("line %q of %s costs %d but the direct line costs %d: this server cannot check where connections come from, so every line ending on the node must cost the same (use a Passwall Node that supports source checks to price lines apart)",
						line.Label(), n.DisplayName, factor, direct),
				},
			})
		case !line.Managed() && !line.CDN() && len(line.SourceCIDRs) == 0:
			a.Problems = append(a.Problems, SourceGuardProblem{
				ID: fmt.Sprintf("needs:%s:%d:%d", line.ID, factor, direct), LineID: line.ID,
				Err: &CodedValidationError{
					Code: CodeSourceGuardNeedsCIDRs, Params: params,
					Msg: fmt.Sprintf("external line %q of %s costs %d but the direct line costs %d: list the addresses its traffic comes from in the line's source addresses, so the two can be told apart",
						line.Label(), n.DisplayName, factor, direct),
				},
			})
		}
	}
}

func (a *SourceGuardAnalysis) problemClasses(n *Node, first SourceClass, count int) SourceGuardProblem {
	return SourceGuardProblem{
		ID: "classes", LineID: first.firstLine(),
		Err: &CodedValidationError{
			Code:   CodeSourceGuardTooManyClasses,
			Params: map[string]string{"node": n.DisplayName, "classes": strconv.Itoa(count), "max": strconv.Itoa(MaxPricedClasses)},
			Msg: fmt.Sprintf("%s has lines at %d different factors besides the direct one, and a connection can be checked against at most %d: give some lines the same factor",
				n.DisplayName, count, MaxPricedClasses),
		},
	}
}

func (a *SourceGuardAnalysis) problemCIDRs(n *Node, c SourceClass, count int) SourceGuardProblem {
	return SourceGuardProblem{
		ID: fmt.Sprintf("cidrs:%d", c.Factor), LineID: c.firstLine(),
		Err: &CodedValidationError{
			Code:   CodeSourceGuardTooManyCIDRs,
			Params: map[string]string{"node": n.DisplayName, "factor": strconv.Itoa(c.Factor), "cidrs": strconv.Itoa(count), "max": strconv.Itoa(MaxSourceCIDRs)},
			Msg: fmt.Sprintf("the lines of %s that cost %d have %d source addresses together, and the most one set holds is %d: remove some, or widen them into fewer prefixes",
				n.DisplayName, c.Factor, count, MaxSourceCIDRs),
		},
	}
}

func (a *SourceGuardAnalysis) problemOverlap(n *Node, x, y SourceClass) SourceGuardProblem {
	if x.Factor > y.Factor {
		x, y = y, x
	}
	label := func(c SourceClass) string {
		for _, line := range n.Relays {
			if len(c.LineIDs) > 0 && line.ID == c.LineIDs[0] {
				return line.Label()
			}
		}
		return ""
	}
	// The line to point at is the one of a priced class.
	lineID := y.firstLine()
	if y.Direct {
		lineID = x.firstLine()
	}
	return SourceGuardProblem{
		ID: fmt.Sprintf("overlap:%d:%d", x.Factor, y.Factor), LineID: lineID,
		Err: &CodedValidationError{
			Code: CodeSourceGuardOverlap,
			Params: map[string]string{
				"node": n.DisplayName, "line": label(x), "other_line": label(y),
				"line_factor": strconv.Itoa(x.Factor), "other_factor": strconv.Itoa(y.Factor),
			},
			Msg: fmt.Sprintf("lines of %s that cost %d (such as %q) and %d (such as %q) arrive from the same addresses, so the node cannot tell them apart: lines that arrive from the same addresses must have the same factor",
				n.DisplayName, x.Factor, label(x), y.Factor, label(y)),
		},
	}
}

func (c SourceClass) firstLine() string {
	if len(c.LineIDs) > 0 {
		return c.LineIDs[0]
	}
	return ""
}

// overlappingSets lists the pairs of sets (by index) that have a prefix in
// common or one inside another. Prefixes nest or are disjoint, so one sweep
// over all of them, ordered by address, with the chain of prefixes that
// enclose the current one, finds every pair.
func overlappingSets(sets [][]netip.Prefix) [][2]int {
	type item struct {
		p   netip.Prefix
		set int
	}
	var all []item
	for i, set := range sets {
		for _, p := range set {
			all = append(all, item{p, i})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i].p, all[j].p
		if a.Addr().Is4() != b.Addr().Is4() {
			return a.Addr().Is4()
		}
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c < 0
		}
		return a.Bits() < b.Bits()
	})
	found := map[[2]int]bool{}
	var stack []item
	for _, it := range all {
		for len(stack) > 0 {
			top := stack[len(stack)-1]
			if top.p.Addr().Is4() == it.p.Addr().Is4() && top.p.Overlaps(it.p) {
				break
			}
			stack = stack[:len(stack)-1]
		}
		for _, enclosing := range stack {
			if enclosing.set != it.set {
				x, y := enclosing.set, it.set
				if x > y {
					x, y = y, x
				}
				found[[2]int{x, y}] = true
			}
		}
		stack = append(stack, it)
	}
	out := make([][2]int, 0, len(found))
	for pair := range found {
		out = append(out, pair)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}
