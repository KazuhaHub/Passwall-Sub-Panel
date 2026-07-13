package handler

import (
	"net/http"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/panelpath"
	"github.com/gin-gonic/gin"
)

func cookieAuthPath(c *gin.Context) string {
	return panelpath.FromRequest(c.Request) + "/api"
}

// panelRedirect turns an internal SPA route into its public URL after a
// path-mounted SSO callback. relative must already be a safe local route.
func panelRedirect(c *gin.Context, relative string) string {
	return panelpath.FromRequest(c.Request) + "/" + strings.TrimLeft(relative, "/")
}

func currentPanelPath(r *http.Request) string { return panelpath.FromRequest(r) }
