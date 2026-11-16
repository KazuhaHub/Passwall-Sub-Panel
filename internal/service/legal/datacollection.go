package legal

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Item is one line of the "what we collect" list. The wording lives in the
// front end's translations, keyed by the item's key; the backend only says
// what is true right now.
type Item struct {
	// Enabled is whether this is being recorded at all.
	Enabled bool `json:"enabled"`
	// RetentionDays is how long it is kept, nil where there is no retention to
	// state. 0 means kept for ever (only the subscription log and the sign-in
	// log can be set that way).
	RetentionDays *int `json:"retention_days,omitempty"`
	// Nodes is how many nodes record it (access-control items only).
	Nodes *int `json:"nodes,omitempty"`
	// IncludesIP is whether the record carries the client's IP address.
	IncludesIP bool `json:"includes_ip"`
	// EnabledGroups is set when the item is off panel-wide but a number of
	// groups turned it back on for themselves (risk signals): Enabled is then
	// true, and this is how many groups, never which.
	EnabledGroups *int `json:"enabled_groups,omitempty"`
}

// countCacheTTL is how long the database-derived counts are remembered. The
// public page is anonymous, so what one request can make the database do is
// bounded; the values read from the settings are never cached.
const countCacheTTL = 30 * time.Second

// memo remembers one successful answer for countCacheTTL. A failure is not
// remembered.
type memo[T any] struct {
	mu sync.Mutex
	at time.Time
	v  T
	ok bool
}

func (m *memo[T]) get(now time.Time, fn func() (T, error)) (T, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ok && now.Sub(m.at) < countCacheTTL {
		return m.v, nil
	}
	v, err := fn()
	if err != nil {
		var zero T
		return zero, err
	}
	m.v, m.at, m.ok = v, now, true
	return v, nil
}

// RiskOverrides counts the groups that override boolean settings
// (sqlstore.LegalRepo). Nil: no group is asked about.
type RiskOverrides interface {
	CountGroupsOverridingBool(ctx context.Context, typ string, names []string, want bool) (int, error)
}

// DestRecording is what the access-control items need to know. Nil: none of
// them is on.
type DestRecording interface {
	// CollectingNodes is how many native nodes are recording rule hits now.
	CollectingNodes(ctx context.Context) (int, error)
	// CollectingUsageNodes is how many native nodes record per-site usage now.
	CollectingUsageNodes(ctx context.Context) (int, error)
	// AllowlistTrialGroups is how many groups are in allowlist trial mode.
	AllowlistTrialGroups(ctx context.Context) (int, error)
}

// Provider produces one item. Key is the item's key in the document.
//
// This is the extension point: a feature that records something new
// registers a provider instead of editing the list. Gate, when set, decides
// from the settings whether the item is listed AT ALL (and Build is not
// called when it says no): an item an administrator has to opt in to disclose
// -- per-site usage, which PSP-U1 adds under the key "dest_usage" gated by
// legal_disclose_usage -- is simply absent from the page until they do.
type Provider struct {
	Key   string
	Gate  func(ports.UISettings) bool
	Build func(ctx context.Context, s ports.UISettings) (Item, error)
}

// Collector builds the list.
type Collector struct {
	providers []Provider
	now       func() time.Time

	nodes, trial, groups, usage memo[int]
	overrides                   RiskOverrides
}

// SetRiskOverrides gives the collector the group-override lookup the
// risk_signals item needs. Without it only the panel-wide switches are read.
func (c *Collector) SetRiskOverrides(o RiskOverrides) { c.overrides = o }

// NewCollector returns a collector with the built-in items.
func NewCollector(dest DestRecording) *Collector {
	c := &Collector{now: time.Now}
	days := func(v int) *int { return &v }
	static := func(key string, build func(s ports.UISettings) Item) {
		c.Register(Provider{Key: key, Build: func(_ context.Context, s ports.UISettings) (Item, error) { return build(s), nil }})
	}
	// Retentions are read the way the cleanup loops read them: Load already
	// applied the 7-day sub-log and 90-day sign-in defaults, and
	// RiskRuntimeFromSettings the connection and flag-record ones.
	static("sub_log", func(s ports.UISettings) Item {
		return Item{Enabled: true, RetentionDays: days(s.SubLogRetentionDays), IncludesIP: true}
	})
	static("auth_event", func(s ports.UISettings) Item {
		return Item{Enabled: true, RetentionDays: days(s.AuthEventRetentionDays), IncludesIP: true}
	})
	static("connection_history", func(s ports.UISettings) Item {
		rt := domain.RiskRuntimeFromSettings(s.RiskRuntimeSettings())
		return Item{Enabled: true, RetentionDays: days(rt.ConnectionRetentionDays), IncludesIP: true}
	})
	static("hwid", func(s ports.UISettings) Item { return Item{Enabled: !s.RiskHWIDCaptureOff} })
	static("flag_records", func(s ports.UISettings) Item {
		rt := domain.RiskRuntimeFromSettings(s.RiskRuntimeSettings())
		return Item{Enabled: true, RetentionDays: days(rt.FlagRecordRetentionDays)}
	})
	// Every one of the five signals can be switched back on for a group
	// (ports.OverridableScopeKeys) and the worker honours that, so "off
	// panel-wide" is not "not judged": the groups that turn one on are counted.
	c.Register(Provider{Key: "risk_signals", Build: func(ctx context.Context, s ports.UISettings) (Item, error) {
		signals := []struct {
			name string
			off  bool
		}{
			{"sub_spread_off", s.RiskSubSpreadOff}, {"devices_off", s.RiskDevicesOff}, {"dest_block_off", s.RiskDestBlockOff},
			{"usage_shift_off", s.RiskUsageShiftOff}, {"login_country_off", s.RiskLoginCountryOff},
		}
		var offNames []string
		for _, sig := range signals {
			if sig.off {
				offNames = append(offNames, sig.name)
			}
		}
		if len(offNames) < len(signals) {
			return Item{Enabled: true}, nil
		}
		if c.overrides == nil {
			return Item{}, nil
		}
		n, err := c.groups.get(c.now(), func() (int, error) {
			return c.overrides.CountGroupsOverridingBool(ctx, "risk", offNames, false)
		})
		if err != nil {
			return Item{}, fmt.Errorf("groups enabling risk signals: %w", err)
		}
		if n == 0 {
			return Item{}, nil
		}
		return Item{Enabled: true, EnabledGroups: &n}, nil
	}})
	// Admin review notes and dismissals: no address, no retention, removed with
	// the account.
	static("risk_reviews", func(ports.UISettings) Item { return Item{Enabled: true} })

	c.Register(Provider{Key: "dest_hits", Build: func(ctx context.Context, s ports.UISettings) (Item, error) {
		nodes, err := c.collectingNodes(ctx, dest)
		if err != nil {
			return Item{}, err
		}
		eff := ports.AccessControlEffectiveOf(s)
		return Item{Enabled: nodes > 0, Nodes: &nodes, RetentionDays: days(eff.HitRetentionDays)}, nil
	}})
	c.Register(Provider{Key: "dest_trial", Build: func(ctx context.Context, s ports.UISettings) (Item, error) {
		nodes, err := c.collectingNodes(ctx, dest)
		if err != nil {
			return Item{}, err
		}
		trial := 0
		if dest != nil {
			if trial, err = c.trial.get(c.now(), func() (int, error) { return dest.AllowlistTrialGroups(ctx) }); err != nil {
				return Item{}, fmt.Errorf("allowlist trial groups: %w", err)
			}
		}
		eff := ports.AccessControlEffectiveOf(s)
		return Item{Enabled: nodes > 0 && trial > 0, Nodes: &nodes, RetentionDays: days(eff.TrialRetentionDays)}, nil
	}})
	// Per-site usage (tier B) is an opt-in disclosure: the entry exists only
	// while the administrator has switched legal_disclose_usage on.
	c.Register(Provider{Key: "dest_usage", Gate: func(s ports.UISettings) bool { return s.LegalDiscloseUsage },
		Build: func(ctx context.Context, s ports.UISettings) (Item, error) {
			n := 0
			if dest != nil {
				var err error
				if n, err = c.usage.get(c.now(), func() (int, error) { return dest.CollectingUsageNodes(ctx) }); err != nil {
					return Item{}, fmt.Errorf("collecting usage nodes: %w", err)
				}
			}
			eff := ports.AccessControlEffectiveOf(s)
			return Item{Enabled: n > 0, Nodes: &n, RetentionDays: days(eff.UsageRetentionDays)}, nil
		}})
	return c
}

func (c *Collector) collectingNodes(ctx context.Context, dest DestRecording) (int, error) {
	if dest == nil {
		return 0, nil
	}
	n, err := c.nodes.get(c.now(), func() (int, error) { return dest.CollectingNodes(ctx) })
	if err != nil {
		return 0, fmt.Errorf("collecting nodes: %w", err)
	}
	return n, nil
}

// Register adds a provider. It panics on a key already registered: two
// writers of one disclosure line is a programming error found at start-up.
func (c *Collector) Register(p Provider) {
	for _, have := range c.providers {
		if have.Key == p.Key {
			panic("legal: data-collection item registered twice: " + p.Key)
		}
	}
	c.providers = append(c.providers, p)
}

// Collect builds the list from the settings. An item that cannot be built
// fails the whole list: a disclosure that silently dropped a line would
// understate what is recorded.
func (c *Collector) Collect(ctx context.Context, s ports.UISettings) (map[string]Item, error) {
	out := make(map[string]Item, len(c.providers))
	for _, p := range c.providers {
		if p.Gate != nil && !p.Gate(s) {
			continue
		}
		item, err := p.Build(ctx, s)
		if err != nil {
			return nil, fmt.Errorf("data collection %s: %w", p.Key, err)
		}
		out[p.Key] = item
	}
	return out, nil
}

// Keys lists the keys of the registered providers, sorted.
func (c *Collector) Keys() []string {
	keys := make([]string, len(c.providers))
	for i, p := range c.providers {
		keys[i] = p.Key
	}
	sort.Strings(keys)
	return keys
}
