// Package panelpath owns the externally visible path prefix of the web panel.
// The prefix is deliberately kept separate from the subscription path: the
// latter is a stable public endpoint and can remain at the origin root.
package panelpath

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type requestKey struct{}

// WithRequest records the external panel prefix after a dispatcher strips it
// before handing the request to Gin.
func WithRequest(ctx context.Context, prefix string) context.Context {
	return context.WithValue(ctx, requestKey{}, prefix)
}

// FromRequest returns the external panel prefix for a dispatched request.
func FromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	v, _ := r.Context().Value(requestKey{}).(string)
	return v
}

// Normalize accepts an empty prefix (root deployment) or a clean absolute
// multi-segment URL path. It intentionally accepts only unreserved URL
// characters: this keeps reverse-proxy and cookie behaviour unambiguous.
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "/" {
		return "", nil
	}
	if !strings.HasPrefix(raw, "/") || strings.ContainsAny(raw, "?#\\") || strings.Contains(raw, "//") {
		return "", fmt.Errorf("panel path must be an absolute URL path")
	}
	clean := path.Clean(raw)
	if clean != raw || clean == "." || clean == "/" {
		return "", fmt.Errorf("panel path must not contain empty, dot, or trailing segments")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(clean, "/"), "/") {
		if segment == "" {
			return "", fmt.Errorf("panel path contains an empty segment")
		}
		for _, r := range segment {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == '~') {
				return "", fmt.Errorf("panel path contains unsupported characters")
			}
		}
	}
	return clean, nil
}

func overlaps(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// Validate checks the panel prefix against root endpoints and subscription
// routing. subPath is the configured segment without its leading slash.
func Validate(panelPath, subPath string) error {
	if panelPath == "" {
		return nil
	}
	if panelPath == "/api" || strings.HasPrefix(panelPath, "/api/") || panelPath == "/health" || strings.HasPrefix(panelPath, "/health/") {
		return fmt.Errorf("panel path overlaps a reserved root endpoint")
	}
	subPath = strings.Trim(subPath, "/")
	if subPath == "" {
		subPath = "sub"
	}
	if overlaps(panelPath, "/"+subPath) {
		return fmt.Errorf("panel path overlaps the subscription path")
	}
	return nil
}

// PublicOrigin validates the canonical public URL used in email and SSO URLs.
// A path is rejected when the panel itself is path-mounted, avoiding ambiguous
// combinations such as both sub_base_url=/foo and panel_path=/panel.
func PublicOrigin(raw string, pathMounted bool) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("public base URL must be an http(s) origin")
	}
	if pathMounted && u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("public base URL must not contain a path when panel_path is set")
	}
	return strings.TrimRight(raw, "/"), nil
}

// PanelURL joins an origin and panel-relative path. It intentionally returns a
// relative URL when origin is empty, preserving direct/local deployments.
func PanelURL(origin, panelPath, relative string) string {
	relative = "/" + strings.TrimLeft(relative, "/")
	return strings.TrimRight(origin, "/") + panelPath + relative
}

// PanelBase is the absolute panel root for email/logo rendering. Unlike
// PanelURL it preserves an empty origin, because callers must not turn an
// unknown public address into a misleading protocol-relative URL.
func PanelBase(origin, panelPath string) string {
	if strings.TrimSpace(origin) == "" {
		return ""
	}
	return strings.TrimRight(PanelURL(origin, panelPath, "/"), "/")
}

type snapshot struct {
	panel string
	sub   string
	next  time.Time
}

// Resolver is a small invalidatable cache used at the HTTP boundary. A DB read
// on every static asset request would be disproportionately expensive.
type Resolver struct {
	repo      ports.SettingsRepo
	mu        sync.RWMutex
	refreshMu sync.Mutex
	s         snapshot
}

func NewResolver(repo ports.SettingsRepo) *Resolver { return &Resolver{repo: repo} }

func (r *Resolver) refresh() snapshot {
	// Collapse concurrent stale reads into one settings query. Static assets
	// commonly arrive in a burst immediately after the cache expires.
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()

	r.mu.RLock()
	current := r.s
	r.mu.RUnlock()
	if !current.next.IsZero() && time.Now().Before(current.next) {
		return current
	}

	next := time.Now().Add(5 * time.Second)
	got, err := r.repo.Load(context.Background(), ports.UISettings{SubPath: "sub"})
	if err != nil {
		// A transient settings-store outage must not silently move a mounted
		// panel back to the origin root. Preserve the last known-good snapshot
		// and retry after the normal short TTL. On first boot there is no known
		// value, so retain the historical root/sub defaults.
		if current.sub == "" {
			current.sub = "sub"
		}
		current.next = next
		r.mu.Lock()
		r.s = current
		r.mu.Unlock()
		return current
	}

	p, err := Normalize(got.PanelPath)
	if err != nil {
		// Treat corrupt persisted data like a read failure: keep serving the
		// last configuration instead of failing open at the root.
		if current.sub == "" {
			current.sub = "sub"
		}
		current.next = next
		r.mu.Lock()
		r.s = current
		r.mu.Unlock()
		return current
	}
	s := snapshot{panel: p, sub: "sub", next: next}
	if sub := strings.Trim(got.SubPath, "/"); sub != "" {
		s.sub = sub
	}
	r.mu.Lock()
	r.s = s
	r.mu.Unlock()
	return s
}

func (r *Resolver) current() snapshot {
	r.mu.RLock()
	s := r.s
	r.mu.RUnlock()
	if s.next.IsZero() || time.Now().After(s.next) {
		return r.refresh()
	}
	return s
}

func (r *Resolver) Invalidate()       { r.mu.Lock(); r.s.next = time.Time{}; r.mu.Unlock() }
func (r *Resolver) PanelPath() string { return r.current().panel }
func (r *Resolver) IsSubscription(p string) bool {
	return strings.HasPrefix(p, "/"+r.current().sub+"/")
}
