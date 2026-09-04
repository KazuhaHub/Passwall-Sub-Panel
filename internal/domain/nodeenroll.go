package domain

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// NodeEnrollPurpose is the auth_tokens.purpose for a one-time node enrollment
// token. Distinct from the user-facing purposes so a password-reset token can
// never be replayed at the enrollment endpoint, and vice versa.
const NodeEnrollPurpose = "node_enroll"

// NodeEnrollReport is what the install script tells PSP about the panel it
// just prepared. Everything here is ATTACKER-CONTROLLED in the sense that it
// arrives over the network from a machine PSP has not yet authenticated —
// holding the one-time token is the only thing that got it this far. Nothing
// in it is trusted enough to store; it is only used to build candidate URLs,
// each of which must survive a real probe before anything is written.
type NodeEnrollReport struct {
	Scheme    string   // "http" | "https"
	Port      int      // the 3X-UI panel port
	BasePath  string   // the panel's webBasePath, "/" when none
	APIToken  string   // admin-scoped, non-expiring, minted by the script
	Addresses []string // addresses the node believes it is reachable at
	Hostname  string
}

// NodeEnrollCandidates builds the ordered list of panel URLs to try.
//
// The ordering is the whole design. "Which address do I put in?" has been the
// single most reliable way to get this wrong by hand, so enrollment does not
// ask and does not guess once — it produces every plausible answer and lets a
// real probe decide.
//
//	observed  where PSP SAW the callback come from. First, because it is the
//	          one address PSP has positive evidence it can route to — it just
//	          received a packet from it. Note it can be a proxy's address if
//	          PSP sits behind one, and under a permissive trusted_proxies it
//	          is header-derived and therefore spoofable, which is exactly why
//	          it is a candidate and not an answer.
//	reported  what the node thinks its own addresses are. Needed when the
//	          callback egresses through NAT or a different interface than the
//	          one the panel listens on, where `observed` is simply wrong.
//
// Addresses PSP's dial guard would refuse are dropped HERE rather than left to
// fail at probe time: a loopback candidate produces "refusing connection to
// non-public address", which reads as a bug rather than as "that address was
// never usable from here".
func NodeEnrollCandidates(r NodeEnrollReport, observed string, allowed func(ip net.IP) bool) ([]string, error) {
	scheme := strings.ToLower(strings.TrimSpace(r.Scheme))
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("%w: scheme must be http or https, got %q", ErrValidation, r.Scheme)
	}
	if r.Port <= 0 || r.Port > 65535 {
		return nil, fmt.Errorf("%w: port %d out of range", ErrValidation, r.Port)
	}

	// "/" and "" both mean "no base path"; anything else is normalised to a
	// single leading slash and no trailing one, because the adapter builds
	// requests as baseURL + "/panel/api/...".
	base := strings.TrimSpace(r.BasePath)
	base = strings.Trim(base, "/")
	if base != "" {
		base = "/" + base
	}

	seen := map[string]struct{}{}
	out := make([]string, 0, len(r.Addresses)+1)
	add := func(host string) {
		host = strings.TrimSpace(host)
		if host == "" {
			return
		}
		ip := net.ParseIP(host)
		if ip == nil {
			// A hostname cannot be checked against the dial policy here — the
			// guard runs post-DNS, inside the dialer. Kept as a candidate and
			// left for the probe to resolve and judge.
			if strings.ContainsAny(host, "/\\ ") {
				return
			}
		} else {
			if ip.To4() == nil {
				host = "[" + host + "]"
			}
			if allowed != nil && !allowed(ip) {
				return
			}
		}
		u := fmt.Sprintf("%s://%s:%d%s", scheme, host, r.Port, base)
		if _, dup := seen[u]; dup {
			return
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}

	add(observed)
	for _, a := range r.Addresses {
		add(a)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no reachable address for this node — every address it reported is one PSP is not allowed to dial", ErrValidation)
	}
	return out, nil
}

// NodeEnrollName derives the panel name PSP stores. The hostname is the
// operator's own word for the machine, so it beats an IP nobody recognises in
// a server list; it is sanitised because it arrives from an unauthenticated
// caller and lands in a uniquely-indexed column.
func NodeEnrollName(hostname, fallbackHost string) string {
	n := strings.TrimSpace(hostname)
	n = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '.', r == '_':
			return r
		}
		return -1
	}, n)
	if len(n) > 48 {
		n = n[:48]
	}
	n = strings.Trim(n, "-._")
	if n == "" {
		if h, _, err := net.SplitHostPort(fallbackHost); err == nil {
			n = h
		} else {
			n = strings.TrimSpace(fallbackHost)
		}
	}
	if n == "" {
		n = "node"
	}
	return n
}

// ParseEnrollHost pulls the host out of a candidate URL, for error messages
// that name the address rather than echoing a URL containing a token.
func ParseEnrollHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Host
}
