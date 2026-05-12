// Package web embeds the built Vue SPA assets so the panel ships as a
// single binary in production. During development the assets are served by
// Vite's dev server; in production `npm run build` writes the dist into
// this package's dist/ subdirectory and `go build` picks them up via the
// directive below.
package web

import "embed"

//go:embed all:dist
var DistFS embed.FS
