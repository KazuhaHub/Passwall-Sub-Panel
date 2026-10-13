//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type nativeIdleCostProbe struct {
	receipt  nativeRestartProbe
	accepted atomic.Uint64
}

func (p *nativeIdleCostProbe) wrap(next http.Handler) http.Handler {
	return p.receipt.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := &nativeMixedResponse{ResponseWriter: w}
		next.ServeHTTP(response, r)
		if r.Method == "POST" && r.URL.Path == "/v1/node/sync" && response.status == 200 {
			p.accepted.Add(1)
		}
	}))
}

type nativeIdleWrites struct {
	DB      uint64 `json:"db_bytes"`
	WAL     uint64 `json:"wal_bytes"`
	Journal uint64 `json:"journal_bytes"`
	Calls   uint64 `json:"write_calls"`
	Syncs   uint64 `json:"sync_calls"`
}

type nativeIdleCycle struct {
	Phase    string            `json:"phase"`
	Index    int               `json:"index"`
	Elapsed  int64             `json:"elapsed_ms"`
	AgentCPU uint64            `json:"agent_cpu_ticks"`
	CoreCPU  uint64            `json:"core_cpu_ticks"`
	Accepted uint64            `json:"accepted_syncs"`
	Writes   *nativeIdleWrites `json:"state_writes"`
}

type nativeIdleWindow struct {
	From  int64 `json:"from_unix_us"`
	Until int64 `json:"until_unix_us"`
}

// This is a real idle runtime gate, not a generated-config size benchmark.
// CPU is measured first without ptrace, then in explicitly instrumented cycles.
// State bytes are successful syscalls to the exact owned DB/WAL/journal; they
// are not physical disk writes, file growth, or the agent's total process I/O.
func nativeIdleCost(t *testing.T, f destinationPolicyFixture, client *http.Client, endpoint, token string, parent int, dataDir string, inbound int, address string, port int, nonce, evidence string, ticks uint64, probe *nativeIdleCostProbe) {
	a := f.a
	m := struct {
		Schema      int               `json:"schema_version"`
		Candidate   string            `json:"candidate"`
		Core        string            `json:"core_version"`
		Domains     int               `json:"published_domains"`
		Applied     int               `json:"applied_domains"`
		CoreDomains int               `json:"core_domains"`
		Ticks       uint64            `json:"cpu_ticks_per_second"`
		Controls    int               `json:"control_successes"`
		TracerBound int               `json:"tracer_bound"`
		Cycles      []nativeIdleCycle `json:"cycles"`
	}{Schema: 1, Candidate: "184f4e23f0f3d72f8492432184685fea4ec4967c", Core: "26.6.27", Ticks: ticks, Cycles: []nativeIdleCycle{}}
	t.Cleanup(func() {
		data, err := json.Marshal(m)
		nativeMust(t, err, "encode numeric idle cost evidence")
		nativeMust(t, os.WriteFile(evidence, append(data, '\n'), 0644), "save numeric idle cost evidence")
	})
	request := func(method, path string, input any, want int, output any) {
		data, err := json.Marshal(input)
		nativeMust(t, err, "encode idle administrator mutation")
		req, err := http.NewRequestWithContext(t.Context(), method, endpoint+"/api/admin/"+path, bytes.NewReader(data))
		nativeMust(t, err, "create idle administrator request")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		nativeMust(t, err, "submit idle administrator mutation")
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatal("idle administrator rejected owned input; body withheld")
		}
		if output != nil {
			nativeMust(t, json.NewDecoder(response.Body).Decode(output), "read idle administrator result")
		} else {
			_, _ = io.Copy(io.Discard, response.Body)
		}
	}
	var text strings.Builder
	for i := 0; i < 50000; i++ {
		fmt.Fprintf(&text, "domain:idle-%05d.test\n", i)
	}
	var list struct {
		ID    int64 `json:"id"`
		Count int   `json:"entry_count"`
	}
	request("POST", "dest/lists", map[string]any{"name": "Owned fifty-thousand idle domains", "kind": "custom", "text": text.String()}, 201, &list)
	if list.ID <= 0 || list.Count != 50000 {
		t.Fatal("idle list did not publish fifty thousand actual domains")
	}
	input := destinationPolicyInput("Owned idle policy", "block")
	input["inline"], input["list_ids"] = map[string]any{}, []int64{list.ID}
	var policy destinationPolicyAPIResult
	request("POST", "dest/policies", input, 201, &policy)
	request("PUT", fmt.Sprintf("servers/%d", f.agent.PanelID), map[string]any{"audit_collect": "off"}, 200, nil)
	panel, err := a.repos.XUIPanel.GetByID(t.Context(), f.agent.PanelID)
	nativeMust(t, err, "bind actual disabled idle collection revision")
	prior, err := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	nativeMust(t, err, "bind actual idle policy before publication")
	after := time.Now().UTC()
	request("POST", "dest/publish", nil, 200, nil)
	m.Domains = list.Count
	nativeWait(t, 180*time.Second, "actual fifty-thousand-domain applied policy and complete streams", func() bool {
		state, e1 := a.destDefinitions.State(t.Context())
		current, e2 := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
		agent, e3 := a.repos.NodeAgent.GetByAgentID(t.Context(), f.agent.AgentID)
		if e1 != nil || e2 != nil || e3 != nil || current == nil || agent == nil || state.Paused || agent.ObservedCoreEngine != domain.NodeCoreXray || !nativeMutationReceiptMatches(prior, *current, state.PublishedGeneration, agent.LastSeen, after) || !nativeCollectionReceiptMatches(*current, "", uint64(panel.AuditCollectRevision)) {
			return false
		}
		var applied protocol.DestinationPolicy
		if json.Unmarshal(current.AppliedBody, &applied) != nil || len(applied.Rules) != 1 || applied.Rules[0].ID != fmt.Sprintf("p%d", policy.ID) || len(applied.Rules[0].Domains) != 50000 || applied.Rules[0].Action != protocol.RuleBlock {
			return false
		}
		for _, name := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster} {
			stream, err := a.repos.NodeAgent.GetStream(t.Context(), f.agent.AgentID, name)
			if err != nil || stream == nil || !stream.Converged() || stream.AppliedEpoch != agent.Epoch {
				return false
			}
		}
		m.Applied = len(applied.Rules[0].Domains)
		return true
	})
	live := nativeBindLiveCore(t, parent, dataDir, "xray")
	core := 0
	for _, pid := range nativeCoreChildren(parent) {
		if nativeCoreHasParent(pid, parent) && nativeOwnedLiveCoreCommand(nativeCoreArgs(pid), "xray", filepath.Join(dataDir, "runtime", "xray")) {
			if core != 0 {
				t.Fatal("idle cost has multiple owned cores")
			}
			core = pid
		}
	}
	if core == 0 {
		t.Fatal("idle cost lacks an owned core")
	}
	config, err := os.ReadFile(filepath.Join(dataDir, "runtime", "xray", "current.json"))
	nativeMust(t, err, "read actual idle core policy")
	var installed struct {
		Routing struct {
			Rules []struct {
				Domain []string `json:"domain"`
				Tag    string   `json:"outboundTag"`
			} `json:"rules"`
		} `json:"routing"`
	}
	nativeMust(t, json.Unmarshal(config, &installed), "decode actual idle core domain count")
	for _, rule := range installed.Routing.Rules {
		if rule.Tag == fmt.Sprintf("psp-deny-p%d", policy.ID) {
			m.CoreDomains += len(rule.Domain)
		}
	}
	if m.CoreDomains != 50000 {
		t.Fatal("running idle core lacks the full published domain policy")
	}
	control := func() {
		live()
		nativeMust(t, nativeVLESSHost(t.Context(), inbound, address, port, f.user.UUID, nonce, "idle-control.test"), "verify owned idle control traffic")
		m.Controls++
	}
	control()
	statePath := filepath.Join(dataDir, "state.db")
	stateFile, err := os.Stat(statePath)
	nativeMust(t, err, "bind owned idle SQLite file")
	readCPU := func(pid int) (uint64, uint64) {
		data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		nativeMust(t, err, "read owned idle CPU")
		cpu, birth, err := nativeLoadProcCounters(string(data))
		nativeMust(t, err, "decode owned idle CPU")
		return cpu, birth
	}
	_, agentBirth := readCPU(parent)
	_, coreBirth := readCPU(core)
	tracerPID := func() int {
		data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(parent), "status"))
		nativeMust(t, err, "read owned Node tracer identity")
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "TracerPid:") {
				pid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "TracerPid:")))
				nativeMust(t, err, "decode owned Node tracer identity")
				return pid
			}
		}
		t.Fatal("owned Node lacks tracer identity")
		return -1
	}
	var windows []nativeIdleWindow
	measure := func(phase string, expectedTracer int) {
		var start time.Time
		var agentCPU, coreCPU, accepted uint64
		observe := func(before bool) {
			live()
			if tracerPID() != expectedTracer {
				t.Fatal("idle CPU phase lost its exact tracer boundary")
			}
			file, err := os.Stat(statePath)
			nativeMust(t, err, "read owned idle SQLite file identity")
			if !os.SameFile(stateFile, file) {
				t.Fatal("idle phase replaced its SQLite state")
			}
			ac, ab := readCPU(parent)
			cc, cb := readCPU(core)
			if ab != agentBirth || cb != coreBirth {
				t.Fatal("idle CPU sample crossed a process replacement")
			}
			if before {
				start = time.Now()
				agentCPU, coreCPU, accepted = ac, cc, probe.accepted.Load()
				return
			}
			end := time.Now()
			if ac < agentCPU || cc < coreCPU || probe.accepted.Load()-accepted != 1 {
				t.Fatal("idle cost interval did not contain exactly one accepted sync")
			}
			cycle := nativeIdleCycle{Phase: phase, Index: len(m.Cycles)%5 + 1, Elapsed: end.Sub(start).Milliseconds(), AgentCPU: ac - agentCPU, CoreCPU: cc - coreCPU, Accepted: 1}
			m.Cycles = append(m.Cycles, cycle)
			if phase == "traced" {
				windows = append(windows, nativeIdleWindow{start.UnixMicro(), end.UnixMicro()})
			}
		}
		nativeAssertIdleSyncCycles(t, a, &probe.receipt, f.agent.AgentID, f.node.ID, parent, dataDir, "xray", func() {}, 5, observe)
	}
	measure("plain", 0)
	tracePath, windowPath := filepath.Join(t.TempDir(), "state-writes.private.trace"), filepath.Join(t.TempDir(), "state-writes.windows.json")
	nativeMust(t, os.WriteFile(tracePath, nil, 0600), "create owned private state trace")
	tracer := exec.Command("strace", "-f", "-ttt", "-yy", "-s", "0", "-e", "trace=write,pwrite64,writev,pwritev,pwritev2,fsync,fdatasync,mmap,msync", "-e", "signal=none", "-o", tracePath, "-p", strconv.Itoa(parent))
	tracer.Stdout, tracer.Stderr = &nativePrivateOutput{}, &nativePrivateOutput{}
	nativeMust(t, tracer.Start(), "attach owned idle state observer")
	done := make(chan error, 1)
	go func() { done <- tracer.Wait() }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = tracer.Process.Signal(os.Interrupt)
			select {
			case err := <-done:
				nativeMust(t, err, "detach owned idle state observer")
			case <-time.After(15 * time.Second):
				_ = tracer.Process.Kill()
				<-done
				t.Error("owned idle state observer failed to stop")
			}
		})
	}
	t.Cleanup(stop)
	nativeWait(t, 15*time.Second, "exact owned Node tracer attachment", func() bool { return tracerPID() == tracer.Process.Pid })
	m.TracerBound = 1
	measure("traced", tracer.Process.Pid)
	stop()
	if tracerPID() != 0 {
		t.Fatal("owned idle state observer did not detach")
	}
	live()
	data, err := json.Marshal(windows)
	nativeMust(t, err, "encode private idle trace intervals")
	nativeMust(t, os.WriteFile(windowPath, data, 0600), "save private idle trace intervals")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	reader := exec.CommandContext(ctx, "node", "deploy/destination-idle-state-trace.mjs", tracePath, statePath, windowPath)
	var privateErrors nativePrivateOutput
	reader.Stderr = &privateErrors
	result, err := reader.Output()
	nativeMust(t, err, "decode owned actual idle state write trace")
	var writes []nativeIdleWrites
	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&writes) != nil || decoder.Decode(new(any)) != io.EOF || len(writes) != 5 || len(m.Cycles) != 10 {
		t.Fatal("idle state byte measurements have an incomplete numeric shape")
	}
	for i := range writes {
		m.Cycles[i+5].Writes = &writes[i]
	}
	control()
}
