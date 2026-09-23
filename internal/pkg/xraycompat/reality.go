// Package xraycompat contains core/client interoperability decisions that are
// needed outside the panel-adapter version gate.
package xraycompat

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const MLKEMFirstRealityVersion = "26.9.8"

// RequiresMLKEMFirst reports whether an Xray server enforces the ML-KEM-first
// REALITY ClientHello introduced in 26.9.8. Invalid or absent observations are
// false: callers must not rewrite an old server's configuration based on a
// version they could not establish.
func RequiresMLKEMFirst(version string) bool {
	have, ok := parse(version)
	if !ok {
		return false
	}
	want, _ := parse(MLKEMFirstRealityVersion)
	return compare(have, want) >= 0
}

// MihomoNeedsMLKEM is the render-facing name for RequiresMLKEMFirst.
func MihomoNeedsMLKEM(version string) bool { return RequiresMLKEMFirst(version) }

// NormalizeRealityFingerprint makes the stored REALITY client template match
// the handshake shape required by Xray 26.9.8+. Keeping this in the inbound
// configuration avoids a hidden subscription-time fingerprint override: the
// admin form, PSP's desired snapshot, and every renderer now see "chrome".
// Unknown and older core versions are deliberately left alone.
func NormalizeRealityFingerprint(streamSettings, version string) (string, bool, error) {
	if !RequiresMLKEMFirst(version) {
		return streamSettings, false, nil
	}
	if strings.TrimSpace(streamSettings) == "" {
		return streamSettings, false, nil
	}
	var stream map[string]json.RawMessage
	if err := json.Unmarshal([]byte(streamSettings), &stream); err != nil {
		return "", false, fmt.Errorf("decode stream settings for REALITY compatibility: %w", err)
	}
	var security string
	if raw := stream["security"]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &security); err != nil {
			return "", false, fmt.Errorf("decode stream security: %w", err)
		}
	}
	if !strings.EqualFold(strings.TrimSpace(security), "reality") {
		return streamSettings, false, nil
	}

	reality := make(map[string]json.RawMessage)
	if raw := stream["realitySettings"]; len(raw) != 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &reality); err != nil {
			return "", false, fmt.Errorf("decode reality settings: %w", err)
		}
	}
	settings := make(map[string]json.RawMessage)
	if raw := reality["settings"]; len(raw) != 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return "", false, fmt.Errorf("decode REALITY client settings: %w", err)
		}
	}
	var fingerprint string
	if raw := settings["fingerprint"]; len(raw) != 0 {
		_ = json.Unmarshal(raw, &fingerprint)
	}
	if strings.EqualFold(strings.TrimSpace(fingerprint), "chrome") {
		return streamSettings, false, nil
	}
	settings["fingerprint"] = json.RawMessage(`"chrome"`)
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return "", false, fmt.Errorf("encode REALITY client settings: %w", err)
	}
	reality["settings"] = settingsJSON
	realityJSON, err := json.Marshal(reality)
	if err != nil {
		return "", false, fmt.Errorf("encode reality settings: %w", err)
	}
	stream["realitySettings"] = realityJSON
	normalized, err := json.Marshal(stream)
	if err != nil {
		return "", false, fmt.Errorf("encode stream settings: %w", err)
	}
	return string(normalized), true, nil
}

func parse(raw string) ([3]int, bool) {
	var zero [3]int
	value := strings.TrimSpace(raw)
	fields := strings.Fields(value)
	if len(fields) >= 2 && strings.EqualFold(fields[0], "xray") {
		value = fields[1]
	}
	value = strings.TrimPrefix(strings.TrimPrefix(value, "v"), "V")
	if index := strings.IndexAny(value, "-+"); index >= 0 {
		value = value[:index]
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return zero, false
	}
	var result [3]int
	for i, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 || strconv.Itoa(parsed) != part {
			return zero, false
		}
		result[i] = parsed
	}
	return result, true
}

func compare(left, right [3]int) int {
	for i := range left {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	return 0
}
