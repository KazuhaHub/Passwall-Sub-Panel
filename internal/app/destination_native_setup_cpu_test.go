//go:build destination_acceptance && linux

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Read-only optional setup diagnostics. No child is signaled, paused, restarted
// or substituted, and these observations cannot satisfy an acceptance gate.
func nativeSetupCPUSampler(t *testing.T, peers []*nativeGlobalPeer) func() {
	t.Helper()
	unavailable := func() { t.Log("native global setup cpu: unavailable=1") }
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	clock, err := exec.CommandContext(ctx, "getconf", "CLK_TCK").Output()
	cancel()
	ticks, parseErr := strconv.ParseUint(strings.TrimSpace(string(clock)), 10, 64)
	if err != nil || parseErr != nil || ticks == 0 || ticks > 1000000 || len(peers) != 65 {
		unavailable()
		return func() {}
	}
	type sample struct {
		core    uint64
		parents map[int]nativeSetupProcCPU
		cores   map[int]uint64
	}
	read := func() (sample, bool) {
		out := sample{parents: map[int]nativeSetupProcCPU{}, cores: map[int]uint64{}}
		for _, peer := range peers {
			if peer == nil || peer.process == nil || peer.process.Process == nil {
				return sample{}, false
			}
			pid := peer.process.Process.Pid
			path := filepath.Join("/proc", strconv.Itoa(pid))
			file, e1 := os.Stat(peer.process.Path)
			live, e2 := os.Stat(filepath.Join(path, "exe"))
			data, e3 := os.ReadFile(filepath.Join(path, "stat"))
			parent, e4 := nativeSetupProcCPUReading(string(data))
			if e1 != nil || e2 != nil || e3 != nil || e4 != nil || !os.SameFile(file, live) || out.parents[pid].birth != 0 {
				return sample{}, false
			}
			out.parents[pid] = parent
			found := 0
			for _, child := range nativeCoreChildren(pid) {
				if !nativeCoreHasParent(child, pid) || !nativeOwnedLiveCoreCommand(nativeCoreArgs(child), "xray", filepath.Join(peer.dir, "node", "runtime", "xray")) {
					continue
				}
				data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(child), "stat"))
				core, parseErr := nativeSetupProcCPUReading(string(data))
				if err != nil || parseErr != nil || out.cores[child] != 0 || core.own > 1000000000000-out.core {
					return sample{}, false
				}
				again, readErr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(child), "stat"))
				confirmed, confirmErr := nativeSetupProcCPUReading(string(again))
				if readErr != nil || confirmErr != nil || confirmed.birth != core.birth || !nativeCoreHasParent(child, pid) || !nativeOwnedLiveCoreCommand(nativeCoreArgs(child), "xray", filepath.Join(peer.dir, "node", "runtime", "xray")) {
					return sample{}, false
				}
				out.cores[child] = core.birth
				out.core += core.own
				found++
			}
			if found != 1 {
				return sample{}, false
			}
			again, readErr := os.ReadFile(filepath.Join(path, "stat"))
			confirmed, confirmErr := nativeSetupProcCPUReading(string(again))
			confirmedFile, fileErr := os.Stat(filepath.Join(path, "exe"))
			if readErr != nil || confirmErr != nil || fileErr != nil || confirmed.birth != parent.birth || !os.SameFile(file, confirmedFile) {
				return sample{}, false
			}
		}
		return out, true
	}
	start := time.Now()
	baseline, ok := read()
	if !ok {
		unavailable()
		return func() {}
	}
	samples := 0
	return func() {
		samples++
		t.Logf("native global setup cpu: sample=%d", samples)
		began := time.Now()
		current, ok := read()
		own, waited, deltaOK := nativeSetupCPUParentDelta(baseline.parents, current.parents)
		if !ok || !deltaOK {
			unavailable()
			return
		}
		changed := 0
		for pid, birth := range current.cores {
			if baseline.cores[pid] != birth {
				changed++
			}
		}
		for _, fact := range []struct {
			name  string
			value uint64
		}{
			{"elapsed_ms", uint64(time.Since(start).Milliseconds())}, {"sample_ms", uint64(time.Since(began).Milliseconds())},
			{"parent_ticks", own}, {"waited_child_ticks", waited},
			{"live_core_ticks", current.core}, {"changed_core_identities", uint64(changed)},
			{"parents", uint64(len(current.parents))}, {"cores", uint64(len(current.cores))},
			{"ticks_per_second", ticks}, {"logical_cpus", uint64(runtime.NumCPU())},
		} {
			t.Logf("native global setup cpu: %s=%d", fact.name, fact.value)
		}
	}
}
