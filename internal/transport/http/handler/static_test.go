package handler

import (
	"strings"
	"testing"
)

func TestRenderIndexInjectsPrefixWithoutInlineScript(t *testing.T) {
	body := []byte(`<head><!-- PSP_PANEL_BASE --></head>`)
	rendered := string(renderIndex(body, "/mypanel"))

	if !strings.Contains(rendered, `<base href="/mypanel/">`) {
		t.Fatalf("missing prefixed base tag: %s", rendered)
	}
	if !strings.Contains(rendered, `<meta name="psp-panel-path" content="/mypanel">`) {
		t.Fatalf("missing panel path meta tag: %s", rendered)
	}
	if strings.Contains(rendered, "<script>") {
		t.Fatalf("rendered index must not include an inline script: %s", rendered)
	}
}
