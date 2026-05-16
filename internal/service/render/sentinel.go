package render

// proxySentinel returns a tiny DIRECT proxy that the render pipeline drops
// into an otherwise empty proxies/outbounds list. Reason: Clash Meta for
// Android (and most Clash-family clients) reject a profile that has either
// no proxies key at all OR a `proxies: []` empty list, with errors like
// "profile does not contain `proxies` or `proxy-providers`". A single
// no-op DIRECT entry satisfies the parser while making it visually obvious
// to the admin that the group rendered without any real nodes.
//
// The name "PSP-NoNodes" is deliberately panel-prefixed so it's easy to
// recognize as a panel-injected placeholder rather than a real node from
// 3X-UI.
func proxySentinel() map[string]any {
	return map[string]any{
		"name": "PSP-NoNodes",
		"type": "direct",
		"udp":  true,
	}
}

// withSentinelIfEmpty returns proxies unchanged when non-empty, otherwise a
// single-element slice containing the sentinel. Pure function so the
// behaviour is unit-testable without dragging in repos/syncer fakes.
func withSentinelIfEmpty(proxies []map[string]any) []map[string]any {
	if len(proxies) > 0 {
		return proxies
	}
	return []map[string]any{proxySentinel()}
}
