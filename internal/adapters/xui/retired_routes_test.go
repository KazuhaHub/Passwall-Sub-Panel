package xui

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestRetiredRouteCleanupKeepsCustomRulesAndTemplateValues(t *testing.T) {
	const owned = "psp-vlessroute:11:8ab539e3-2f2d-4fea-93ee-418321c3f887"
	config := json.RawMessage(`{"routing":{"domainStrategy":"IPIfNonMatch","rules":[
		{"type":"field","inboundTag":["inbound-443"],"vlessRoute":"2","outboundTag":"custom"},
		{"type":"field","ruleTag":"` + owned + `","inboundTag":["inbound-443"],"vlessRoute":"17","outboundTag":"psp-target"},
		{"type":"field","ruleTag":"psp-vlessroute:11:operator","inboundTag":["inbound-443"],"vlessRoute":"21","outboundTag":"keep"},
		{"type":"field","ruleTag":"` + owned + `","outboundTag":"missing-match"}
	]},"unknown":9007199254740993}`)
	next, rules, err := retiredRouteCleanup(config)
	if err != nil || len(rules) != 1 || rules[0] != owned {
		t.Fatalf("cleanup returned %v, %v", rules, err)
	}
	out := string(next)
	if strings.Contains(out, "psp-target") || !strings.Contains(out, "9007199254740993") ||
		!strings.Contains(out, "IPIfNonMatch") || !strings.Contains(out, "missing-match") ||
		strings.Index(out, "custom") < 0 || strings.Index(out, "custom") >= strings.Index(out, "operator") {
		t.Fatalf("cleanup changed the retained template: %s", next)
	}
	again, rules, err := retiredRouteCleanup(next)
	if err != nil || len(rules) != 0 || !bytes.Equal(again, next) {
		t.Fatalf("repeated cleanup = %s, %v, %v", again, rules, err)
	}
}

func TestRetiredRoutePreviewIsBoundToTemplateAndTestURL(t *testing.T) {
	config, testURL, err := retiredRouteTemplate(json.RawMessage(`{"xraySetting":"{\"routing\":{\"rules\":[]}}","outboundTestUrl":"https://example.com/204"}`))
	if err != nil || string(config) != `{"routing":{"rules":[]}}` || testURL != "https://example.com/204" {
		t.Fatalf("decode returned %s, %q, %v", config, testURL, err)
	}
	rev := retiredRouteRevision(config, testURL)
	if len(rev) != 64 || rev == retiredRouteRevision(config, "") || rev == retiredRouteRevision(json.RawMessage(`{}`), testURL) {
		t.Fatal("the preview token did not distinguish template changes")
	}
}

func TestRetiredRouteCleanupAppliesOnlyTheCurrentPreview(t *testing.T) {
	const original = `{"routing":{"rules":[{"type":"field","ruleTag":"psp-vlessroute:11:8ab539e3-2f2d-4fea-93ee-418321c3f887","inboundTag":["inbound-443"],"vlessRoute":"17","outboundTag":"psp-target"}]}}`
	const testURL = "https://example.com/204"
	var mu sync.Mutex
	template := original
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		switch r.URL.Path {
		case "/panel/api/xray/":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "obj": map[string]string{"xraySetting": template, "outboundTestUrl": testURL},
			})
		case "/panel/api/xray/update":
			if r.FormValue("outboundTestUrl") != testURL {
				t.Error("cleanup changed the outbound test URL")
			}
			template = r.FormValue("xraySetting")
			writes++
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	client := &Client{baseURL: server.URL, http: server.Client(), apiToken: "t"}
	preview, err := client.RetiredRouteCleanup(t.Context(), "")
	if err != nil || len(preview.RuleTags) != 1 {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	mu.Lock()
	template = strings.TrimSuffix(original, "}") + `,"untouched":true}`
	mu.Unlock()

	if _, err := client.RetiredRouteCleanup(t.Context(), preview.Revision); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("the outdated revision was accepted: %v", err)
	}
	mu.Lock()
	if writes != 0 {
		t.Errorf("preview or stale request performed %d writes", writes)
	}
	mu.Unlock()

	preview, err = client.RetiredRouteCleanup(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RetiredRouteCleanup(t.Context(), preview.Revision); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if writes != 1 || !strings.Contains(template, `"untouched":true`) || strings.Contains(template, "psp-target") {
		t.Fatalf("cleanup writes = %d, template = %s", writes, template)
	}
}
