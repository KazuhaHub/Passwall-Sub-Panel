//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destaudit"
	"golang.org/x/net/publicsuffix"
)

// One real producer establishes whether the original frozen-batch/ACK boundary
// can form sufficiently large four-lane packets. It does not saturate PSP's
// global byte capacities or replace the separate 65-owner pressure acceptance.
func nativeByteFormation(t *testing.T, f destinationPolicyFixture, client *http.Client, endpoint, token string, parent int, dataDir string, inbound int, address string, targetPort int, nonce, controlAddress string, controlPort int, controlNonce, evidence string, output func() string, probe *nativeMixedProbe, fault *nativeByteResponseFault, binding *nativeAdmissionBinding, storage *nativeGlobalAuditStorage) {
	const perLane, faultSeconds = 2048, 75
	m := struct {
		Schema       int                       `json:"schema_version"`
		Candidate    string                    `json:"candidate"`
		Core         string                    `json:"core_version"`
		Database     string                    `json:"database"`
		Stage        int                       `json:"stage"`
		Owners       int                       `json:"native_owners"`
		TargetOwners int                       `json:"target_native_owners"`
		MinimumBytes [4]int                    `json:"required_canonical_bytes_exclusive"`
		Users        int                       `json:"trial_users"`
		Attempts     [4]uint64                 `json:"attempted"`
		Delivered    [4]uint64                 `json:"delivered"`
		Failures     uint64                    `json:"transport_failures"`
		FaultMS      int64                     `json:"response_fault_ms"`
		FirstStored  [4]int                    `json:"durable_first_packets"`
		Packets      nativeByteFormationFacts  `json:"packets"`
		HeldQueue    [4]destaudit.LanePressure `json:"held_queue"`
		SQLHeld      uint64                    `json:"held_sql_entries"`
		SQLEarly     uint64                    `json:"delegated_begins_while_held"`
		Ingress      nativeMixedIngress        `json:"access_ingress"`
		Stored       [4]int64                  `json:"stored_events"`
		LossRows     [4]int64                  `json:"loss_rows"`
		Complete     int                       `json:"formation_complete"`
		BytesDone    int                       `json:"byte_saturation_complete"`
		Pressure     int                       `json:"pressure_complete"`
	}{Schema: 2, Candidate: "61f30239e09d62b05bd59df7d9d28a5071790556", Core: "26.6.27", Database: f.a.cfg.DBKind(), Owners: 1}
	var attempted, delivered [4]atomic.Uint64
	var failures atomic.Uint64
	write := func() {
		for i := range 4 {
			m.Attempts[i], m.Delivered[i] = attempted[i].Load(), delivered[i].Load()
		}
		m.Failures = failures.Load()
		m.Packets, _, _ = fault.snapshot()
		data, err := json.Marshal(m)
		nativeMust(t, err, "encode numeric real batch-formation evidence")
		nativeMust(t, os.WriteFile(evidence, append(data, '\n'), 0644), "save owned batch-formation evidence")
	}
	t.Cleanup(write)
	write()
	_, observeAddress, observePort, observeNonce, _ := nativeTargetAtPort(t, nativePort(t))
	stage := "capability"
	users, group, policy, blockAddress, blockPort, blockReached := nativePrepareTrialLoad(t, f, client, endpoint, token, inbound, output, &stage, &nativeMixedSetup{observePort: observePort, memberConcurrency: 8})
	m.Users = len(users)
	if m.Users != 200 || storage == nil || blockPort == targetPort || blockPort == observePort || blockPort == controlPort || observePort == targetPort || observePort == controlPort || targetPort == controlPort {
		t.Fatal("byte formation lacks original principals, storage or independent lane targets")
	}
	name := nativeByteFormationHosts(t, nonce, address, blockAddress, observeAddress, perLane)
	live := nativeBindLiveCore(t, parent, dataDir, "xray")
	nativeMust(t, nativeVLESSHost(t.Context(), inbound, name(3, 0), targetPort, f.user.UUID, nonce, name(3, 0)), "warm original byte formation route")
	nativeMust(t, nativeVLESSHost(t.Context(), inbound, controlAddress, controlPort, f.user.UUID, controlNonce, "formation-control.example.test"), "verify independent actual control target")
	queue, ok := f.a.destAudit.(interface {
		Pressure() [4]destaudit.LanePressure
	})
	if !ok {
		t.Fatal("byte formation replaced the original queue observer")
	}
	lanes := queue.Pressure()
	m.TargetOwners, ok = nativeGlobalOwners(lanes)
	if !ok || m.TargetOwners != 65 {
		t.Fatal("formation target lost the original global owner geometry")
	}
	for i, lane := range lanes {
		m.MinimumBytes[i], ok = nativeByteFormationMinimum(lane, m.TargetOwners)
		if !ok {
			t.Fatal("original lane cannot form a byte-only refusal with one fresh packet per owner")
		}
	}
	readStored := func(kind int) int64 {
		t.Helper()
		query, args := nativeByteFormationStoredQuery(f.agent.PanelID, policy.ID, group.ID, kind)
		var count int64
		nativeMust(t, f.a.database.QueryRowContext(t.Context(), query, args...).Scan(&count), "read original stored formation events")
		return count
	}
	var baseline nativeMixedAccess
	var beforeStored [4]int64
	nativeWait(t, 60*time.Second, "actual warmup parsed and completely drained", func() bool {
		probe.mu.Lock()
		reading, invalid := probe.node, probe.invalid
		probe.mu.Unlock()
		if invalid || len(reading.Lanes) != 4 || reading.Access.Available != 1 || !reading.Access.quiescent() || readStored(3) != 2 {
			return false
		}
		for i, lane := range reading.Lanes {
			if lane.Keys != 0 || lane.Pending != 0 || lane.Dropped != 0 || lane.Unmatched != 0 || queue.Pressure()[i].Batches != 0 {
				return false
			}
		}
		baseline = reading.Access
		return true
	})
	for i := range 4 {
		beforeStored[i] = readStored(i)
	}
	start, armed := fault.arm()
	if !armed {
		t.Fatal("real response fault was already armed")
	}
	t.Cleanup(fault.release)
	faultEnd := start.Add(faultSeconds * time.Second)
	send := func(ctx context.Context, kind, seq int) error {
		host := name(kind, seq)
		if kind == 0 {
			header, err := nativePressureBlockRequest(f.user.UUID, host, blockPort)
			if err != nil {
				return err
			}
			return nativeLoadBlockedRequestFailure(ctx, inbound, header, func() {})
		}
		port, uuid, want := targetPort, f.user.UUID, nonce
		if kind == 1 {
			port, want = observePort, observeNonce
		} else if kind == 2 {
			uuid = users[seq%len(users)].UUID
		}
		return nativeVLESSHost(ctx, inbound, host, port, uuid, want, host)
	}
	// Prime low-priority classes first. A frozen block packet is deliberately
	// preferred in fallback slots; freezing it before other classes would spend
	// the original failure-backoff window repeatedly sending only that packet.
	for _, kind := range []int{3, 2, 1, 0} {
		attempted[kind].Add(1)
		err := send(t.Context(), kind, 1)
		if err != nil {
			failures.Add(1)
		}
		nativeMust(t, err, "prime one actual small packet in each lane")
		delivered[kind].Add(1)
		nativeWait(t, max(time.Millisecond, time.Until(faultEnd)), "actual reverse-priority first packet is frozen before priming the next class", func() bool {
			facts, packets, valid := fault.snapshot()
			return valid && packets[kind].id != "" && facts.Suppressed[kind] > 0
		})
	}
	nativeWait(t, min(65*time.Second, time.Until(faultEnd)), "real first packets durable while their responses remain suppressed", func() bool {
		facts, packets, valid := fault.snapshot()
		if !valid {
			return false
		}
		for i, packet := range packets {
			if packet.id == "" || facts.FirstRows[i] < 1 || facts.FirstRows[i] > 8 || facts.Suppressed[i] == 0 {
				return false
			}
			if f.a.database.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM dest_audit_batches WHERE agent_id=? AND batch_id=? AND kind=?", f.agent.AgentID, packet.id, nativeMixedKinds[i]).Scan(&m.FirstStored[i]) != nil || m.FirstStored[i] != 1 || queue.Pressure()[i].Batches != 0 {
				return false
			}
		}
		return true
	})
	m.Stage = 1
	write()
	ctx, cancel := context.WithDeadline(t.Context(), faultEnd)
	defer cancel()
	type job struct{ kind, seq int }
	jobs := make(chan job, 256)
	var workers sync.WaitGroup
	var closeJobs sync.Once
	for range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := range jobs {
				if err := send(ctx, j.kind, j.seq); err != nil {
					failures.Add(1)
				} else {
					delivered[j.kind].Add(1)
				}
			}
		}()
	}
	t.Cleanup(func() { cancel(); closeJobs.Do(func() { close(jobs) }); workers.Wait() })
	paced := time.Now()
	for n := range perLane * 4 {
		if delay := time.Until(paced.Add(time.Duration(n) * time.Millisecond)); delay > 0 {
			time.Sleep(delay)
		}
		j := job{n % 4, n/4 + 2}
		select {
		case jobs <- j:
			attempted[j.kind].Add(1)
		case <-ctx.Done():
			t.Fatal("original 75-second fault budget cannot form all four live batches")
		}
	}
	closeJobs.Do(func() { close(jobs) })
	workers.Wait()
	if failures.Load() != 0 {
		t.Fatal("real formation traffic failed before response recovery")
	}
	nativeWait(t, max(time.Millisecond, time.Until(faultEnd)), "real collector retains live keys separately from unacknowledged first packets", func() bool {
		probe.mu.Lock()
		reading, invalid := probe.node, probe.invalid
		probe.mu.Unlock()
		if invalid || len(reading.Lanes) != 4 {
			return false
		}
		for i, lane := range reading.Lanes {
			want := perLane
			if i == 3 {
				want *= 3
			}
			if lane.Pending != 1 || lane.Keys < want || lane.Dropped != 0 || lane.Unmatched != 0 {
				return false
			}
		}
		return true
	})
	pause, err := storage.pause()
	nativeMust(t, err, "hold original admitted SQL before real response recovery")
	t.Cleanup(func() { storage.resume(pause) })
	beforeBegins, beforeHeld := storage.begins.Load(), storage.blocked.Load()
	if delay := time.Until(faultEnd); delay > 0 {
		time.Sleep(delay)
	}
	fault.release()
	m.FaultMS, m.Stage = time.Since(start).Milliseconds(), 2
	write()
	if m.FaultMS < 75000 || m.FaultMS > 76000 {
		t.Fatal("real response fault exceeded its recorded 75-second window")
	}
	nativeWait(t, 120*time.Second, "actual canonical retry releases large next packets into original held reservations", func() bool {
		facts, _, valid := fault.snapshot()
		if !valid {
			return false
		}
		view := queue.Pressure()
		for i, lane := range view {
			minimum := m.MinimumBytes[i]
			if facts.Recovered[i] != 1 || facts.NewPackets[i] < 1 || facts.MaxNewBytes[i] <= minimum || lane.Bytes <= minimum || lane.Batches < 1 || lane.Bytes > lane.ByteLimit || lane.Batches > lane.BatchLimit {
				return false
			}
		}
		m.HeldQueue = view
		return true
	})
	m.SQLHeld, m.SQLEarly = storage.blocked.Load()-beforeHeld, storage.begins.Load()-beforeBegins
	if m.SQLHeld != 1 || m.SQLEarly != 0 {
		t.Fatal("real byte formation bypassed the original SQL hold")
	}
	storage.resume(pause)
	nativeWait(t, 180*time.Second, "original SQL and real producer fully drain after formation", func() bool {
		probe.mu.Lock()
		reading, invalid := probe.node, probe.invalid
		probe.mu.Unlock()
		if invalid || len(reading.Lanes) != 4 || !reading.Access.quiescent() {
			return false
		}
		for i, lane := range reading.Lanes {
			if lane.Keys != 0 || lane.Pending != 0 || lane.Dropped != 0 || lane.Unmatched != 0 || queue.Pressure()[i].Batches != 0 {
				return false
			}
		}
		var complete bool
		m.Ingress, complete = nativeMixedIngressBetween(baseline, reading.Access, 3*(perLane+1), perLane+1)
		if !complete || m.Ingress.AllowedNotReceived != 0 || m.Ingress.DeniedNotReceived != 0 {
			return false
		}
		// Empty queue reservations do not by themselves prove that the separate
		// receiver-loss buffer has reached SQL. Reconcile within the same drain.
		for i := range 4 {
			m.Stored[i] = readStored(i) - beforeStored[i]
			nativeMust(t, f.a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(dest_audit_loss_hourly.rows),0) FROM dest_audit_loss_hourly WHERE panel_id=? AND kind=?", f.agent.PanelID, nativeMixedKinds[i]).Scan(&m.LossRows[i]), "read independent original formation loss rows")
			expected := int64(perLane + 1)
			if i == 3 {
				expected *= 3
			}
			if m.Stored[i]+m.LossRows[i] != expected {
				return false
			}
		}
		return true
	})
	if _, valid := binding.snapshot(f.agent.AgentID); !valid || blockReached.Load() != 0 {
		t.Fatal("formation lost authenticated admission binding or blocked traffic escaped")
	}
	live()
	m.Stage, m.Complete = 3, 1
	write()
}

// Hit lanes use their original frozen source/action columns. Kind exists on
// loss and batch records; dest_hits deliberately has no kind column.
func nativeByteFormationStoredQuery(panel, blockPolicy, group int64, kind int) (string, []any) {
	const hits = "SELECT COALESCE(SUM(count),0) FROM dest_hits WHERE "
	switch kind {
	case 0:
		return hits + "panel_id=? AND source=?", []any{panel, fmt.Sprintf("p%d", blockPolicy)}
	case 1:
		return hits + "panel_id=? AND action='observe' AND user_id>0", []any{panel}
	case 2:
		return hits + "panel_id=? AND source=?", []any{panel, fmt.Sprintf("g%d", group)}
	case 3:
		return "SELECT COALESCE(SUM(count),0) FROM dest_usage_hourly WHERE panel_id=?", []any{panel}
	default:
		return "", nil
	}
}

func TestNativeByteFormationStoredQueriesUseOriginalSQLiteSchema(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	for kind := range 4 {
		query, args := nativeByteFormationStoredQuery(f.agent.PanelID, 12, 17, kind)
		var count int64
		nativeMust(t, f.a.database.QueryRowContext(t.Context(), query, args...).Scan(&count), "execute each real formation ledger query against original App migrations")
		if count != 0 {
			t.Fatal("new ledger fixture contains unrelated stored events")
		}
	}
}

func nativeByteFormationHosts(t *testing.T, nonce, address, blocked, observed string, count int) func(int, int) string {
	t.Helper()
	original, err := os.ReadFile("/etc/hosts")
	nativeMust(t, err, "read disposable formation host mappings")
	info, err := os.Stat("/etc/hosts")
	nativeMust(t, err, "bind disposable mapping permissions")
	digest := sha256.Sum256([]byte(nonce))
	prefix := "bf-" + hex.EncodeToString(digest[:6]) + "-"
	if bytes.Contains(original, []byte(prefix)) || net.ParseIP(address) == nil || net.ParseIP(blocked) == nil || net.ParseIP(observed) == nil {
		t.Fatal("formation host mappings overlap foreign state")
	}
	name := func(kind, seq int) string {
		host, _ := nativeByteFormationName(prefix, kind, seq)
		return host
	}
	var additions strings.Builder
	for kind := range 4 {
		ip := address
		if kind == 0 {
			ip = blocked
		} else if kind == 1 {
			ip = observed
		}
		for seq := 0; seq <= count+1; seq++ {
			host, expectedRoot := nativeByteFormationName(prefix, kind, seq)
			root, err := publicsuffix.EffectiveTLDPlusOne(host)
			if err != nil || root != expectedRoot || (kind >= 2 && root != host) {
				t.Fatal("formation padding would fold into an existing trial site")
			}
			fmt.Fprintf(&additions, "%s %s\n", ip, host)
		}
	}
	t.Cleanup(func() {
		nativeMust(t, os.WriteFile("/etc/hosts", original, info.Mode().Perm()), "restore disposable formation mappings")
	})
	nativeMust(t, os.WriteFile("/etc/hosts", append(append([]byte(nil), original...), []byte("\n"+additions.String())...), info.Mode().Perm()), "install actual distinct registrable formation sites")
	return name
}

func nativeByteFormationName(prefix string, kind, seq int) (host, root string) {
	label := fmt.Sprintf("%s%d-%05d-", prefix, kind, seq)
	root = label + strings.Repeat("x", 63-len(label)) + ".test"
	host = root
	if kind < 2 {
		// Identified hits retain the full real destination, up to the original
		// 253-byte domain limit. Anonymous trial/usage keep distinct bare roots.
		host = strings.Repeat("b", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("b", 56) + "." + root
	}
	return
}

func nativeByteFormationMinimum(lane destaudit.LanePressure, owners int) (int, bool) {
	if owners < 2 || owners > 65 || lane.BatchLimit < 2 || lane.ByteLimit < 1 || lane.OwnerLimit < 1 {
		return 0, false
	}
	// One owner stays fresh. Also stay below the original global batch cap so
	// it cannot explain the eventual refusal. Equality does not fill the bytes.
	eligible := min(owners-1, lane.BatchLimit-1)
	minimum := lane.ByteLimit / eligible
	return minimum, minimum < protocol.MaxAuditObservationBytes
}

func TestNativeByteFormationNamesAndOriginalReservationGeometry(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	queue := f.a.destAudit.(interface {
		Pressure() [4]destaudit.LanePressure
	})
	for kind, lane := range queue.Pressure() {
		minimum, valid := nativeByteFormationMinimum(lane, 65)
		if !valid || minimum != [4]int{524288, 270600, 270600, 266305}[kind] {
			t.Fatal("formation minimum lost the original owner/batch/byte capacity geometry")
		}
		eligible := min(64, lane.BatchLimit-1)
		if minimum*eligible > lane.ByteLimit || (minimum+1)*eligible <= lane.ByteLimit {
			t.Fatal("formation boundary confused equality with a byte-exclusive refusal")
		}
		var priorRoot string
		for seq := range 3 {
			host, root := nativeByteFormationName("bf-000000000000-", kind, seq)
			folded, err := publicsuffix.EffectiveTLDPlusOne(host)
			if err != nil || folded != root || root == priorRoot || len(root) != 68 || len(host) != [4]int{253, 253, 68, 68}[kind] || (kind >= 2 && host != root) {
				t.Fatal("real destination padding changed anonymous registrable-root geometry")
			}
			priorRoot = root
			body := fixtureAudit(t, f, seq+1)
			body.Hits[0].Dest = host
			nativeMust(t, protocol.ValidateAuditObservation(*body), "validate actual domain names at original protocol limits")
		}
	}
	for _, bad := range []struct {
		lane   destaudit.LanePressure
		owners int
	}{
		{destaudit.LanePressure{BatchLimit: 128, ByteLimit: 32 << 20, OwnerLimit: 4}, 1},
		{destaudit.LanePressure{BatchLimit: 128, ByteLimit: 32 << 20, OwnerLimit: 4}, 33},
		{destaudit.LanePressure{BatchLimit: 1, ByteLimit: 32 << 20, OwnerLimit: 4}, 65},
	} {
		if _, valid := nativeByteFormationMinimum(bad.lane, bad.owners); valid {
			t.Fatal("impossible fresh-owner byte geometry was silently accepted")
		}
	}
}
