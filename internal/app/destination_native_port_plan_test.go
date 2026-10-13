package app

import (
	"strconv"
	"strings"
	"testing"
)

// Reserve explicit fixture server ports outside the kernel's automatic local
// range. Later httptest servers and control connections use automatic ports.
func nativeFixturePortRange(raw string) (first, last int, valid bool) {
	fields := strings.Fields(raw)
	if len(fields) != 2 {
		return 0, 0, false
	}
	lower, lowErr := strconv.Atoi(fields[0])
	upper, highErr := strconv.Atoi(fields[1])
	if lowErr != nil || highErr != nil || lower < 1 || upper > 65535 || lower > upper {
		return 0, 0, false
	}
	before, afterFirst := max(0, lower-1024), max(1024, upper+1)
	after := max(0, 65536-afterFirst)
	if before == 0 && after == 0 {
		return 0, 0, false
	}
	if before >= after {
		return 1024, lower - 1, true
	}
	return afterFirst, 65535, true
}

func TestNativeFixturePortsStayOutsideAutomaticRange(t *testing.T) {
	for _, row := range []struct {
		raw         string
		first, last int
	}{
		{"32768\t60999\n", 1024, 32767},
		{"1024 30000", 30001, 65535},
		{"1025 65535", 1024, 1024},
		{"1 65534", 65535, 65535},
	} {
		first, last, valid := nativeFixturePortRange(row.raw)
		if !valid || first != row.first || last != row.last || first < 1024 || last > 65535 || first > last {
			t.Fatal("fixture server range violates the explicit port boundary")
		}
	}
	for _, raw := range []string{"", "32768", "32768 60999 extra", "private 60999", "0 60999", "-1 60999", "65536 65536", "60999 32768", "1 65535", "1024 65535"} {
		if _, _, valid := nativeFixturePortRange(raw); valid {
			t.Fatal("invalid or exhausted automatic range accepted")
		}
	}
}
