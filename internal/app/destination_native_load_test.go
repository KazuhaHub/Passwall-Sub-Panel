//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The separate load gate keeps every functional matrix case. Two independent
// five-minute phases exercise one webhook rule and fifty concurrently hit rules.
func TestNativeDestinationWebhookLoad(t *testing.T) {
	nativeDestinationLoad(t, nativeWebhookWorkload)
}

func TestNativeDestinationTrialLoad(t *testing.T) {
	nativeDestinationLoad(t, nativeTrialWorkload)
}

func TestNativeDestinationBlockOverflow(t *testing.T) {
	nativeDestinationLoad(t, nativeBlockOverflowWorkload)
}

func TestNativeDestinationMixedPressure(t *testing.T) {
	nativeDestinationLoad(t, nativeMixedWorkload)
}

func TestNativeDestinationIdleCost(t *testing.T) {
	nativeDestinationLoad(t, nativeIdleCostWorkload)
}

func nativeDestinationLoad(t *testing.T, workload nativeLoadWorkload) {
	trial, overflow := workload == nativeTrialWorkload, workload == nativeBlockOverflowWorkload
	mixed := workload == nativeMixedWorkload
	idle := workload == nativeIdleCostWorkload
	if workload > nativeIdleCostWorkload {
		t.Fatal("unknown native load workload")
	}
	binary, evidence := os.Getenv("PSP_NATIVE_NODE_BIN"), os.Getenv("PSP_NATIVE_LOAD_METRICS")
	metricsName := "destination-load-metrics.json"
	if trial {
		metricsName = "destination-trial-load-metrics.json"
	} else if overflow {
		metricsName = "destination-block-overflow-metrics.json"
	} else if mixed {
		metricsName = "destination-mixed-pressure-metrics.json"
	} else if idle {
		metricsName = "destination-idle-cost-metrics.json"
	}
	runnerTemp, tempErr := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	evidenceDir, dirErr := filepath.EvalSymlinks(filepath.Dir(evidence))
	database := os.Getenv("PSP_NATIVE_DB_KIND")
	validDatabase := database == "sqlite" || mixed && (database == "mysql" || database == "postgres")
	if tempErr != nil || dirErr != nil || runnerTemp != evidenceDir || filepath.Base(evidence) != metricsName || !filepath.IsAbs(binary) || filepath.Base(binary) != "destination-node" || !validDatabase || os.Getenv("PSP_NATIVE_CORE_VERSION") != "26.6.27" {
		t.Fatal("load fixture requires exact hosted candidate, owned evidence and supported owned database")
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		t.Fatal("load candidate is absent or mutable")
	}
	f := buildNativeDestinationPolicyFixture(t) // the existing disposable/root guards
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	nativeMust(t, err, "read load cadence")
	settings.NodePollSeconds, settings.FullReportSeconds = 2, 0
	if trial || mixed {
		settings.CronTrafficPullMinutes = 1
	}
	nativeMust(t, a.settings.Save(t.Context(), settings), "save load cadence")
	ownedPorts := map[int]bool{}
	port := func() int {
		for {
			value := nativePort(t)
			if !ownedPorts[value] {
				ownedPorts[value] = true
				return value
			}
		}
	}
	inbound, api := port(), port()
	f.node.DesiredPort, f.node.ObservedPort, f.node.InboundListen = inbound, inbound, "127.0.0.1"
	if trial || overflow || mixed || idle {
		f.node.Sniffing = `{"enabled":true,"destOverride":["http","tls","quic"],"routeOnly":true}`
	}
	nativeMust(t, a.repos.Node.Update(t.Context(), f.node), "configure owned load inbound")
	a.server.Addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port()))
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		nativeMust(t, a.Shutdown(ctx), "stop load production server")
		select {
		case err := <-runDone:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				t.Error("load production server failed")
			}
		case <-ctx.Done():
			t.Error("load production server did not stop")
		}
	})
	nativeWait(t, 30*time.Second, "complete load production server", func() bool {
		response, err := (&http.Client{Timeout: time.Second}).Get("http://" + a.server.Addr + "/health")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == 200
	})
	handler := a.server.Handler
	probe := newNativeMixedProbe(f.agent.AgentID)
	idleProbe := &nativeIdleCostProbe{}
	if trial || mixed {
		elapsed := metrics.NewHistogram("fixture_destination_sync_handler_ms", "Owned load control handler completion time; excludes network transit.", "ms", metrics.LatencyBucketsMS)
		original := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/node/sync" {
				started := time.Now()
				defer elapsed.ObserveSince(started)
			}
			original.ServeHTTP(w, r)
		})
		metrics.PollStageDuration.With("sink_flush")
		kinds := []string{"block", "trial"}
		if mixed {
			kinds = nativeMixedKinds[:]
		}
		for _, kind := range kinds {
			metrics.DestAuditBeginMS.With(kind)
			metrics.DestAuditChunkMS.With(kind)
		}
	}
	if mixed {
		handler = probe.handler(handler)
	} else if idle {
		handler = idleProbe.wrap(handler)
	}
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 15 * time.Second
	dir := t.TempDir()
	ca, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "credential")
	nativeMust(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600), "write load scoped CA")
	nativeMust(t, os.WriteFile(key, []byte(f.credential), 0600), "write load scoped credential")
	output := &nativePrivateOutput{}
	cmd := exec.Command(binary, "--endpoint", server.URL+"/v1/node/sync", "--agent-id", f.agent.AgentID, "--credential-file", key, "--data-dir", filepath.Join(dir, "node"), "--xray-api-listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(api)))
	cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+ca)
	cmd.Stdout, cmd.Stderr = output, output
	if mixed {
		cmd.Args = append(cmd.Args, "--audit-metrics")
		journal := &nativeMixedOutput{private: output, probe: probe}
		cmd.Stdout, cmd.Stderr = journal, journal
	}
	nativeMust(t, cmd.Start(), "start load candidate")
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			nativeMust(t, err, "drain load candidate")
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("load candidate did not drain")
		}
		for _, private := range []string{f.credential, f.user.Email, "destination@psp.local", " accepted ", " rejected "} {
			if strings.Contains(output.text(), private) {
				t.Error("load candidate exposed private data")
			}
		}
	})
	_, address, firstPort, nonce, reached := nativeTarget(t)
	_, controlAddress, controlPort, controlNonce, _ := nativeTargetAtPort(t, port())
	t.Cleanup(func() {
		for _, private := range []string{nonce, controlNonce, f.user.SubToken} {
			if strings.Contains(output.text(), private) {
				t.Error("load target or account data escaped into candidate output")
			}
		}
	})
	ports := []int{firstPort}
	for !mixed && !idle && len(ports) < 50 {
		listener, err := net.Listen("tcp6", net.JoinHostPort(address, "0"))
		nativeMust(t, err, "bind owned fifty-rule load target")
		target := &httptest.Server{Listener: listener, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached.Add(1); _, _ = io.WriteString(w, nonce) })}}
		target.Start()
		t.Cleanup(target.Close)
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}
	// These policies match ports across every address. A different target IP
	// alone cannot isolate the control request from the fifty block rules.
	if !mixed && !idle && !nativeLoadControlPortIsolated(ports, controlPort) {
		t.Fatal("load policy ports overlap or include the independent control port")
	}
	clock, err := exec.Command("getconf", "CLK_TCK").Output()
	nativeMust(t, err, "read actual process CPU clock")
	ticks, err := strconv.ParseUint(strings.TrimSpace(string(clock)), 10, 64)
	nativeMust(t, err, "decode actual process CPU clock")
	if idle {
		nativeIdleCost(t, f, client, server.URL, token, cmd.Process.Pid, filepath.Join(dir, "node"), inbound, address, firstPort, nonce, evidence, ticks, idleProbe)
		return
	}
	if mixed {
		nativeMixedLoad(t, f, client, server.URL, token, cmd.Process.Pid, filepath.Join(dir, "node"), inbound, address, firstPort, nonce, reached, controlAddress, controlPort, controlNonce, evidence, output.text, probe)
		return
	}
	if trial {
		nativeTrialLoad(t, f, client, server.URL, token, cmd.Process.Pid, filepath.Join(dir, "node"), inbound, api, ticks, address, firstPort, nonce, reached, controlAddress, controlPort, controlNonce, evidence, output.text)
		return
	}
	report := struct {
		Schema      int               `json:"schema_version"`
		Candidate   string            `json:"candidate"`
		CoreVersion string            `json:"core_version"`
		Ticks       uint64            `json:"cpu_ticks_per_second"`
		Phases      []nativeLoadPhase `json:"phases"`
	}{1, "1ce8f26cfbce8d3decf8855dda4349be10778a23", "26.6.27", ticks, []nativeLoadPhase{}}
	t.Cleanup(func() {
		data, err := json.Marshal(report)
		nativeMust(t, err, "encode count-only load evidence")
		nativeMust(t, os.WriteFile(evidence, append(data, '\n'), 0644), "save count-only owned load evidence")
	})
	caseTest := t
	var prior *domain.DestAgentPolicy
	var after time.Time
	request := func(method, path string, body any, want int, result any) {
		// Publication may leave the already minted candidate unchanged. Keep
		// the actual administrator edit as the causal before/after boundary,
		// as the functional native driver does, even if its receipt is pending.
		mutation := method != http.MethodGet && path != "dest/publish"
		if mutation {
			var err error
			prior, err = a.repos.DestAgentPolicy.Get(caseTest.Context(), f.agent.AgentID, true)
			nativeMust(caseTest, err, "bind load candidate before mutation")
		}
		data, err := json.Marshal(body)
		nativeMust(caseTest, err, "encode load administrator input")
		r, err := http.NewRequestWithContext(caseTest.Context(), method, server.URL+"/api/admin/"+path, bytes.NewReader(data))
		nativeMust(caseTest, err, "create load administrator request")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		if mutation {
			after = time.UnixMilli(time.Now().UnixMilli())
		}
		response, err := client.Do(r)
		nativeMust(caseTest, err, "call load production administrator route")
		defer response.Body.Close()
		if response.StatusCode != want {
			caseTest.Fatal("load administrator returned unexpected status; body withheld")
		}
		if result != nil {
			nativeMust(caseTest, json.NewDecoder(response.Body).Decode(result), "decode load administrator reply")
		} else {
			_, _ = io.Copy(io.Discard, response.Body)
		}
	}
	var oldIDs []int64
	readLosses := func(test *testing.T) map[string]nativeLoadLoss {
		rows, err := a.database.QueryContext(test.Context(), "SELECT reason, SUM(rows), SUM(events), SUM(unmatched) FROM dest_audit_loss_hourly WHERE panel_id=? AND kind='block' GROUP BY reason ORDER BY reason", f.agent.PanelID)
		nativeMust(test, err, "read actual categorized load losses")
		defer rows.Close()
		result := map[string]nativeLoadLoss{}
		for rows.Next() {
			var loss nativeLoadLoss
			nativeMust(test, rows.Scan(&loss.Reason, &loss.Rows, &loss.Events, &loss.Unmatched), "read bounded load loss counters")
			result[loss.Reason] = loss
		}
		nativeMust(test, rows.Err(), "finish bounded load loss counters")
		return result
	}
	phaseNames := []string{"single_rule", "fifty_rules"}
	if overflow {
		phaseNames = []string{"block_overflow"}
	}
	for index, name := range phaseNames {
		if !t.Run(name, func(test *testing.T) {
			caseTest = test
			phase := nativeLoadPhase{Name: name, Rules: []int{1, 50}[index], Losses: []nativeLoadLoss{}, Samples: []nativeLoadSample{}}
			defer func() { report.Phases = append(report.Phases, phase) }()
			for _, id := range oldIDs {
				request("DELETE", fmt.Sprintf("dest/policies/%d", id), nil, 204, nil)
			}
			oldIDs = nil
			for i := 0; i < phase.Rules; i++ {
				input := destinationPolicyInput(fmt.Sprintf("Load rule %d", i), "block")
				input["inline"] = map[string]any{"ports": strconv.Itoa(ports[i])}
				var result destinationPolicyAPIResult
				request("POST", "dest/policies", input, 201, &result)
				oldIDs = append(oldIDs, result.ID)
			}
			request("PUT", fmt.Sprintf("servers/%d", f.agent.PanelID), map[string]any{"audit_collect": "hits"}, 200, nil)
			request("POST", "dest/publish", nil, 200, nil)
			panel, err := a.repos.XUIPanel.GetByID(test.Context(), f.agent.PanelID)
			nativeMust(test, err, "read load collection revision")
			factNames := []string{"load_state", "load_runtime", "load_agent", "load_panel", "load_selection", "load_observed_core", "load_transition", "load_collection", "load_rule_count", "load_rule_shape", "load_config", "load_roster"}
			facts := make(map[string]bool)
			defer func() {
				if test.Failed() {
					for _, name := range factNames {
						test.Logf("native diagnostic: %s=%t", name, facts[name])
					}
				}
			}()
			nativeWait(test, 120*time.Second, "exact load policy and collection receipt", func() bool {
				for _, name := range factNames {
					facts[name] = false
				}
				state, e1 := a.destDefinitions.State(test.Context())
				runtime, e2 := a.repos.DestAgentPolicy.Get(test.Context(), f.agent.AgentID, true)
				agent, e3 := a.repos.NodeAgent.GetByAgentID(test.Context(), f.agent.AgentID)
				observedPanel, e4 := a.repos.XUIPanel.GetByID(test.Context(), f.agent.PanelID)
				facts["load_state"] = e1 == nil && !state.Paused
				facts["load_runtime"], facts["load_agent"], facts["load_panel"] = e2 == nil && runtime != nil, e3 == nil && agent != nil, e4 == nil && observedPanel != nil
				if agent != nil {
					facts["load_selection"] = agent.DesiredCoreEngine == domain.NodeCoreXray && agent.DesiredCoreVersion == "26.6.27" && !agent.AllowRestrictedReality && agent.Epoch == f.agent.Epoch
				}
				facts["load_observed_core"] = agent != nil && observedPanel != nil && agent.ObservedCoreEngine == domain.NodeCoreXray && observedPanel.XrayVersion == "26.6.27"
				if runtime != nil && agent != nil && e1 == nil {
					facts["load_transition"] = nativeMutationReceiptMatches(prior, *runtime, state.PublishedGeneration, agent.LastSeen, after)
					facts["load_collection"] = nativeCollectionReceiptMatches(*runtime, protocol.CollectHits, uint64(panel.AuditCollectRevision))
				}
				var body protocol.DestinationPolicy
				facts["load_rule_count"] = runtime != nil && json.Unmarshal(runtime.MintedBody, &body) == nil && len(body.Rules) == phase.Rules
				if facts["load_rule_count"] {
					facts["load_rule_shape"] = true
					for i, rule := range body.Rules {
						if rule.ID != fmt.Sprintf("p%d", oldIDs[i]) || rule.Action != protocol.RuleBlock || rule.Ports != strconv.Itoa(ports[i]) || len(rule.Domains) != 0 || len(rule.Subjects) != 0 {
							facts["load_rule_shape"] = false
						}
					}
				}
				for _, streamName := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster} {
					stream, err := a.repos.NodeAgent.GetStream(test.Context(), f.agent.AgentID, streamName)
					facts["load_"+string(streamName)] = err == nil && stream != nil && agent != nil && stream.Converged() && stream.AppliedEpoch == agent.Epoch
				}
				for _, name := range factNames {
					if !facts[name] {
						return false
					}
				}
				return true
			})
			live := nativeBindLiveCore(test, cmd.Process.Pid, filepath.Join(dir, "node"), "xray")
			corePID, coreBinary := 0, ""
			for _, pid := range nativeCoreChildren(cmd.Process.Pid) {
				args := nativeCoreArgs(pid)
				if nativeCoreHasParent(pid, cmd.Process.Pid) && nativeOwnedLiveCoreCommand(args, "xray", filepath.Join(dir, "node", "runtime", "xray")) {
					if corePID != 0 {
						test.Fatal("load has more than one owned core")
					}
					corePID, coreBinary = pid, args[0]
				}
			}
			if corePID == 0 {
				test.Fatal("load cannot find the original owned core")
			}
			sample := nativeLoadSampler(test, cmd.Process.Pid, corePID, coreBinary, api, live)
			baseline := sample()
			priorLosses := readLosses(test)
			start := time.Now()
			baseline.ElapsedMS = 0
			phase.Samples = append(phase.Samples, baseline)
			var attempted, delivered, blocked, failed, late atomic.Uint64
			windows := make([]atomic.Uint64, 300)
			jobs := make(chan int, 512)
			var workers sync.WaitGroup
			headers := make([][]byte, phase.Rules)
			for i := range headers {
				headers[i], err = nativeVLESSRequest(f.user.UUID, address, ports[i], "load.example.test")
				nativeMust(test, err, "encode owned load request")
			}
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			var closeWorkers sync.Once
			defer func() { cancel(); closeWorkers.Do(func() { close(jobs) }); workers.Wait() }()
			for range 512 {
				workers.Add(1)
				go func() {
					defer workers.Done()
					for job := range jobs {
						attempted.Add(1)
						var header []byte
						if overflow {
							var err error
							header, err = nativeVLESSRequest(f.user.UUID, address, ports[0], fmt.Sprintf("overflow%07d.test", job))
							if err != nil {
								failed.Add(1)
								continue
							}
						} else {
							header = headers[job]
						}
						ok := nativeLoadBlockedRequest(ctx, inbound, header, func() {
							delivered.Add(1)
							bucket := int(time.Since(start) / time.Second)
							if bucket < 300 {
								windows[bucket].Add(1)
							} else {
								late.Add(1)
							}
						})
						if ok {
							blocked.Add(1)
						} else {
							failed.Add(1)
						}
					}
				}()
			}
			// The load generator uses a bounded open-loop schedule. Evidence is
			// actual successful writes per second, never the scheduled target.
			var seen time.Time
			for tick := 0; tick < 3000; tick++ {
				due := start.Add(time.Duration(tick) * 100 * time.Millisecond)
				if delay := time.Until(due); delay > 0 {
					time.Sleep(delay)
				}
				for n := 0; n < 110; n++ {
					job := tick*110 + n
					if !overflow {
						job %= phase.Rules
					}
					select {
					case jobs <- job:
					default:
						failed.Add(1)
					}
				}
				if tick%10 == 0 {
					live()
					controlCtx, stop := context.WithTimeout(test.Context(), 5*time.Second)
					controlErr := nativeVLESSHost(controlCtx, inbound, controlAddress, controlPort, f.user.UUID, controlNonce, "load-control.example.test")
					stop()
					if controlErr != nil {
						failed.Add(1)
					} else {
						phase.ControlSuccesses++
					}
					agent, err := a.repos.NodeAgent.GetByAgentID(test.Context(), f.agent.AgentID)
					nativeMust(test, err, "read load control heartbeat")
					if agent.LastSeen != nil && agent.LastSeen.After(seen) {
						phase.HeartbeatAdvances++
						seen = *agent.LastSeen
					}
				}
				if tick > 0 && tick%100 == 0 {
					point := sample()
					point.ElapsedMS = time.Since(start).Milliseconds()
					phase.Samples = append(phase.Samples, point)
				}
			}
			if delay := time.Until(start.Add(300 * time.Second)); delay > 0 {
				time.Sleep(delay)
			}
			phase.DurationMS = time.Since(start).Milliseconds()
			closeWorkers.Do(func() { close(jobs) })
			workers.Wait()
			phase.Attempted, phase.Delivered, phase.Blocked, phase.TransportFailures, phase.LateDelivered = attempted.Load(), delivered.Load(), blocked.Load(), failed.Load(), late.Load()
			phase.TargetRequests = reached.Load()
			phase.WindowDelivered = make([]uint64, len(windows))
			for i := range windows {
				phase.WindowDelivered[i] = windows[i].Load()
			}
			point := sample()
			point.ElapsedMS = time.Since(start).Milliseconds()
			phase.Samples = append(phase.Samples, point)
			for range 3 {
				time.Sleep(10 * time.Second)
				point := sample()
				point.ElapsedMS = time.Since(start).Milliseconds()
				phase.Samples = append(phase.Samples, point)
			}
			// Read persisted counts after the actual worker has caught up. The
			// source IDs are owned fixture identities, never exported in metrics.
			marks, args := make([]string, len(oldIDs)), []any{f.agent.PanelID, f.user.ID}
			for i, id := range oldIDs {
				marks[i] = "?"
				args = append(args, fmt.Sprintf("p%d", id))
			}
			query := "SELECT COUNT(DISTINCT source), COALESCE(SUM(count),0) FROM dest_hits WHERE panel_id=? AND user_id=? AND action='block' AND source IN (" + strings.Join(marks, ",") + ")"
			nativeWait(test, 90*time.Second, "actual load ingestion and explained event totals", func() bool {
				nativeMust(test, a.database.QueryRowContext(test.Context(), query, args...).Scan(&phase.HitRules, &phase.HitEvents), "read actual load hit totals")
				phase.Losses = []nativeLoadLoss{}
				accounted := phase.HitEvents
				for _, loss := range readLosses(test) {
					before := priorLosses[loss.Reason]
					loss.Rows -= before.Rows
					loss.Events -= before.Events
					loss.Unmatched -= before.Unmatched
					if loss.Rows < 0 || loss.Events < 0 || loss.Unmatched < 0 {
						test.Fatal("load loss counters regressed")
					}
					if loss.Rows+loss.Events+loss.Unmatched > 0 {
						phase.Losses = append(phase.Losses, loss)
					}
					accounted += loss.Events + loss.Unmatched
					// Only the overflow fixture uses one unique host/event per
					// connection, making a discarded row one known event.
					if overflow {
						accounted += loss.Rows
					}
				}
				return phase.HitRules == phase.Rules && accounted >= 0 && uint64(accounted) == phase.Blocked
			})
			if overflow {
				nativeMust(test, a.database.QueryRowContext(test.Context(), "SELECT COALESCE(MAX(rows_reserved),0) FROM dest_audit_ingest_budget WHERE agent_id=? AND kind='block'", f.agent.AgentID).Scan(&phase.BudgetMaxRows), "read actual exhausted block reservation")
				var discarded int64
				for _, loss := range phase.Losses {
					if loss.Reason == "over_budget" {
						discarded += loss.Rows
					}
				}
				if phase.BudgetMaxRows != 20000 || discarded <= 0 {
					test.Fatal("block overflow never exhausted its independent actual budget")
				}
			}
			if phase.TransportFailures != 0 || phase.TargetRequests != 0 || phase.Attempted != phase.Delivered || phase.Blocked != phase.Delivered || phase.LateDelivered != 0 || phase.ControlSuccesses < 300 || phase.HeartbeatAdvances < 30 || phase.HitRules != phase.Rules {
				test.Fatal("sustained load, own receipt, target or control proof failed")
			}
			for _, count := range phase.WindowDelivered {
				if count < 1000 {
					test.Fatal("actual load delivery fell below 1000 per second")
				}
			}
		}) {
			return
		}
	}
}

func nativeLoadBlockedRequest(ctx context.Context, inbound int, header []byte, sent func()) bool {
	return nativeLoadBlockedRequestFailure(ctx, inbound, header, sent) == nil
}

func nativeLoadBlockedRequestFailure(ctx context.Context, inbound int, header []byte, sent func()) error {
	connection, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(inbound)))
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	n, err := connection.Write(header)
	if err != nil {
		return err
	}
	if n != len(header) {
		return io.ErrShortWrite
	}
	sent()
	data, err := io.ReadAll(io.LimitReader(connection, 4))
	if err != nil && !errors.Is(err, syscall.ECONNRESET) {
		return err
	}
	if len(data) == 0 || len(data) == 2 && data[0] == 0 && data[1] == 0 {
		return nil
	}
	return errors.New("unexpected blocked response")
}

func nativeLoadSampler(t *testing.T, agent, core int, binary string, api int, live func()) func() nativeLoadSample {
	readCPU := func(pid int) (uint64, uint64) {
		data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		nativeMust(t, err, "read owned load process counters")
		cpu, birth, err := nativeLoadProcCounters(string(data))
		nativeMust(t, err, "decode owned load process counters")
		return cpu, birth
	}
	_, agentBirth := readCPU(agent)
	_, coreBirth := readCPU(core)
	return func() nativeLoadSample {
		live()
		agentCPU, currentAgent := readCPU(agent)
		coreCPU, currentCore := readCPU(core)
		if currentAgent != agentBirth || currentCore != coreBirth {
			t.Fatal("load resource sampler lost original process identity")
		}
		fds, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(core), "fd"))
		nativeMust(t, err, "count owned core descriptors")
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		data, err := exec.CommandContext(ctx, binary, "api", "statssys", "--server="+net.JoinHostPort("127.0.0.1", strconv.Itoa(api))).Output()
		nativeMust(t, err, "read actual Xray runtime statistics")
		var stats struct {
			Goroutines uint64 `json:"numGoroutine"`
		}
		nativeMust(t, json.Unmarshal(data, &stats), "decode bounded Xray goroutine count")
		if stats.Goroutines == 0 {
			t.Fatal("load resource sampler has no actual goroutine count")
		}
		live()
		return nativeLoadSample{CoreGoroutines: stats.Goroutines, CoreFDs: uint64(len(fds)), AgentCPUTicks: agentCPU, CoreCPUTicks: coreCPU}
	}
}
