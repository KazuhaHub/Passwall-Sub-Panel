package ports

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

// TransitProbeSettings is the link-probe settings (transit.probe.v1): EVERY
// transit_probe_ field of UISettings, owned by /api/admin/transit/probe-settings
// the same way the access-control page owns its dest_ keys. The system settings
// PUT keeps them exactly as stored, so a tab opened before another admin's save
// cannot revert it.
//
// 0 means "not configured" and runs as the shipped default
// (TransitProbeEffectiveOf); an empty URL is NOT "not configured": it means
// transport-only (no end-to-end fetch), and the view carries the real URL (the
// default filled in for a panel that never set one). A number typed out of range is refused, never
// clamped, because a probe interval of 5 seconds that silently runs as 30 is a
// setting that lies. Probing is stored inverted (TransitProbeOff) so that a
// database that never saw these keys probes, which is the shipped default; the
// API says "enabled".
type TransitProbeSettings struct {
	TransitProbeEnabled         bool   `json:"transit_probe_enabled"`
	TransitProbeIntervalSeconds int    `json:"transit_probe_interval_seconds"`
	TransitProbeTimeoutMS       int    `json:"transit_probe_timeout_ms"`
	TransitProbeURL             string `json:"transit_probe_url"`
	TransitProbeWithhold        bool   `json:"transit_probe_withhold"`
	TransitProbeWithholdAfter   int    `json:"transit_probe_withhold_after"`
	TransitProbeRestoreAfter    int    `json:"transit_probe_restore_after"`
}

// TransitProbeSettings copies the settings out of the record.
func (s UISettings) TransitProbeSettings() TransitProbeSettings {
	return TransitProbeSettings{
		TransitProbeEnabled:         !s.TransitProbeOff,
		TransitProbeIntervalSeconds: s.TransitProbeIntervalSeconds,
		TransitProbeTimeoutMS:       s.TransitProbeTimeoutMS,
		TransitProbeURL:             effectiveProbeURL(s),
		TransitProbeWithhold:        s.TransitProbeWithhold,
		TransitProbeWithholdAfter:   s.TransitProbeWithholdAfter,
		TransitProbeRestoreAfter:    s.TransitProbeRestoreAfter,
	}
}

// SetTransitProbeSettings copies the settings into the record and touches no
// other field.
func (s *UISettings) SetTransitProbeSettings(p TransitProbeSettings) {
	s.TransitProbeOff = !p.TransitProbeEnabled
	s.TransitProbeIntervalSeconds = p.TransitProbeIntervalSeconds
	s.TransitProbeTimeoutMS = p.TransitProbeTimeoutMS
	s.TransitProbeURL = strings.TrimSpace(p.TransitProbeURL)
	s.TransitProbeURLOff = s.TransitProbeURL == ""
	s.TransitProbeWithhold = p.TransitProbeWithhold
	s.TransitProbeWithholdAfter = p.TransitProbeWithholdAfter
	s.TransitProbeRestoreAfter = p.TransitProbeRestoreAfter
}

// Shipped defaults and bounds. The interval and timeout bounds are the
// protocol's, so a stored value can always be sent.
const (
	TransitProbeIntervalDefault = 60
	TransitProbeIntervalMin     = nodeprotocol.MinProbeIntervalSeconds
	TransitProbeIntervalMax     = nodeprotocol.MaxProbeIntervalSeconds
	TransitProbeTimeoutDefault  = 5000
	TransitProbeTimeoutMin      = nodeprotocol.MinProbeTimeoutMS
	TransitProbeTimeoutMax      = nodeprotocol.MaxProbeTimeoutMS
	TransitProbeURLDefault      = "https://www.gstatic.com/generate_204"
	TransitProbeAfterDefault    = 3
	TransitProbeAfterMin        = 1
	TransitProbeAfterMax        = 20
)

// TransitProbeEffective is what the panel runs with for the stored settings.
type TransitProbeEffective struct {
	Enabled         bool `json:"transit_probe_enabled"`
	IntervalSeconds int  `json:"transit_probe_interval_seconds"`
	TimeoutMS       int  `json:"transit_probe_timeout_ms"`
	// URL is what links fetch; "" is transport-only.
	URL           string `json:"transit_probe_url"`
	Withhold      bool   `json:"transit_probe_withhold"`
	WithholdAfter int    `json:"transit_probe_withhold_after"`
	RestoreAfter  int    `json:"transit_probe_restore_after"`
}

// Interval is the time between probe rounds.
func (e TransitProbeEffective) Interval() time.Duration {
	return time.Duration(e.IntervalSeconds) * time.Second
}

// effectiveProbeURL is the URL links fetch: "" when transport-only, the stored
// URL, or the default when none was ever stored (or one that cannot have been
// saved is).
func effectiveProbeURL(s UISettings) string {
	if s.TransitProbeURLOff {
		return ""
	}
	stored := strings.TrimSpace(s.TransitProbeURL)
	if stored == "" || ValidateTransitProbeURL(stored) != nil {
		return TransitProbeURLDefault
	}
	return stored
}

// TransitProbeEffectiveOf applies the defaults. A stored value is used as
// stored: validation refuses a bad one at the PUT, and one that got into the
// database some other way is the default rather than something the protocol
// would refuse to carry.
func TransitProbeEffectiveOf(s UISettings) TransitProbeEffective {
	pick := func(v, def, lo, hi int) int {
		if v < lo || v > hi {
			return def
		}
		return v
	}
	probeURL := effectiveProbeURL(s)
	return TransitProbeEffective{
		Enabled:         !s.TransitProbeOff,
		IntervalSeconds: pick(s.TransitProbeIntervalSeconds, TransitProbeIntervalDefault, TransitProbeIntervalMin, TransitProbeIntervalMax),
		TimeoutMS:       pick(s.TransitProbeTimeoutMS, TransitProbeTimeoutDefault, TransitProbeTimeoutMin, TransitProbeTimeoutMax),
		URL:             probeURL,
		Withhold:        s.TransitProbeWithhold,
		WithholdAfter:   pick(s.TransitProbeWithholdAfter, TransitProbeAfterDefault, TransitProbeAfterMin, TransitProbeAfterMax),
		RestoreAfter:    pick(s.TransitProbeRestoreAfter, TransitProbeAfterDefault, TransitProbeAfterMin, TransitProbeAfterMax),
	}
}

// TransitProbeSettingsDefaults is the shipped value of every key, for the
// page's placeholders.
func TransitProbeSettingsDefaults() map[string]any {
	return map[string]any{
		"transit_probe_enabled":          true,
		"transit_probe_interval_seconds": TransitProbeIntervalDefault,
		"transit_probe_timeout_ms":       TransitProbeTimeoutDefault,
		"transit_probe_url":              TransitProbeURLDefault,
		"transit_probe_withhold":         false,
		"transit_probe_withhold_after":   TransitProbeAfterDefault,
		"transit_probe_restore_after":    TransitProbeAfterDefault,
	}
}

// ValidateTransitProbeURL accepts an absolute http or https URL with a host,
// no credentials and a port in 1..65535 when it names one. The protocol's own
// check (rc.1) does not look at the port range, so a URL with port 0 or 70000
// would be sent and every probe would fail; it is refused here instead.
func ValidateTransitProbeURL(raw string) error {
	if len(raw) > nodeprotocol.MaxProbeLinkURLBytes {
		return fmt.Errorf("exceeds %d bytes", nodeprotocol.MaxProbeLinkURLBytes)
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] <= ' ' || raw[i] >= 0x7f {
			return fmt.Errorf("contains a space, control or non-ASCII byte")
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("is not a URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("scheme %q is not http or https", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("has no host")
	}
	if parsed.User != nil {
		return fmt.Errorf("carries credentials")
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return fmt.Errorf("has an empty port")
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("port %q is outside 1..65535", port)
		}
	}
	return nil
}

// ValidateTransitProbeSettings checks a settings update. 0 and "" stay valid
// (the default). On refusal it names the json key at fault.
func ValidateTransitProbeSettings(s TransitProbeSettings) (field string, err error) {
	check := func(key string, v, lo, hi int) (string, error) {
		if v != 0 && (v < lo || v > hi) {
			return key, fmt.Errorf("%s must be 0 (the default) or between %d and %d", key, lo, hi)
		}
		return "", nil
	}
	if f, err := check("transit_probe_interval_seconds", s.TransitProbeIntervalSeconds, TransitProbeIntervalMin, TransitProbeIntervalMax); err != nil {
		return f, err
	}
	if f, err := check("transit_probe_timeout_ms", s.TransitProbeTimeoutMS, TransitProbeTimeoutMin, TransitProbeTimeoutMax); err != nil {
		return f, err
	}
	if f, err := check("transit_probe_withhold_after", s.TransitProbeWithholdAfter, TransitProbeAfterMin, TransitProbeAfterMax); err != nil {
		return f, err
	}
	if f, err := check("transit_probe_restore_after", s.TransitProbeRestoreAfter, TransitProbeAfterMin, TransitProbeAfterMax); err != nil {
		return f, err
	}
	if s.TransitProbeURL != "" {
		if err := ValidateTransitProbeURL(s.TransitProbeURL); err != nil {
			return "transit_probe_url", fmt.Errorf("transit_probe_url %w", err)
		}
	}
	return "", nil
}
