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

type nativeTrialMetrics struct {
	Schema          int                `json:"schema_version"`
	Candidate       string             `json:"candidate"`
	CoreVersion     string             `json:"core_version"`
	Users           int                `json:"users"`
	Duration        int64              `json:"duration_ms"`
	TrialWindows    []uint64           `json:"trial_windows"`
	UserDeliveries  []uint64           `json:"user_deliveries"`
	TrialDelivered  uint64             `json:"trial_delivered"`
	TrialAllowed    uint64             `json:"trial_allowed"`
	TrialTarget     int64              `json:"trial_target_requests"`
	TrialRows       int64              `json:"trial_stored_rows"`
	TrialEvents     int64              `json:"trial_stored_events"`
	TrialOverBudget int64              `json:"trial_over_budget_rows"`
	TrialIdentified int64              `json:"trial_identified_rows"`
	TrialBudget     int64              `json:"trial_budget_max_rows"`
	TrialLosses     []nativeLoadLoss   `json:"trial_losses"`
	Samples         []nativeLoadSample `json:"samples"`
	CPUTicks        uint64             `json:"cpu_ticks_per_second"`
	BlockWindows    []uint64           `json:"block_windows"`
	BlockDelivered  uint64             `json:"block_delivered"`
	BlockDenied     uint64             `json:"block_denied"`
	BlockEvents     int64              `json:"block_stored_events"`
	BlockLossRows   int64              `json:"block_loss_rows"`
	BlockLossEvents int64              `json:"block_loss_events"`
	Failures        uint64             `json:"transport_failures"`
	Late            uint64             `json:"late_delivered"`
	Controls        uint64             `json:"control_successes"`
	Heartbeats      uint64             `json:"heartbeat_advances"`
}

// Distinct virtual HTTP hosts are sniffed only for routing and audit identity;
// routeOnly leaves the real destination on the owned documentation-address
// nonce server. No benchmark request resolves or connects to those hostnames.
func nativeTrialLoad(t *testing.T, f destinationPolicyFixture, client *http.Client, endpoint, token string, parent int, dataDir string, inbound, api int, ticks uint64, address string, targetPort int, nonce string, reached *atomic.Int64, controlAddress string, controlPort int, controlNonce, evidence string, output func() string) {
	a := f.a
	stage := "capability"
	defer func() {
		if t.Failed() {
			t.Logf("native diagnostic: trial_setup_%s=false", stage)
		}
	}()
	m := nativeTrialMetrics{Schema: 1, Candidate: "0614879497263defbda2bbcad6cdc81b3cdfd483", CoreVersion: "26.6.27", TrialWindows: make([]uint64, 300), BlockWindows: make([]uint64, 300), UserDeliveries: make([]uint64, 200)}
	t.Cleanup(func() {
		data, err := json.Marshal(m)
		nativeMust(t, err, "encode bounded trial load evidence")
		nativeMust(t, os.WriteFile(evidence, append(data, '\n'), 0644), "write owned trial load evidence")
	})
	users, group, policy, blockAddress, blockPort, blockReached := nativePrepareTrialLoad(t, f, client, endpoint, token, inbound, output, &stage, nil)
	m.Users = len(users)
	stage = "resources"
	live := nativeBindLiveCore(t, parent, dataDir, "xray")
	corePID, coreBinary := 0, ""
	for _, pid := range nativeCoreChildren(parent) {
		args := nativeCoreArgs(pid)
		if nativeCoreHasParent(pid, parent) && nativeOwnedLiveCoreCommand(args, "xray", filepath.Join(dataDir, "runtime", "xray")) {
			if corePID != 0 {
				t.Fatal("trial load has multiple owned cores")
			}
			corePID, coreBinary = pid, args[0]
		}
	}
	if corePID == 0 {
		t.Fatal("trial load has no original owned core")
	}
	sample := nativeLoadSampler(t, parent, corePID, coreBinary, api, live)
	m.CPUTicks = ticks
	// Prove routeOnly before launching the flood. A virtual name must reach
	// the owned nonce through the same user/listener and never public DNS.
	// The open principal keeps warmup out of measured trial audit rows.
	stage = "warmup"
	nativeMust(t, nativeVLESSHost(t.Context(), inbound, address, targetPort, f.user.UUID, nonce, "trial-warmup.test"), "verify owned route-only trial target")
	stage = "traffic"
	baselineRequests := reached.Load()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type job struct {
		seq, user int
		block     bool
	}
	jobs := make(chan job, 512)
	var wg sync.WaitGroup
	var closeJobs sync.Once
	defer func() { cancel(); closeJobs.Do(func() { close(jobs) }); wg.Wait() }()
	trialWindows, blockWindows := make([]atomic.Uint64, 300), make([]atomic.Uint64, 300)
	perUser := make([]atomic.Uint64, 200)
	var allowed, trialDelivered, blockDelivered, denied, failures, late atomic.Uint64
	blockHeader, err := nativeVLESSRequest(f.user.UUID, blockAddress, blockPort, "trial-block.example.test")
	nativeMust(t, err, "encode independent block request")
	baseline := sample()
	latencyBefore := nativeTrialLatencySnapshot(t)
	pageState, err := a.destDefinitions.State(t.Context())
	nativeMust(t, err, "bind published generation for first-screen reads")
	start := time.Now()
	pageDone := nativeStartPageProbe(ctx, client, endpoint, token, nativePageIdentity{panel: f.agent.PanelID, user: f.user.ID, policy: policy.ID, group: group.ID, generation: pageState.PublishedGeneration}, start)
	baseline.ElapsedMS = 0
	m.Samples = append(m.Samples, baseline)
	for range 512 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if j.block {
					ok := nativeLoadBlockedRequest(ctx, inbound, blockHeader, func() {
						blockDelivered.Add(1)
						bucket := int(time.Since(start) / time.Second)
						if bucket < 300 {
							blockWindows[bucket].Add(1)
						} else {
							late.Add(1)
						}
					})
					if ok {
						denied.Add(1)
					} else {
						failures.Add(1)
					}
				} else {
					err := nativeVLESSHost(ctx, inbound, address, targetPort, users[j.user].UUID, nonce, fmt.Sprintf("trial%07d.test", j.seq))
					if err != nil {
						failures.Add(1)
						continue
					}
					allowed.Add(1)
					trialDelivered.Add(1)
					perUser[j.user].Add(1)
					bucket := int(time.Since(start) / time.Second)
					if bucket < 300 {
						trialWindows[bucket].Add(1)
					} else {
						late.Add(1)
					}
				}
			}
		}()
	}
	var lastSeen time.Time
	for tick := 0; tick < 3000; tick++ {
		if delay := time.Until(start.Add(time.Duration(tick) * 100 * time.Millisecond)); delay > 0 {
			time.Sleep(delay)
		}
		for n := 0; n < 110; n++ {
			seq := tick*110 + n
			select {
			case jobs <- job{seq: seq, user: seq % 200}:
			default:
				failures.Add(1)
			}
		}
		for n := 0; n < 11; n++ {
			select {
			case jobs <- job{block: true}:
			default:
				failures.Add(1)
			}
		}
		if tick%10 == 0 {
			live()
			nativeMust(t, nativeVLESSHost(t.Context(), inbound, controlAddress, controlPort, f.user.UUID, controlNonce, "trial-control.example.test"), "trial flood independent control connection")
			m.Controls++
			agent, err := a.repos.NodeAgent.GetByAgentID(t.Context(), f.agent.AgentID)
			nativeMust(t, err, "read actual trial control heartbeat")
			if agent.LastSeen != nil && agent.LastSeen.After(lastSeen) {
				m.Heartbeats++
				lastSeen = *agent.LastSeen
			}
		}
		if tick > 0 && tick%100 == 0 {
			point := sample()
			point.ElapsedMS = time.Since(start).Milliseconds()
			m.Samples = append(m.Samples, point)
		}
	}
	if delay := time.Until(start.Add(300 * time.Second)); delay > 0 {
		time.Sleep(delay)
	}
	m.Duration = time.Since(start).Milliseconds()
	pageFailure := nativeWritePageProbe(t, evidence, m.Duration, pageDone)
	nativeWriteTrialLatency(t, evidence, m.Duration, latencyBefore)
	closeJobs.Do(func() { close(jobs) })
	wg.Wait()
	live()
	point := sample()
	point.ElapsedMS = time.Since(start).Milliseconds()
	m.Samples = append(m.Samples, point)
	for range 3 {
		time.Sleep(10 * time.Second)
		point := sample()
		point.ElapsedMS = time.Since(start).Milliseconds()
		m.Samples = append(m.Samples, point)
	}
	m.TrialDelivered, m.TrialAllowed, m.BlockDelivered, m.BlockDenied, m.Failures, m.Late = trialDelivered.Load(), allowed.Load(), blockDelivered.Load(), denied.Load(), failures.Load(), late.Load()
	m.TrialTarget = reached.Load() - baselineRequests
	for i := range trialWindows {
		m.TrialWindows[i] = trialWindows[i].Load()
		m.BlockWindows[i] = blockWindows[i].Load()
	}
	for i := range perUser {
		m.UserDeliveries[i] = perUser[i].Load()
	}
	if blockReached.Load() != 0 {
		t.Fatal("trial concurrency bypassed independent block policy")
	}
	// No account identifier or virtual host is exported. This query checks the
	// persisted anonymous group source and actual per-kind budget/loss totals.
	stage = "ingestion"
	nativeWait(t, 90*time.Second, "actual trial saturation with intact block ingestion", func() bool {
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COUNT(*), COALESCE(SUM(count),0), COALESCE(SUM(CASE WHEN user_id<>0 OR port<>0 THEN 1 ELSE 0 END),0) FROM dest_hits WHERE panel_id=? AND source=? AND action='observe'", f.agent.PanelID, fmt.Sprintf("g%d", group.ID)).Scan(&m.TrialRows, &m.TrialEvents, &m.TrialIdentified), "read anonymous real trial ingestion")
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(rows),0) FROM dest_audit_loss_hourly WHERE panel_id=? AND kind='trial' AND reason='over_budget'", f.agent.PanelID).Scan(&m.TrialOverBudget), "read actual exhausted trial quota")
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COALESCE(MAX(rows_reserved),0) FROM dest_audit_ingest_budget WHERE agent_id=? AND kind='trial'", f.agent.AgentID).Scan(&m.TrialBudget), "read actual per-hour trial reservation")
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(count),0) FROM dest_hits WHERE panel_id=? AND source=? AND user_id=? AND action='block'", f.agent.PanelID, fmt.Sprintf("p%d", policy.ID), f.user.ID).Scan(&m.BlockEvents), "read independently retained block events")
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(rows),0),COALESCE(SUM(events+unmatched),0) FROM dest_audit_loss_hourly WHERE panel_id=? AND kind='block'", f.agent.PanelID).Scan(&m.BlockLossRows, &m.BlockLossEvents), "read block-only loss accounting")
		rows, err := a.database.QueryContext(t.Context(), "SELECT reason, SUM(rows), SUM(events), SUM(unmatched) FROM dest_audit_loss_hourly WHERE panel_id=? AND kind='trial' GROUP BY reason ORDER BY reason", f.agent.PanelID)
		nativeMust(t, err, "read categorized trial-only loss counters")
		defer rows.Close()
		m.TrialLosses = make([]nativeLoadLoss, 0)
		accounted := m.TrialEvents
		for rows.Next() {
			var loss nativeLoadLoss
			nativeMust(t, rows.Scan(&loss.Reason, &loss.Rows, &loss.Events, &loss.Unmatched), "decode bounded trial-only loss counters")
			m.TrialLosses = append(m.TrialLosses, loss)
			// Each virtual site is unique with count=1, so discarded rows
			// account for one event only in this specific workload.
			accounted += loss.Rows + loss.Events + loss.Unmatched
		}
		nativeMust(t, rows.Err(), "finish trial-only loss counter read")
		return m.TrialRows > 0 && m.TrialRows == m.TrialEvents && m.TrialOverBudget > 0 && uint64(accounted) == m.TrialDelivered && m.BlockEvents >= 0 && uint64(m.BlockEvents) == m.BlockDenied
	})
	if m.Failures != 0 || m.Late != 0 || m.TrialAllowed != m.TrialDelivered || m.TrialTarget != int64(m.TrialDelivered) || m.BlockDenied != m.BlockDelivered || m.BlockLossRows != 0 || m.BlockLossEvents != 0 || m.TrialIdentified != 0 || m.TrialBudget > 20000 {
		t.Fatal("trial flood lost sustained delivery, anonymity, control or independent block reservation")
	}
	for i := range m.TrialWindows {
		if m.TrialWindows[i] < 1000 || m.BlockWindows[i] < 100 {
			t.Fatal("trial or independent block flow fell below its actual rate")
		}
	}
	for _, count := range m.UserDeliveries {
		if count == 0 {
			t.Fatal("trial load omitted an actual user")
		}
	}
	// The accepted workload has a separate future block-overflow and four-lane
	// pressure gate. This phase proves trial budget exhaustion and block isolation.
	if pageFailure != "" {
		t.Fatalf("first-screen API probe failed: %s; private body withheld", pageFailure)
	}
}

// nativePrepareTrialLoad uses the production membership retry service and
// exact applied policy/config/roster receipts for both independent workloads.
func nativePrepareTrialLoad(t *testing.T, f destinationPolicyFixture, client *http.Client, endpoint, token string, inbound int, output func() string, stage *string, mixed *nativeMixedSetup) ([]*domain.User, *domain.Group, destinationPolicyAPIResult, string, int, *atomic.Int64) {
	a := f.a
	lastStage := ""
	setStage := func(next string) {
		*stage = next
		if next != lastStage {
			t.Log("native setup stage: " + next)
			lastStage = next
		}
	}
	setStage("capability")
	collect, ruleCount := "hits", 2
	if mixed != nil {
		collect, ruleCount = "hits_and_usage", 3
	}
	var prior *domain.DestAgentPolicy
	var after time.Time
	request := func(method, path string, body any, want int, result any) {
		if method != "GET" {
			var err error
			prior, err = a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
			nativeMust(t, err, "bind trial load candidate before mutation")
		}
		data, err := json.Marshal(body)
		nativeMust(t, err, "encode trial administrator input")
		r, err := http.NewRequestWithContext(t.Context(), method, endpoint+"/api/admin/"+path, bytes.NewReader(data))
		nativeMust(t, err, "create trial administrator request")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		if method != "GET" {
			after = time.Now()
		}
		response, err := client.Do(r)
		nativeMust(t, err, "call trial production administrator route")
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatal("trial load administrator failed; private body withheld")
		}
		if result != nil {
			nativeMust(t, json.NewDecoder(response.Body).Decode(result), "decode trial administrator reply")
		} else {
			_, _ = io.Copy(io.Discard, response.Body)
		}
	}
	// Do not borrow a fabricated capability or roster to provision the users.
	nativeWait(t, 120*time.Second, "actual native trial provisioning capability", func() bool {
		agent, err := a.repos.NodeAgent.GetByAgentID(t.Context(), f.agent.AgentID)
		if err != nil || agent == nil || agent.LastSeen == nil || !slicesContainsNative(agent.ObservedCapabilities, protocol.CapabilityDestinationPolicy) {
			return false
		}
		// The first control report may advertise capability before installation
		// or any full runtime enumeration. Membership reconciliation reads the
		// real native adapter, so wait for its observed inbound rather than
		// treating advertised support as a completed startup.
		cli, err := a.xuiPool.Get(f.agent.PanelID)
		if err != nil {
			return false
		}
		status, err := cli.GetServerStatus(t.Context())
		if err != nil {
			return false
		}
		observedInbound, err := cli.GetInbound(t.Context(), f.node.InboundID)
		return err == nil && nativeTrialProvisioningReady(status, observedInbound, f.node.InboundID, inbound)
	})
	setStage("group")
	group := &domain.Group{Slug: "trial-load", Name: "Trial load", TagFilter: domain.TagFilter{All: true}}
	nativeMust(t, a.repos.Group.Create(t.Context(), group), "create owned trial load group")
	users := make([]*domain.User, 200)
	t.Cleanup(func() {
		for _, user := range users {
			if user != nil && (strings.Contains(output(), user.Email) || strings.Contains(output(), user.UUID) || strings.Contains(output(), user.SubToken)) {
				t.Error("trial principal escaped into private candidate output")
			}
		}
	})
	memberConcurrency := 1
	if mixed != nil && mixed.memberConcurrency != 0 {
		memberConcurrency = mixed.memberConcurrency
	}
	if memberConcurrency < 1 || memberConcurrency > 8 {
		t.Fatal("trial fixture membership concurrency outside the bounded owner workload")
	}
	for i := range users {
		setStage("principal")
		email := fmt.Sprintf("trial-load-%03d@example.test", i)
		users[i] = &domain.User{UPN: email, Email: email, SSOProvider: domain.SSOProviderLocal, SSOSubject: email, Role: domain.RoleUser, Enabled: true, GroupID: group.ID, UUID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1), SubToken: fmt.Sprintf("fixture-trial-load-sub-%03d", i)}
		nativeMust(t, a.repos.User.Create(t.Context(), users[i]), "create real trial principal")
		if memberConcurrency == 1 {
			setStage("membership")
			nativeMust(t, a.user.ResyncMembershipOrEnqueue(t.Context(), users[i].ID, "trial load membership"), "admit trial principal through production membership retry service")
			if mixed != nil && mixed.memberReady != nil {
				mixed.memberReady(i + 1)
			}
		}
	}
	setStage("membership")
	// Different users have independent production resync locks. Bound the
	// fixture's concurrency without bypassing intent, lifecycle, provisioning,
	// durable retries, or the exact applied roster/traffic checks below.
	if memberConcurrency > 1 {
		jobs := make(chan *domain.User, len(users))
		results := make(chan error, len(users))
		for _, user := range users {
			jobs <- user
		}
		close(jobs)
		var members sync.WaitGroup
		for range memberConcurrency {
			members.Add(1)
			go func() {
				defer members.Done()
				for user := range jobs {
					results <- a.user.ResyncMembershipOrEnqueue(t.Context(), user.ID, "trial load membership")
				}
			}()
		}
		go func() { members.Wait(); close(results) }()
		completed := 0
		var firstErr error
		for err := range results {
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			completed++
			if mixed.memberReady != nil {
				mixed.memberReady(completed)
			}
		}
		nativeMust(t, firstErr, "admit every real trial principal through bounded production membership sync")
		if completed != len(users) {
			t.Fatal("bounded trial membership omitted a real principal")
		}
	}
	// Block traffic uses the original separate open-group principal and its own target port.
	blockBindPort := 0
	if mixed != nil {
		blockBindPort = nativePort(t)
	}
	_, blockAddress, blockPort, _, blockReached := nativeTargetAtPort(t, blockBindPort)
	setStage("policy")
	input := destinationPolicyInput("Concurrent trial block", "block")
	input["inline"] = map[string]any{"ports": strconv.Itoa(blockPort)}
	var policy destinationPolicyAPIResult
	request("POST", "dest/policies", input, 201, &policy)
	var observePolicy destinationPolicyAPIResult
	if mixed != nil {
		observeInput := destinationPolicyInput("Concurrent mixed observe", "observe")
		observeInput["inline"] = map[string]any{"ports": strconv.Itoa(mixed.observePort)}
		request("POST", "dest/policies", observeInput, 201, &observePolicy)
	}
	var mode destinationGroupModeAPIResult
	setStage("mode")
	request("PUT", fmt.Sprintf("dest/groups/%d", group.ID), map[string]any{"mode": "allowlist", "stage": "trial", "list_ids": []int64{}, "updated_at": int64(0), "base_hosts": []string{}}, 200, &mode)
	setStage("collection")
	request("PUT", fmt.Sprintf("servers/%d", f.agent.PanelID), map[string]any{"audit_collect": collect}, 200, nil)
	setStage("publication")
	request("POST", "dest/publish", nil, 200, nil)
	panel, err := a.repos.XUIPanel.GetByID(t.Context(), f.agent.PanelID)
	setStage("receipt")
	nativeMust(t, err, "bind trial collection revision")
	nativeWait(t, 120*time.Second, "current trial policy and complete real roster", func() bool {
		state, e1 := a.destDefinitions.State(t.Context())
		runtime, e2 := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
		agent, e3 := a.repos.NodeAgent.GetByAgentID(t.Context(), f.agent.AgentID)
		observedPanel, e4 := a.repos.XUIPanel.GetByID(t.Context(), f.agent.PanelID)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || runtime == nil || agent == nil || observedPanel == nil || agent.ObservedCoreEngine != domain.NodeCoreXray || agent.DesiredCoreEngine != domain.NodeCoreXray || agent.DesiredCoreVersion != "26.6.27" || observedPanel.XrayVersion != "26.6.27" || agent.AllowRestrictedReality || agent.Epoch != f.agent.Epoch || state.Paused || !nativeMutationReceiptMatches(prior, *runtime, state.PublishedGeneration, agent.LastSeen, after) || !nativeCollectionReceiptMatches(*runtime, protocol.CollectLevel(collect), uint64(panel.AuditCollectRevision)) {
			return false
		}
		var body protocol.DestinationPolicy
		if json.Unmarshal(runtime.MintedBody, &body) != nil || len(body.Rules) != ruleCount {
			return false
		}
		trialRules, blockRules, observeRules := 0, 0, 0
		for _, rule := range body.Rules {
			if rule.ID == fmt.Sprintf("g%d", group.ID) && rule.Action == protocol.RuleObserve && rule.CatchAll && len(rule.Subjects) == 200 {
				members := make(map[protocol.SubjectKey]bool)
				for _, subject := range rule.Subjects {
					members[subject] = true
				}
				for _, user := range users {
					if !members[protocol.NewSubjectKey(user.ID)] {
						return false
					}
				}
				trialRules++
			}
			if rule.ID == fmt.Sprintf("p%d", policy.ID) && rule.Action == protocol.RuleBlock && rule.Ports == strconv.Itoa(blockPort) && len(rule.Subjects) == 0 && len(rule.Domains) == 0 {
				blockRules++
			}
			if mixed != nil && rule.ID == fmt.Sprintf("p%d", observePolicy.ID) && rule.Action == protocol.RuleObserve && rule.Ports == strconv.Itoa(mixed.observePort) && len(rule.Subjects) == 0 && len(rule.Domains) == 0 {
				observeRules++
			}
		}
		if mixed != nil && (observeRules != 1 || !slicesContainsNative(agent.ObservedCapabilities, protocol.CapabilityAuditUsage)) {
			return false
		}
		if trialRules != 1 || blockRules != 1 {
			return false
		}
		configStream, err := a.repos.NodeAgent.GetStream(t.Context(), f.agent.AgentID, domain.NodeAgentStreamConfig)
		if err != nil || configStream == nil || !configStream.Converged() || configStream.AppliedEpoch != agent.Epoch {
			return false
		}
		stream, err := a.repos.NodeAgent.GetStream(t.Context(), f.agent.AgentID, domain.NodeAgentStreamRoster)
		if err != nil || stream == nil || !stream.Converged() || stream.AppliedEpoch != agent.Epoch {
			return false
		}
		var roster protocol.RosterBody
		if json.Unmarshal(stream.DesiredBody, &roster) != nil {
			return false
		}
		seen := map[protocol.SubjectKey]bool{}
		for _, c := range roster.Clients {
			if c.Enabled {
				for _, l := range c.Listeners {
					if l == protocol.NewListenerKey(f.node.ID) {
						seen[c.Subject] = true
					}
				}
			}
		}
		for _, user := range users {
			if !seen[protocol.NewSubjectKey(user.ID)] {
				return false
			}
		}
		return seen[protocol.NewSubjectKey(f.user.ID)]
	})
	return users, group, policy, blockAddress, blockPort, blockReached
}
