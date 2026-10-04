package destpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

type retryListener struct {
	ID       string `json:"id"`
	Sniffing string `json:"sniffing"`
}

func PolicyContext(generation int64, capabilities []string, base protocol.ConfigBody) (string, error) {
	if generation < 0 {
		return "", invalid("published_generation")
	}
	engine := base.Core.Engine
	if engine == "" {
		engine = "xray"
	}
	var listeners []retryListener
	for _, listener := range base.Listeners {
		var config struct {
			Enabled  bool   `json:"enabled"`
			Sniffing string `json:"sniffing"`
		}
		if err := json.Unmarshal(listener.Config, &config); err != nil {
			// Invalid listener data keeps its ordinary compiler attribution. A
			// deterministic fingerprint must not turn it into a policy failure.
			listeners = append(listeners, retryListener{ID: string(listener.Key), Sniffing: "invalid:" + string(listener.Config)})
			continue
		}
		if !config.Enabled {
			continue
		}
		sniffing := strings.TrimSpace(config.Sniffing)
		if sniffing != "" {
			var parsed any
			if json.Unmarshal([]byte(sniffing), &parsed) == nil {
				canonical, err := json.Marshal(parsed)
				if err != nil {
					return "", err
				}
				sniffing = string(canonical)
			}
		}
		listeners = append(listeners, retryListener{ID: string(listener.Key), Sniffing: sniffing})
	}
	slices.SortFunc(listeners, func(a, b retryListener) int { return strings.Compare(a.ID, b.ID) })
	canonical, err := json.Marshal(struct {
		Generation          int64 `json:"generation"`
		Policy, Hits, Usage bool
		Engine, Version     string
		Listeners           []retryListener
	}{generation, slices.Contains(capabilities, protocol.CapabilityDestinationPolicy), slices.Contains(capabilities, "audit.hits.v1"), slices.Contains(capabilities, "audit.usage.v1"), engine, base.Core.Version, listeners})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}
