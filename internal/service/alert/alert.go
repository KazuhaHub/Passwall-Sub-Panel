// Package alert is the unified notification center. It DERIVES alerts from
// current state on every request — node health, certificate status, panel
// versions, user expiry, recent lockouts — rather than maintaining an events
// table. A condition that clears simply stops producing its alert; there is no
// lifecycle to manage and the feed always reflects reality.
//
// One AlertService is the single source the admin top-bar bell and (via a
// drift test) the dashboard cards both rely on, so a category can't be shown in
// one place and silently dropped in the other.
package alert

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)


// defaultCertRenewBeforeDays is the cert-expiry lookahead used when the admin
// hasn't set CertRenewBeforeDays.
const defaultCertRenewBeforeDays = 14

// Severity orders an alert by how much it demands attention. The bell badge is
// coloured by the highest severity present.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Type identifies the alert category. The frontend maps it to a label, icon and
// deep link.
type Type string

const (
	TypeNodeHealth    Type = "node_health"
	TypeCertFailed    Type = "cert_failed"
	TypeCertExpiring  Type = "cert_expiring"
	TypePanelUpgrade  Type = "panel_upgrade"
	TypePSPUpgrade    Type = "psp_upgrade"
	TypeLoginSecurity Type = "login_security"
)

// Alert is one derived notification. Type-specific fields are optional; the
// frontend renders the title/message per Type from these structured values so
// no server-side localization is needed.
type Alert struct {
	Key      string   `json:"key"` // stable identity, e.g. "node_health:12"
	Type     Type     `json:"type"`
	Severity Severity `json:"severity"`

	TargetID   int64  `json:"target_id,omitempty"`
	TargetName string `json:"target_name,omitempty"`

	HealthState    string     `json:"health_state,omitempty"`    // node_health
	PanelName      string     `json:"panel_name,omitempty"`      // node_health
	LastError      string     `json:"last_error,omitempty"`      // cert_failed
	CurrentVersion string     `json:"current_version,omitempty"` // panel_upgrade / psp_upgrade
	LatestVersion  string     `json:"latest_version,omitempty"`  // panel_upgrade / psp_upgrade
	ExpireAt       *time.Time `json:"expire_at,omitempty"`       // cert_expiring
	Count          int        `json:"count,omitempty"`           // login_security
	Since          *time.Time `json:"since,omitempty"`
}

// AdminOnly reports whether this alert type deep-links to an admin-only page
// (certificates, 3X-UI servers). The feed hides these from operators so the
// bell never offers them a link to a page they're forbidden to open.
func (t Type) AdminOnly() bool {
	switch t {
	case TypeCertFailed, TypeCertExpiring, TypePanelUpgrade, TypePSPUpgrade:
		return true
	default:
		return false
	}
}

// Counts is the per-severity tally the bell badge renders.
type Counts struct {
	Error   int `json:"error"`
	Warning int `json:"warning"`
	Info    int `json:"info"`
}

// Tally counts alerts by severity. Single source used by List and by the
// handler after it role-filters, so the badge counts can't drift from the list.
func Tally(alerts []Alert) Counts {
	var c Counts
	for _, a := range alerts {
		switch a.Severity {
		case SeverityError:
			c.Error++
		case SeverityWarning:
			c.Warning++
		case SeverityInfo:
			c.Info++
		}
	}
	return c
}

// ---- narrow dependency interfaces (interface segregation) ----

type NodeLister interface {
	List(ctx context.Context) ([]*domain.Node, error)
}
type PanelLister interface {
	List(ctx context.Context) ([]*domain.XUIPanel, error)
}
type CertLister interface {
	ListByStatus(ctx context.Context, status domain.CertStatus) ([]*domain.TLSCertificate, error)
}
type EventCounter interface {
	CountByReasonSince(ctx context.Context, reason string, since time.Time) (int64, error)
}
type SettingsLoader interface {
	Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error)
}

// Deps wires the AlertService. Any source may be nil — its alert category is
// then simply skipped (e.g. no cert repo → no cert alerts).
type Deps struct {
	Nodes    NodeLister
	Panels   PanelLister
	Certs    CertLister
	Events   EventCounter
	Settings SettingsLoader
	// UpgradeFor reports the 3X-UI version a panel running `current` should be
	// nudged to upgrade to, or ("", false) when it's already at/above PSP's tested
	// ceiling. (v3.7.0: this is PSP's max_tested_xui, not the upstream latest — we
	// only nudge up to what PSP has verified.) nil → no panel_upgrade alerts.
	UpgradeFor func(current string) (latest string, available bool)
	// PSPUpgrade reports whether a newer STABLE PSP release exists than this build,
	// for the psp_upgrade alert. ("","",false) when up to date / unknown. Injected
	// so the service stays decoupled from the version package. nil → no psp_upgrade.
	PSPUpgrade func() (current, latest string, available bool)
	// Now defaults to time.Now.
	Now func() time.Time
}

// Service aggregates derived alerts from the wired sources.
type Service struct {
	d   Deps
	now func() time.Time
}

func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{d: d, now: now}
}

// List gathers every active alert (best-effort: a failing source is logged and
// skipped, never blanking the whole feed) and the per-severity counts.
func (s *Service) List(ctx context.Context) ([]Alert, Counts) {
	var out []Alert
	out = append(out, s.nodeHealth(ctx)...)
	out = append(out, s.panelUpgrades(ctx)...)
	out = append(out, s.pspUpgrade()...)
	out = append(out, s.certAlerts(ctx)...)
	out = append(out, s.loginSecurity(ctx)...)
	return out, Tally(out)
}

func (s *Service) nodeHealth(ctx context.Context) []Alert {
	if s.d.Nodes == nil {
		return nil
	}
	nodes, err := s.d.Nodes.List(ctx)
	if err != nil {
		log.Warn("alert: list nodes", "err", err)
		return nil
	}
	panelNames := s.panelNames(ctx)
	var bad []*domain.Node
	for _, n := range nodes {
		if n.IsSeparator() || !n.Enabled {
			continue
		}
		if n.HealthState == "" || n.HealthState == domain.NodeHealthOK {
			continue
		}
		bad = append(bad, n)
	}
	// Most-recently-checked first (mirrors the dashboard), nil checked-at last.
	sort.SliceStable(bad, func(i, j int) bool {
		ai, bi := bad[i].HealthCheckedAt, bad[j].HealthCheckedAt
		if ai == nil && bi == nil {
			return bad[i].ID < bad[j].ID
		}
		if ai == nil {
			return false
		}
		if bi == nil {
			return true
		}
		return ai.After(*bi)
	})
	out := make([]Alert, 0, len(bad))
	for _, n := range bad {
		out = append(out, Alert{
			Key:         "node_health:" + strconv.FormatInt(n.ID, 10),
			Type:        TypeNodeHealth,
			Severity:    SeverityError,
			TargetID:    n.ID,
			TargetName:  n.DisplayName,
			PanelName:   panelNames[n.PanelID],
			HealthState: string(n.HealthState),
			Since:       n.HealthCheckedAt,
		})
	}
	return out
}

func (s *Service) panelUpgrades(ctx context.Context) []Alert {
	if s.d.Panels == nil || s.d.UpgradeFor == nil {
		return nil
	}
	panels, err := s.d.Panels.List(ctx)
	if err != nil {
		log.Warn("alert: list panels", "err", err)
		return nil
	}
	var out []Alert
	for _, p := range panels {
		latest, ok := s.d.UpgradeFor(p.PanelVersion)
		if !ok {
			continue
		}
		out = append(out, Alert{
			Key:            "panel_upgrade:" + strconv.FormatInt(p.ID, 10) + ":" + latest,
			Type:           TypePanelUpgrade,
			Severity:       SeverityInfo,
			TargetID:       p.ID,
			TargetName:     p.Name,
			CurrentVersion: p.PanelVersion,
			LatestVersion:  latest,
		})
	}
	return out
}

func (s *Service) certAlerts(ctx context.Context) []Alert {
	if s.d.Certs == nil {
		return nil
	}
	var out []Alert
	// Failed issuance/renewal.
	if failed, err := s.d.Certs.ListByStatus(ctx, domain.CertStatusFailed); err != nil {
		log.Warn("alert: list failed certs", "err", err)
	} else {
		for _, c := range failed {
			out = append(out, Alert{
				Key:        "cert_failed:" + strconv.FormatInt(c.ID, 10),
				Type:       TypeCertFailed,
				Severity:   SeverityError,
				TargetID:   c.ID,
				TargetName: c.Name,
				LastError:  c.LastError,
			})
		}
	}
	// Active certs nearing expiry (warning) or already expired (error).
	renewDays := defaultCertRenewBeforeDays
	if s.d.Settings != nil {
		if set, err := s.d.Settings.Load(ctx, ports.UISettings{}); err == nil && set.CertRenewBeforeDays > 0 {
			renewDays = set.CertRenewBeforeDays
		}
	}
	now := s.now()
	cutoff := now.Add(time.Duration(renewDays) * 24 * time.Hour)
	if active, err := s.d.Certs.ListByStatus(ctx, domain.CertStatusActive); err != nil {
		log.Warn("alert: list active certs", "err", err)
	} else {
		for _, c := range active {
			if c.NotAfter == nil || c.NotAfter.After(cutoff) {
				continue
			}
			sev := SeverityWarning
			if c.NotAfter.Before(now) {
				sev = SeverityError
			}
			out = append(out, Alert{
				Key:        "cert_expiring:" + strconv.FormatInt(c.ID, 10),
				Type:       TypeCertExpiring,
				Severity:   sev,
				TargetID:   c.ID,
				TargetName: c.Name,
				ExpireAt:   c.NotAfter,
			})
		}
	}
	return out
}

// pspUpgrade emits a single admin-only alert when a newer STABLE PSP release is
// available than this build (the self-update nudge). Stable-only by construction
// — the injected PSPUpgrade reads version.LatestPSP() (GitHub /releases/latest,
// which excludes pre-releases), so running a beta only alerts once the stable it's
// behind ships, never to a newer beta.
func (s *Service) pspUpgrade() []Alert {
	if s.d.PSPUpgrade == nil {
		return nil
	}
	current, latest, ok := s.d.PSPUpgrade()
	if !ok || latest == "" {
		return nil
	}
	return []Alert{{
		Key:            "psp_upgrade:" + latest,
		Type:           TypePSPUpgrade,
		Severity:       SeverityInfo,
		CurrentVersion: current,
		LatestVersion:  latest,
	}}
}

func (s *Service) loginSecurity(ctx context.Context) []Alert {
	if s.d.Events == nil || s.d.Settings == nil {
		return nil
	}
	set, err := s.d.Settings.Load(ctx, ports.UISettings{})
	if err != nil || !set.LockoutEnabled {
		return nil
	}
	windowMin := set.LockoutWindowMinutes
	if windowMin <= 0 {
		windowMin = 15
	}
	now := s.now()
	since := now.Add(-time.Duration(windowMin) * time.Minute)
	count, err := s.d.Events.CountByReasonSince(ctx, domain.AuthReasonLockedOut, since)
	if err != nil {
		log.Warn("alert: count locked-out events", "err", err)
		return nil
	}
	if count <= 0 {
		return nil
	}
	return []Alert{{
		Key:      "login_security",
		Type:     TypeLoginSecurity,
		Severity: SeverityWarning,
		Count:    int(count),
		Since:    &since,
	}}
}

func (s *Service) panelNames(ctx context.Context) map[int64]string {
	names := map[int64]string{}
	if s.d.Panels == nil {
		return names
	}
	panels, err := s.d.Panels.List(ctx)
	if err != nil {
		return names
	}
	for _, p := range panels {
		names[p.ID] = p.Name
	}
	return names
}
