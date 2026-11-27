package handler

import (
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The sample nginx config only compresses the Content-Types listed in
// gzip_types. The panel picks its types in Go (mime.TypeByExtension for the
// embedded SPA, gin's JSON helper for /api, literals in the render package
// for /sub), so the two drifted once already: JS is served as text/javascript
// but the list only had application/javascript, leaving the SPA bundle
// uncompressed behind the sample config. This pins every emitted text type.
func sampleGzipTypes(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "nginx", "passwall-sub-panel.conf"))
	if err != nil {
		t.Fatalf("read sample nginx config: %v", err)
	}
	m := regexp.MustCompile(`(?s)gzip_types\s+([^;]+);`).FindSubmatch(b)
	if m == nil {
		t.Fatal("no gzip_types directive in sample nginx config")
	}
	got := map[string]bool{}
	for _, f := range strings.Fields(string(m[1])) {
		if !strings.HasPrefix(f, "#") { // tolerate no comments inside; defensive
			got[f] = true
		}
	}
	return got
}

func mediaType(ct string) string {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])
	}
	return mt
}

func TestSampleNginxGzipTypesCoverEmittedTextTypes(t *testing.T) {
	covered := sampleGzipTypes(t)

	emitted := map[string]string{
		"SPA .js (static.go, mime.TypeByExtension)":   mime.TypeByExtension(".js"),
		"SPA .css (static.go, mime.TypeByExtension)":  mime.TypeByExtension(".css"),
		"SPA .svg (static.go, mime.TypeByExtension)":  mime.TypeByExtension(".svg"),
		"SPA .json (static.go, mime.TypeByExtension)": mime.TypeByExtension(".json"),
		"JSON API (gin c.JSON)":                       "application/json; charset=utf-8",
	}
	// Subscription formats: read the literals out of the render package so a
	// new format added there cannot slip past this check.
	files, _ := filepath.Glob(filepath.Join("..", "..", "..", "service", "render", "*.go"))
	re := regexp.MustCompile(`ContentType:\s*"([^"]+)"`)
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range re.FindAllSubmatch(b, -1) {
			emitted["subscription ("+filepath.Base(f)+")"] = string(m[1])
			found++
		}
	}
	if found < 3 {
		t.Fatalf("expected the 3 subscription content types in render, found %d", found)
	}
	for src, ct := range emitted {
		if ct == "" {
			t.Errorf("%s: no content type resolved", src)
			continue
		}
		if mt := mediaType(ct); !covered[mt] {
			t.Errorf("%s emits %q, not in gzip_types of deploy/nginx/passwall-sub-panel.conf", src, mt)
		}
	}
}
