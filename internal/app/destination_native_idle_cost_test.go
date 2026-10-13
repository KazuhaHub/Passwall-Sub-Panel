//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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

// strace 6.8 detaches, flushes its log, then re-raises the interrupt it received.
// Only a successfully requested SIGINT may explain its signal/130 exit; an
// arbitrary nonzero status or a failed stop request remains an error.
func nativeIdleObserverStopError(signalErr, waitErr error) error {
	if signalErr != nil {
		return signalErr
	}
	if waitErr == nil {
		return nil
	}
	var exited *exec.ExitError
	if errors.As(waitErr, &exited) && exited.ProcessState != nil {
		if status, ok := exited.Sys().(syscall.WaitStatus); ok && (status.Signaled() && status.Signal() == syscall.SIGINT || status.Exited() && status.ExitStatus() == 128+int(syscall.SIGINT)) {
			return nil
		}
	}
	return waitErr
}

var nativeIdleObserverMessage = regexp.MustCompile(`^strace: Process [1-9][0-9]* (?:attached(?: with [1-9][0-9]* threads)?|detached)$`)

func nativeIdleObserverMessagesValid(output string) bool {
	if len(output) > 65536 {
		return false
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > 512 {
		return false
	}
	for _, line := range lines {
		if line != "" && !nativeIdleObserverMessage.MatchString(line) {
			return false
		}
	}
	return true
}

func TestNativeIdleObserverStopRequiresRequestedInterrupt(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		want          bool
	}{
		{"zero", "exit 0", true},
		{"interrupt", "kill -INT $$", true},
		{"interrupt-fallback", "exit 130", true},
		{"ordinary-failure", "exit 1", false},
		{"termination", "kill -TERM $$", false},
		{"killed", "kill -KILL $$", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := exec.Command("sh", "-c", tc.command).Run()
			if (nativeIdleObserverStopError(nil, err) == nil) != tc.want {
				t.Fatal("observer stop accepted an unexpected process exit")
			}
			if nativeIdleObserverStopError(errors.New("stop request failed"), err) == nil {
				t.Fatal("observer stop ignored a failed interrupt request")
			}
		})
	}
	for _, err := range []error{errors.New("unknown wait failure"), &exec.ExitError{}} {
		if nativeIdleObserverStopError(nil, err) == nil {
			t.Fatal("observer stop ignored an unknown wait failure")
		}
	}
	for _, output := range []string{"", "strace: Process 123 attached with 8 threads\nstrace: Process 124 detached\n", "strace: Process 123 attached\n"} {
		if !nativeIdleObserverMessagesValid(output) {
			t.Fatal("observer rejected its bounded attach/detach messages")
		}
	}
	for _, output := range []string{"strace: write: I/O error", "strace: Process 123 detached\nprivate payload", "strace: Process 0 detached", "strace: Process /private/state.db detached", strings.Repeat("strace: Process 123 detached\n", 513)} {
		if nativeIdleObserverMessagesValid(output) {
			t.Fatal("observer discarded an error or private output")
		}
	}
}

// This is a real idle runtime gate, not a generated-config size benchmark.
// CPU is measured first without ptrace, then in explicitly instrumented cycles.
// State bytes are successful syscalls to the exact owned DB/WAL/journal; they
// are not physical disk writes, file growth, or the agent's total process I/O.
func nativeIdleCost(t *testing.T, f destinationPolicyFixture, client *http.Client, endpoint, token string, parent int, dataDir string, inbound int, address string, port int, nonce, evidence string, ticks uint64, probe *nativeIdleCostProbe) {
	a := f.a
	m := struct {
		Schema        int               `json:"schema_version"`
		Candidate     string            `json:"candidate"`
		Core          string            `json:"core_version"`
		Domains       int               `json:"published_domains"`
		Applied       int               `json:"applied_domains"`
		CoreDomains   int               `json:"core_domains"`
		Ticks         uint64            `json:"cpu_ticks_per_second"`
		Controls      int               `json:"control_successes"`
		TracerBound   int               `json:"tracer_bound"`
		ReceiptChecks uint64            `json:"receipt_checks"`
		TraceStage    uint64            `json:"trace_stage"`
		TraceError    uint64            `json:"trace_error"`
		Cycles        []nativeIdleCycle `json:"cycles"`
	}{Schema: 2, Candidate: "184f4e23f0f3d72f8492432184685fea4ec4967c", Core: "26.6.27", Ticks: ticks, Cycles: []nativeIdleCycle{}}
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
	// Bootstrap sync samples its clock before compiling. Wait for its real
	// acknowledged candidate before editing so an overlapping first sync cannot
	// leave the mutation without a minted causal baseline. This warmup is outside
	// both measurement phases and does not alter the receipt predicate.
	nativeWait(t, 180*time.Second, "actual initial idle candidate and complete streams", func() bool {
		state, e1 := a.destDefinitions.State(t.Context())
		current, e2 := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
		agent, e3 := a.repos.NodeAgent.GetByAgentID(t.Context(), f.agent.AgentID)
		if e1 != nil || e2 != nil || e3 != nil || state.Paused || current == nil || current.MintedAt == nil || agent == nil || agent.Epoch != f.agent.Epoch || agent.ObservedCoreEngine != domain.NodeCoreXray || probe.accepted.Load() == 0 || !nativeReceiptMatches(*current, state.PublishedGeneration, agent.LastSeen, *current.MintedAt) {
			return false
		}
		for _, name := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster} {
			stream, err := a.repos.NodeAgent.GetStream(t.Context(), f.agent.AgentID, name)
			if err != nil || stream == nil || !stream.Converged() || stream.AppliedEpoch != agent.Epoch {
				return false
			}
		}
		return true
	})
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
	// Keep the actual policy edit's causal baseline. A following publication
	// may be a no-op over an already minted, not-yet-acknowledged candidate.
	prior, err := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	nativeMust(t, err, "bind actual idle policy before policy mutation")
	after := time.UnixMilli(time.Now().UnixMilli())
	var policy destinationPolicyAPIResult
	request("POST", "dest/policies", input, 201, &policy)
	request("PUT", fmt.Sprintf("servers/%d", f.agent.PanelID), map[string]any{"audit_collect": "off"}, 200, nil)
	panel, err := a.repos.XUIPanel.GetByID(t.Context(), f.agent.PanelID)
	nativeMust(t, err, "bind actual disabled idle collection revision")
	if panel == nil || panel.AuditCollect != domain.AuditCollectOff {
		t.Fatal("idle panel did not retain the requested disabled collection setting")
	}
	request("POST", "dest/publish", nil, 200, nil)
	m.Domains = list.Count
	nativeWait(t, 180*time.Second, "actual fifty-thousand-domain applied policy and complete streams", func() bool {
		m.ReceiptChecks = 0
		m.Applied = 0
		state, e1 := a.destDefinitions.State(t.Context())
		current, e2 := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
		agent, e3 := a.repos.NodeAgent.GetByAgentID(t.Context(), f.agent.AgentID)
		if e1 == nil && !state.Paused {
			m.ReceiptChecks |= 1
		}
		if e2 == nil && current != nil {
			m.ReceiptChecks |= 2
		}
		if e3 == nil && agent != nil {
			m.ReceiptChecks |= 4
		}
		if agent != nil && agent.ObservedCoreEngine == domain.NodeCoreXray {
			m.ReceiptChecks |= 8
		}
		if agent != nil && agent.Epoch == f.agent.Epoch {
			m.ReceiptChecks |= 16
		}
		if agent != nil && agent.DesiredCoreEngine == domain.NodeCoreXray && agent.DesiredCoreVersion == "26.6.27" {
			m.ReceiptChecks |= 32
		}
		if e1 == nil && current != nil && agent != nil && nativeMutationReceiptMatches(prior, *current, state.PublishedGeneration, agent.LastSeen, after) {
			m.ReceiptChecks |= 64
		}
		// The policy compiler omits collection revision when Collect is empty.
		if current != nil && nativeCollectionReceiptMatches(*current, "", 0) {
			m.ReceiptChecks |= 128
		}
		var applied protocol.DestinationPolicy
		if current != nil && json.Unmarshal(current.AppliedBody, &applied) == nil {
			m.ReceiptChecks |= 256
			if len(applied.Rules) == 1 && applied.Rules[0].ID == fmt.Sprintf("p%d", policy.ID) && applied.Rules[0].Action == protocol.RuleBlock {
				m.ReceiptChecks |= 512
			}
			for _, rule := range applied.Rules {
				if rule.ID == fmt.Sprintf("p%d", policy.ID) {
					m.Applied = len(rule.Domains)
				}
			}
			if m.Applied == 50000 {
				m.ReceiptChecks |= 1024
			}
		}
		for i, name := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster} {
			stream, err := a.repos.NodeAgent.GetStream(t.Context(), f.agent.AgentID, name)
			if err == nil && stream != nil && agent != nil && stream.Converged() && stream.AppliedEpoch == agent.Epoch {
				m.ReceiptChecks |= 2048 << i
			}
		}
		return m.ReceiptChecks == 8191
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
	var observerOutput, observerErrors nativePrivateOutput
	tracer.Stdout, tracer.Stderr = &observerOutput, &observerErrors
	nativeMust(t, tracer.Start(), "attach owned idle state observer")
	done := make(chan error, 1)
	go func() { done <- tracer.Wait() }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			signalErr := tracer.Process.Signal(os.Interrupt)
			select {
			case err := <-done:
				err = nativeIdleObserverStopError(signalErr, err)
				if err != nil {
					m.TraceError = 19
				}
				nativeMust(t, err, "detach owned idle state observer")
				if observerOutput.text() != "" || !nativeIdleObserverMessagesValid(observerErrors.text()) {
					m.TraceError = 21
					t.Fatal("owned idle state observer reported unexpected private output")
				}
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
	m.TraceStage = 1
	stop()
	m.TraceStage = 2
	if tracerPID() != 0 {
		m.TraceError = 20
		t.Fatal("owned idle state observer did not detach")
	}
	live()
	data, err := json.Marshal(windows)
	nativeMust(t, err, "encode private idle trace intervals")
	nativeMust(t, os.WriteFile(windowPath, data, 0600), "save private idle trace intervals")
	m.TraceStage = 3
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	reader := exec.CommandContext(ctx, "node", "deploy/destination-idle-state-trace.mjs", tracePath, statePath, windowPath)
	var privateErrors nativePrivateOutput
	reader.Stderr = &privateErrors
	result, err := reader.Output()
	if err != nil {
		m.TraceError = 17
		if errors.Is(err, exec.ErrNotFound) {
			m.TraceError = 16
		} else {
			// Only the reader's closed numeric diagnostic may leave stderr.
			// Arbitrary runtime errors, paths and private trace lines stay private.
			var diagnostic struct {
				Schema int    `json:"schema_version"`
				Code   uint64 `json:"error_code"`
			}
			d := json.NewDecoder(strings.NewReader(privateErrors.text()))
			d.DisallowUnknownFields()
			if d.Decode(&diagnostic) == nil && d.Decode(new(any)) == io.EOF && diagnostic.Schema == 1 && diagnostic.Code >= 1 && diagnostic.Code <= 15 {
				m.TraceError = diagnostic.Code
			}
		}
	}
	nativeMust(t, err, "decode owned actual idle state write trace")
	m.TraceStage = 4
	var writes []nativeIdleWrites
	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&writes) != nil || decoder.Decode(new(any)) != io.EOF || len(writes) != 5 || len(m.Cycles) != 10 {
		m.TraceError = 18
		t.Fatal("idle state byte measurements have an incomplete numeric shape")
	}
	for i := range writes {
		m.Cycles[i+5].Writes = &writes[i]
	}
	m.TraceStage = 5
	control()
	m.TraceStage = 6
}
