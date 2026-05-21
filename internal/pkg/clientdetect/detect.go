// Package clientdetect provides subscription client detection based on
// User-Agent string and optional query parameter override.
package clientdetect

import (
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Result holds the detection outcome.
type Result struct {
	// ClientName is the matched rule's name, or "other" if no match.
	ClientName string
	// RenderFormat is "mihomo" or "sing-box".
	RenderFormat string
	// Matched indicates whether a family was matched.
	Matched bool
	// Enabled is the matched family's enabled flag (false when no match).
	Enabled bool
}

// Detect identifies the subscription client from the User-Agent by matching it
// against the detection families' keywords. Families are evaluated in order;
// the first whose keyword is a substring of the UA wins. If none match, the
// default result (mihomo, allowed) is returned.
//
// Priority: UA detection only (the ?client= query param overrides the render
// format later, not access control).
func Detect(userAgent string, families []ports.SubClientFamily) Result {
	ua := strings.ToLower(userAgent)

	for _, fam := range families {
		for _, kw := range fam.Keywords {
			if kw != "" && strings.Contains(ua, strings.ToLower(kw)) {
				return Result{
					ClientName:   fam.Name,
					RenderFormat: fam.RenderFormat,
					Matched:      true,
					Enabled:      fam.Enabled,
				}
			}
		}
	}

	// No match — default to mihomo.
	return Result{
		ClientName:   "other",
		RenderFormat: "mihomo",
		Matched:      false,
	}
}

// NormalizeRenderFormat maps common client names to render formats.
// Used when ?client=xxx overrides the UA-detected format.
func NormalizeRenderFormat(client string) string {
	c := strings.ToLower(strings.TrimSpace(client))
	switch c {
	case "sing-box", "singbox", "sing_box":
		return "sing-box"
	case "uri-list", "uri_list", "urilist", "v2ray", "v2rayn", "passwall", "shadowrocket":
		return "uri-list"
	default:
		return "mihomo"
	}
}

// Filter modes for the subscription client gate.
const (
	FilterBlacklist = "blacklist"
	FilterWhitelist = "whitelist"
)

// ClientBlocked decides whether a subscription request should be blocked, given
// the configured filter mode and the UA detection result:
//   - blacklist (default): block only a client that matched a DISABLED family;
//     unknown / unmatched clients pass through.
//   - whitelist: block anything that isn't a matched AND enabled family, so
//     unknown clients are blocked too (no need for an explicit "other" family).
func ClientBlocked(mode string, r Result) bool {
	if mode == FilterWhitelist {
		return !(r.Matched && r.Enabled)
	}
	return r.Matched && !r.Enabled
}
