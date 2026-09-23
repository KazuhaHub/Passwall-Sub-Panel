package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type realityPanelRepo struct {
	ports.XUIPanelRepo
	version string
}

func (r realityPanelRepo) GetByID(context.Context, int64) (*domain.XUIPanel, error) {
	return &domain.XUIPanel{ID: 1, XrayVersion: r.version}, nil
}

func TestNormalizeRealityFingerprintAtInboundWriteBoundary(t *testing.T) {
	h := &AdminNodeHandler{panels: realityPanelRepo{version: "26.9.9"}}
	spec := ports.InboundSpec{StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"settings":{"fingerprint":"firefox"}}}`}
	if err := h.normalizeRealityFingerprint(context.Background(), 1, &spec); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(spec.StreamSettings, `"fingerprint":"chrome"`) || strings.Contains(spec.StreamSettings, "firefox") {
		t.Fatalf("stream settings were not normalized: %s", spec.StreamSettings)
	}
}

func TestNormalizeRealityFingerprintPreservesOlderXray(t *testing.T) {
	h := &AdminNodeHandler{panels: realityPanelRepo{version: "26.7.28"}}
	spec := ports.InboundSpec{StreamSettings: `{"security":"reality","realitySettings":{"settings":{"fingerprint":"firefox"}}}`}
	want := spec.StreamSettings
	if err := h.normalizeRealityFingerprint(context.Background(), 1, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.StreamSettings != want {
		t.Fatalf("older Xray config changed: %s", spec.StreamSettings)
	}
}
