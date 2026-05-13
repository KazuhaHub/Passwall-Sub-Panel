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
	// Matched indicates whether a rule was matched.
	Matched bool
}

// Detect identifies the subscription client from the User-Agent and optional
// query parameter. The rules are evaluated in order; the first matching rule
// wins. If no rule matches, the default result (mihomo, allowed) is returned.
//
// Priority: UA detection only (query param is used later to override render
// format, not for access control).
func Detect(userAgent string, rules []ports.SubClientRule) Result {
	ua := strings.ToLower(userAgent)

	for _, rule := range rules {
		for _, kw := range rule.Keywords {
			if strings.Contains(ua, strings.ToLower(kw)) {
				return Result{
					ClientName:   rule.Name,
					RenderFormat: rule.RenderFormat,
					Matched:      true,
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
	default:
		return "mihomo"
	}
}
