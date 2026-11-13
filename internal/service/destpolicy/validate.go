package destpolicy

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ValidationError is an admin input refused, with the machine-readable code the
// API answers with (dest_policy_invalid, dest_policy_no_match, ...), the field
// at fault, and the offending values. It classifies as a validation error
// (HTTP 400).
type ValidationError struct {
	Code  string
	Field string
	Bad   []string
	Msg   string
	// Kind/Used/Limit are set for dest_policy_over_limit.
	Kind  string
	Used  int
	Limit int
}

func (e *ValidationError) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("%v: %s: %s", domain.ErrValidation, e.Code, e.Msg)
	}
	return fmt.Sprintf("%v: %s (%s)", domain.ErrValidation, e.Code, e.Field)
}

func (e *ValidationError) Unwrap() error { return domain.ErrValidation }

// The API's error codes for a policy.
const (
	CodePolicyInvalid          = "dest_policy_invalid"
	CodePolicyNoMatch          = "dest_policy_no_match"
	CodePolicyOverLimit        = "dest_policy_over_limit"
	CodeStateUnavailable       = "dest_policy_state_unavailable"
	CodeSourceRuleNotNative    = "dest_source_rule_not_native"
	CodeExemptionExists        = "dest_exemption_exists"
	CodeExemptionInvalid       = "dest_exemption_invalid"
	CodeSourceRuleInvalid      = "dest_source_rule_invalid"
	CodeTestInvalid            = "dest_test_invalid"
	CodeExceptionInvalid       = "dest_exception_invalid"
	reportStateMessage         = "the report state is not available yet: it needs hit recording, which arrives later"
	maxPortsInlineLen          = 256
	maxInlineCIDRs             = 20000
	protocolBitTorrent         = "bittorrent"
	maxExemptionReasonBytes    = 255
	maxSourceRuleCountries     = 300
	maxSourceRuleCIDRs         = 60000
	maxExceptionTargetBytes    = 253
	exceptionTemplateKey       = "global_exceptions"
	exceptionListName          = "Allow exceptions"
	exceptionPolicyName        = "Allow exceptions"
	exceptionPolicyDescription = "Destinations added with \"allow\" from the access-control page."
)

// ErrStateUnavailable is the report state, refused until the hit recording
// that gives it something to show exists.
func errStateUnavailable() error {
	return &ValidationError{Code: CodeStateUnavailable, Field: "state", Msg: reportStateMessage}
}

// CheckState validates a state an admin chose: off or on. "report" is part of
// the model but is refused (dest_policy_state_unavailable) until batch 2.
func CheckState(s domain.DestState) error {
	switch {
	case domain.DestStateSelectable(s):
		return nil
	case s == domain.DestStateReport:
		return errStateUnavailable()
	}
	return &ValidationError{Code: CodePolicyInvalid, Field: "state", Bad: []string{string(s)}, Msg: "state must be off or on"}
}

// NormalizePorts parses a port list the way the wire does and writes it in the
// one spelling the wire accepts: "25,465,587", "6881-6889". Entries may come in
// any order and may touch ("80,81" is "80-81"? no: they are kept as typed, but
// must not overlap); a range of one port is the port. An unparsable or
// overlapping list is an error.
func NormalizePorts(spec string) (string, error) {
	spec = strings.ReplaceAll(strings.TrimSpace(spec), " ", "")
	if spec == "" {
		return "", nil
	}
	if len(spec) > maxPortsInlineLen {
		return "", fmt.Errorf("ports list is too long")
	}
	type span struct{ lo, hi int }
	var spans []span
	for _, part := range strings.Split(spec, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil || a < 1 || a > 65535 || (len(lo) > 1 && lo[0] == '0') {
			return "", fmt.Errorf("%q is not a port in 1..65535", lo)
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil || b < 1 || b > 65535 || (len(hi) > 1 && hi[0] == '0') {
				return "", fmt.Errorf("%q is not a port in 1..65535", hi)
			}
			if a > b {
				return "", fmt.Errorf("range %q runs backwards", part)
			}
		}
		spans = append(spans, span{a, b})
	}
	slices.SortFunc(spans, func(x, y span) int { return x.lo - y.lo })
	var out []string
	for i, sp := range spans {
		if i > 0 && sp.lo <= spans[i-1].hi {
			return "", fmt.Errorf("ports %d-%d and %d-%d overlap", spans[i-1].lo, spans[i-1].hi, sp.lo, sp.hi)
		}
		if sp.lo == sp.hi {
			out = append(out, strconv.Itoa(sp.lo))
		} else {
			out = append(out, fmt.Sprintf("%d-%d", sp.lo, sp.hi))
		}
	}
	return strings.Join(out, ","), nil
}

// NormalizeCIDRs masks each prefix, refuses IPv4-mapped IPv6 ones (the core
// matches a mapped address as IPv4, so such a prefix would never hit) and
// returns them in the wire's bytewise order without repeats. bad lists the
// entries that did not parse.
func NormalizeCIDRs(in []string) (out []string, bad []string) {
	for _, c := range in {
		c = strings.TrimSpace(c)
		p, err := netip.ParsePrefix(c)
		if err != nil || p.Addr().Is4In6() {
			bad = append(bad, c)
			continue
		}
		out = append(out, p.Masked().String())
	}
	slices.Sort(out)
	return slices.Compact(out), bad
}

// normalizeInline cleans a policy's built-in match.
func normalizeInline(in domain.DestPolicyInline) (domain.DestPolicyInline, *ValidationError) {
	out := domain.DestPolicyInline{Private: in.Private}
	cidrs, bad := NormalizeCIDRs(in.CIDRs)
	if len(bad) > 0 {
		return out, &ValidationError{Code: CodePolicyInvalid, Field: "inline.cidrs", Bad: bad, Msg: "not a CIDR prefix"}
	}
	if len(cidrs) > maxInlineCIDRs {
		return out, &ValidationError{Code: CodePolicyInvalid, Field: "inline.cidrs", Msg: "too many prefixes"}
	}
	out.CIDRs = cidrs
	ports, err := NormalizePorts(in.Ports)
	if err != nil {
		return out, &ValidationError{Code: CodePolicyInvalid, Field: "inline.ports", Bad: []string{in.Ports}, Msg: err.Error()}
	}
	out.Ports = ports
	switch in.Network {
	case "", "tcp", "udp":
		out.Network = in.Network
	default:
		return out, &ValidationError{Code: CodePolicyInvalid, Field: "inline.network", Bad: []string{in.Network}, Msg: "network is tcp, udp or empty"}
	}
	for _, p := range in.Protocols {
		if p != protocolBitTorrent {
			return out, &ValidationError{Code: CodePolicyInvalid, Field: "inline.protocols", Bad: []string{p}, Msg: "the only protocol is bittorrent"}
		}
	}
	out.Protocols = sortedUnique(in.Protocols)
	return out, nil
}
