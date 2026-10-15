package app

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
)

type nativeSetupProcCPU struct{ own, waited, birth uint64 }

func nativeSetupCPUParentDelta(before, after map[int]nativeSetupProcCPU) (own, waited uint64, valid bool) {
	if len(before) != 65 || len(after) != 65 {
		return 0, 0, false
	}
	const limit = uint64(1000000000000)
	for pid, previous := range before {
		current, found := after[pid]
		if pid <= 0 || !found || previous.birth == 0 || current.birth != previous.birth || current.own < previous.own || current.waited < previous.waited {
			return 0, 0, false
		}
		self, children := current.own-previous.own, current.waited-previous.waited
		if self > limit-own || children > limit-waited {
			return 0, 0, false
		}
		own, waited = own+self, waited+children
	}
	return own, waited, true
}

// Linux stat fields 16/17 account for children already reaped by this parent.
// They include CLI commands and exited cores, not just the currently live core.
func nativeSetupProcCPUReading(input string) (nativeSetupProcCPU, error) {
	own, birth, err := nativeLoadProcCounters(input)
	if err != nil {
		return nativeSetupProcCPU{}, err
	}
	fields := strings.Fields(input[strings.LastIndexByte(input, ')')+1:])
	user, e1 := strconv.ParseUint(fields[13], 10, 64)
	system, e2 := strconv.ParseUint(fields[14], 10, 64)
	if e1 != nil || e2 != nil || user > math.MaxUint64-system {
		return nativeSetupProcCPU{}, errors.New("invalid waited-child counters")
	}
	return nativeSetupProcCPU{own, user + system, birth}, nil
}

func TestNativeSetupCPUSeparatesOwnAndReapedChildTime(t *testing.T) {
	fields := strings.Fields("S 1 1 1 0 -1 0 0 0 0 0 7 3 13 17 0 0 2 0 123")
	build := func(values []string) string { return "42 (owned (parent) fixture) " + strings.Join(values, " ") }
	got, err := nativeSetupProcCPUReading(build(fields))
	if err != nil || got != (nativeSetupProcCPU{own: 10, waited: 30, birth: 123}) {
		t.Fatal("parent CPU, reaped child CPU and birth were conflated")
	}
	for _, change := range []func([]string){
		func(f []string) { f[13] = "-1" },
		func(f []string) { f[14] = "private" },
		func(f []string) { f[13], f[14] = "18446744073709551615", "1" },
		func(f []string) { f[19] = "0" },
	} {
		values := append([]string(nil), fields...)
		change(values)
		if _, err := nativeSetupProcCPUReading(build(values)); err == nil {
			t.Fatal("malformed or unbound CPU sample became a numeric observation")
		}
	}
	for _, value := range []string{"", "42 (owned) S 1", "42 no command"} {
		if _, err := nativeSetupProcCPUReading(value); err == nil {
			t.Fatal("incomplete process coordinates were accepted")
		}
	}
}

func TestNativeSetupCPUDeltaRequiresEveryOriginalParentAndMonotonicCounters(t *testing.T) {
	before := make(map[int]nativeSetupProcCPU)
	after := make(map[int]nativeSetupProcCPU)
	for pid := 1; pid <= 65; pid++ {
		before[pid] = nativeSetupProcCPU{10, 20, uint64(pid + 100)}
		after[pid] = nativeSetupProcCPU{17, 31, uint64(pid + 100)}
	}
	if own, waited, ok := nativeSetupCPUParentDelta(before, after); !ok || own != 455 || waited != 715 {
		t.Fatal("cohort CPU deltas were not computed from every bound original parent")
	}
	if own, waited, ok := nativeSetupCPUParentDelta(before, before); !ok || own != 0 || waited != 0 {
		t.Fatal("actual zero deltas were confused with missing evidence")
	}
	for _, change := range []func(map[int]nativeSetupProcCPU){
		func(v map[int]nativeSetupProcCPU) { delete(v, 1) },
		func(v map[int]nativeSetupProcCPU) { v[66] = nativeSetupProcCPU{1, 2, 3} },
		func(v map[int]nativeSetupProcCPU) { delete(v, 1); v[66] = nativeSetupProcCPU{17, 31, 101} },
		func(v map[int]nativeSetupProcCPU) { v[1] = nativeSetupProcCPU{17, 31, 999} },
		func(v map[int]nativeSetupProcCPU) {
			v[1], v[2] = nativeSetupProcCPU{9, 31, 101}, nativeSetupProcCPU{1000, 31, 102}
		},
		func(v map[int]nativeSetupProcCPU) { v[1] = nativeSetupProcCPU{17, 19, 101} },
		func(v map[int]nativeSetupProcCPU) { v[1] = nativeSetupProcCPU{math.MaxUint64, 31, 101} },
	} {
		values := make(map[int]nativeSetupProcCPU)
		for pid, value := range after {
			values[pid] = value
		}
		change(values)
		if own, waited, ok := nativeSetupCPUParentDelta(before, values); ok || own != 0 || waited != 0 {
			t.Fatal("missing/replaced/reset/out-of-bound process became a valid CPU delta")
		}
	}
}
