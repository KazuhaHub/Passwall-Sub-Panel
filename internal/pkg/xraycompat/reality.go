// Package xraycompat contains core/client interoperability decisions that are
// needed outside the panel-adapter version gate.
package xraycompat

import (
	"strconv"
	"strings"
)

const MLKEMFirstRealityVersion = "26.9.8"

// MihomoNeedsMLKEM reports whether an Xray server enforces the ML-KEM-first
// REALITY ClientHello introduced in 26.9.8. Invalid or absent observations are
// false: callers must not rewrite an old server's client fingerprint based on
// a version they could not establish.
func MihomoNeedsMLKEM(version string) bool {
	have, ok := parse(version)
	if !ok {
		return false
	}
	want, _ := parse(MLKEMFirstRealityVersion)
	return compare(have, want) >= 0
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
