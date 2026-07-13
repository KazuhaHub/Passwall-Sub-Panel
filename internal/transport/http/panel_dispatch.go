package http

import (
	stdhttp "net/http"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/panelpath"
)

// panelDispatch keeps the externally visible panel prefix outside Gin's fixed
// route tree, so an administrator can change it without a restart.
func panelDispatch(paths *panelpath.Resolver, next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		prefix := paths.PanelPath()
		if prefix == "" {
			next.ServeHTTP(w, r)
			return
		}
		if paths.IsSubscription(r.URL.Path) || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/" {
			// panel_path is runtime-editable, so this redirect must never become a
			// sticky browser/CDN mapping to an old prefix.
			w.Header().Set("Cache-Control", "no-store")
			stdhttp.Redirect(w, r, prefix+"/", stdhttp.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
			stdhttp.NotFound(w, r)
			return
		}
		stripped := strings.TrimPrefix(r.URL.Path, prefix)
		if stripped == "" {
			stripped = "/"
		}
		// A panel-prefixed subscription must not become a root subscription
		// after prefix stripping.
		if paths.IsSubscription(stripped) {
			stdhttp.NotFound(w, r)
			return
		}
		r2 := r.Clone(panelpath.WithRequest(r.Context(), prefix))
		u := *r.URL
		u.Path = stripped
		r2.URL = &u
		r2.RequestURI = ""
		next.ServeHTTP(w, r2)
	})
}
