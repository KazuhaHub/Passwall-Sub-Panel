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
