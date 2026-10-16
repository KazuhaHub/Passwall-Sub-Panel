//go:build destination_acceptance && linux

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
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
	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type nativeDayUTCHour struct {
	HourMS      int64 `json:"hour_ms"`
	TrialRows   int64 `json:"trial_rows"`
	TrialEvents int64 `json:"trial_events"`
	BlockEvents int64 `json:"block_events"`
}

type nativeDaySQLNode struct {
	Owner       int   `json:"owner"`
	Rows        int64 `json:"trial_rows"`
	TrialEvents int64 `json:"trial_events"`
	BlockEvents int64 `json:"block_events"`
	Sites       int64 `json:"distinct_sites"`
}

type nativeDayReport struct {
	FileBytes      [3]int64                 `json:"sqlite_file_bytes"`
	NodeBaseline   [10]nativeMixedAccess    `json:"node_access_baseline"`
	LossBefore     [3]int64                 `json:"loss_before"`
	LossAfter      [3]int64                 `json:"loss_after"`
	LatencyMS      int64                    `json:"latency_window_ms"`
	SQLDrainMS     int64                    `json:"sql_drain_ms"`
	SQLNodes       []nativeDaySQLNode       `json:"sql_nodes"`
	Schema         int                      `json:"schema_version"`
	Candidate      string                   `json:"candidate"`
	Core           string                   `json:"core_version"`
	Database       string                   `json:"database"`
	Owners         int                      `json:"native_owners"`
	Users          int                      `json:"users"`
	Sites          int                      `json:"sites"`
	DomainSHA      string                   `json:"domain_set_sha256"`
	StartedMS      int64                    `json:"started_unix_ms"`
	EndedMS        int64                    `json:"ended_unix_ms"`
	WindowMS       int64                    `json:"window_ms"`
	TrialAttempted uint64                   `json:"trial_attempted"`
	TrialDelivered uint64                   `json:"trial_delivered"`
	TrialTarget    int64                    `json:"trial_target_requests"`
	BlockAttempted uint64                   `json:"block_attempted"`
	BlockDelivered uint64                   `json:"block_delivered"`
	BlockDenied    uint64                   `json:"block_denied"`
	Failures       uint64                   `json:"transport_failures"`
	Late           uint64                   `json:"late_or_wrong_hour"`
	Controls       uint64                   `json:"control_successes"`
	UserHours      [24][200]uint64          `json:"user_hour_deliveries"`
	SiteHours      [24][200]int             `json:"user_hour_unique_sites"`
	OwnerHours     [24][10]uint64           `json:"owner_hour_deliveries"`
	NodeHours      []nativeDayNodeHour      `json:"node_hours"`
	Pages          nativePageReport         `json:"pages"`
	Histograms     []nativeLatencyHistogram `json:"latency_histograms"`
	UTC            []nativeDayUTCHour       `json:"utc_sql_hours"`
	Rows           int64                    `json:"trial_stored_rows"`
	Events         int64                    `json:"trial_stored_events"`
	BlockEvents    int64                    `json:"block_stored_events"`
	Identified     int64                    `json:"trial_identified_rows"`
	LossRows       int64                    `json:"loss_rows"`
	LossEvents     int64                    `json:"loss_events"`
	LossUnmatched  int64                    `json:"loss_unmatched"`
	DBBytes        int64                    `json:"sqlite_allocated_bytes"`
	HitsBytes      int64                    `json:"dest_hits_allocated_bytes"`
	Completed      int                      `json:"complete"`
}

// The day driver has an independent disposable-host entrypoint. It never
// impersonates GITHUB_ACTIONS or relaxes the existing hosted database fixture.
func nativeBuildTrialDayFixture(t *testing.T) destinationPolicyFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	info, statErr := os.Stat(root)
	if err != nil || statErr != nil || !filepath.IsAbs(root) || !info.IsDir() || info.Mode().Perm()&0077 != 0 || os.Geteuid() != 0 || os.Getenv("PSP_NATIVE_TRIAL_DAY") != "24h" || os.Getenv("PSP_NATIVE_DB_DSN") != "" || (os.Getenv("PSP_NATIVE_DB_KIND") != "" && os.Getenv("PSP_NATIVE_DB_KIND") != "sqlite") {
		t.Fatal("real day requires an explicit isolated root-owned SQLite host directory")
	}
	dir := filepath.Join(root, "application")
	nativeMust(t, os.Mkdir(dir, 0700), "create fresh persistent private day database directory")
	cfg, err := nativeDestinationDatabaseConfig("sqlite", config.Config{Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("d", 48), EncryptionKey: strings.Repeat("e", 48), ConfigDir: filepath.Join(dir, "config"), DataDir: filepath.Join(dir, "data")})
	nativeMust(t, err, "configure isolated real-day SQLite")
	a, err := buildWithDestinationAuditStore(t.Context(), &cfg, nil)
	nativeMust(t, err, "build real-day production application")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		nativeMust(t, a.Shutdown(ctx), "close real-day database")
		sqlstore.ConfigureSecretKey("")
	})
	nativeVerifyDestinationDatabase(t, a)
	var dbstatAvailable int64
	nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM dbstat").Scan(&dbstatAvailable), "verify SQLite byte measurement before real-day traffic")
	return seedDestinationPolicyFixture(t, a)
}

func nativeDayToken(t *testing.T, a *App, admin *domain.User) string {
	t.Helper()
	current, err := a.repos.User.GetByID(t.Context(), admin.ID)
	nativeMust(t, err, "read current real-day administrator")
	if current == nil || !current.Enabled || current.Role != domain.RoleAdmin {
		t.Fatal("real-day administrator is no longer valid")
	}
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	nativeMust(t, err, "read real-day issuer")
	token, err := jwtutil.NewIssuer(a.cfg.JWTSecret, func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: settings.JWTIssuer}
	}).IssueAccess(current.ID, current.UPN, current.Role, current.TokenVersion)
	nativeMust(t, err, "issue owned real-day administrator token")
	return token
}

// Keep private database paths in memory. Export only main/WAL/journal sizes;
// allocated logical pages and dbstat table/index bytes remain separate units.
func nativeDayFileSizes(t *testing.T, a *App) [3]int64 {
	t.Helper()
	rows, err := a.database.QueryContext(t.Context(), "PRAGMA database_list")
	nativeMust(t, err, "locate owned real-day SQLite files")
	var path string
	for rows.Next() {
		var seq int
		var name, file string
		nativeMust(t, rows.Scan(&seq, &name, &file), "read private SQLite path")
		if name == "main" {
			path = file
		}
	}
	nativeMust(t, rows.Err(), "finish SQLite paths")
	nativeMust(t, rows.Close(), "close SQLite paths")
	root, err := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	nativeMust(t, err, "resolve owned day file directory")
	actual, err := filepath.EvalSymlinks(path)
	nativeMust(t, err, "resolve owned day SQLite file")
	relative, err := filepath.Rel(root, actual)
	if err != nil || !filepath.IsAbs(actual) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatal("day SQLite file escaped its owned output directory")
	}
	var out [3]int64
	for i, suffix := range []string{"", "-wal", "-journal"} {
		info, err := os.Stat(actual + suffix)
		if os.IsNotExist(err) && i > 0 {
			continue
		}
		nativeMust(t, err, "read real-day SQLite file size")
		if !info.Mode().IsRegular() {
			t.Fatal("day SQLite file is not regular")
		}
		out[i] = info.Size()
	}
	return out
}

func nativeDayPublish(t *testing.T, c *nativeGlobalCohort, token string, users []*domain.User, group, policy int64, blockPort int) {
	t.Helper()
	a := c.fixture.a
	prior := make([]*domain.DestAgentPolicy, len(c.peers))
	for i, p := range c.peers {
		var err error
		prior[i], err = a.repos.DestAgentPolicy.Get(t.Context(), p.agent.AgentID, true)
		nativeMust(t, err, "bind prior real-day policy")
		response := serverAuditRequest(t, a, token, "PUT", "/"+strconv.FormatInt(p.agent.PanelID, 10), map[string]any{"audit_collect": "hits"})
		if response.Code != http.StatusOK {
			t.Fatal("real-day hits-only collection update failed")
		}
	}
	after := time.Now()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, c.server.URL+"/api/admin/dest/publish", nil)
	nativeMust(t, err, "create real-day publication")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.server.Client().Do(req)
	nativeMust(t, err, "publish real-day policy")
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("real-day publication failed")
	}
	nativeWait(t, 240*time.Second, "all ten current hits-only policies and complete 200-user rosters", func() bool {
		state, err := a.destDefinitions.State(t.Context())
		if err != nil || state.Paused {
			return false
		}
		for i, peer := range c.peers {
			p, e1 := a.repos.DestAgentPolicy.Get(t.Context(), peer.agent.AgentID, true)
			agent, e2 := a.repos.NodeAgent.GetByAgentID(t.Context(), peer.agent.AgentID)
			panel, e3 := a.repos.XUIPanel.GetByID(t.Context(), peer.agent.PanelID)
			roster, e4 := a.repos.NodeAgent.GetStream(t.Context(), peer.agent.AgentID, domain.NodeAgentStreamRoster)
			if e1 != nil || e2 != nil || e3 != nil || e4 != nil || p == nil || agent == nil || panel == nil || roster == nil || !c.ready(peer) || !nativeMutationReceiptMatches(prior[i], *p, state.PublishedGeneration, agent.LastSeen, after) || !nativeCollectionReceiptMatches(*p, protocol.CollectHits, uint64(panel.AuditCollectRevision)) {
				return false
			}
			var body protocol.DestinationPolicy
			var members protocol.RosterBody
			if json.Unmarshal(p.MintedBody, &body) != nil || json.Unmarshal(roster.DesiredBody, &members) != nil || len(body.Rules) != 2 {
				return false
			}
			trial, block := 0, 0
			for _, r := range body.Rules {
				if r.ID == fmt.Sprintf("g%d", group) && r.CatchAll && r.Action == protocol.RuleObserve && len(r.Subjects) == 200 {
					seen := map[protocol.SubjectKey]bool{}
					for _, s := range r.Subjects {
						seen[s] = true
					}
					for _, u := range users {
						if !seen[protocol.NewSubjectKey(u.ID)] {
							return false
						}
					}
					trial++
				}
				if r.ID == fmt.Sprintf("p%d", policy) && r.Action == protocol.RuleBlock && r.Ports == strconv.Itoa(blockPort) && len(r.Subjects) == 0 && len(r.Domains) == 0 {
					block++
				}
			}
			if trial != 1 || block != 1 {
				return false
			}
			seen := map[protocol.SubjectKey]bool{}
			for _, m := range members.Clients {
				if m.Enabled {
					for _, l := range m.Listeners {
						if l == protocol.NewListenerKey(peer.node.ID) {
							seen[m.Subject] = true
						}
					}
				}
			}
			if !seen[protocol.NewSubjectKey(c.fixture.user.ID)] {
				return false
			}
			for _, u := range users {
				if !seen[protocol.NewSubjectKey(u.ID)] {
					return false
				}
			}
		}
		return true
	})
}

func nativeDayPage(ctx context.Context, client *http.Client, endpoint, token string, policies bool, own nativePageIdentity, panels map[int64]bool, start time.Time) (nativePageSample, error) {
	path := "/api/admin/dest/hits?group_by=none&include_trial=1&page=1&page_size=50"
	if policies {
		path = "/api/admin/dest/policies"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
	if err != nil {
		return nativePageSample{}, errors.New("day page request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	began := time.Now()
	response, err := client.Do(req)
	if err != nil {
		return nativePageSample{}, errors.New("day page transport")
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	closeErr := response.Body.Close()
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") || readErr != nil || closeErr != nil || len(body) > 1<<20 {
		return nativePageSample{}, errors.New("day page response")
	}
	items, total, valid := 0, int64(0), false
	if policies {
		items, total, valid = nativeValidatePage(body, true, own)
	} else {
		var page struct {
			Items []domain.DestHitRecord  `json:"items"`
			Group string                  `json:"group_by"`
			Total int64                   `json:"total"`
			Page  int                     `json:"page"`
			Size  int                     `json:"page_size"`
			Loss  *domain.DestAuditLosses `json:"losses"`
		}
		if json.Unmarshal(body, &page) == nil && page.Items != nil && page.Group == "none" && page.Page == 1 && page.Size == 50 && len(page.Items) <= 50 && page.Total >= int64(len(page.Items)) && page.Loss != nil && page.Loss.Scope == "panel" && !page.Loss.Complete && page.Loss.Rows >= 0 && page.Loss.Events >= 0 && page.Loss.Unmatched >= 0 {
			valid = true
			items, total = len(page.Items), page.Total
			for _, r := range page.Items {
				if !panels[r.PanelID] || r.Count <= 0 || r.Dest == "" || r.FirstAt > r.LastAt {
					valid = false
				}
				if r.Source == fmt.Sprintf("g%d", own.group) {
					valid = valid && r.UserID == 0 && r.UserUPN == nil && r.Port == 0 && r.Action == "observe"
				} else if r.Source == fmt.Sprintf("p%d", own.policy) {
					valid = valid && r.UserID == own.user && r.Action == "block"
				} else {
					valid = false
				}
			}
		}
	}
	if !valid {
		return nativePageSample{}, errors.New("day page identity")
	}
	return nativePageSample{ElapsedMS: time.Since(start).Milliseconds(), DurationUS: time.Since(began).Microseconds(), Items: items, Total: total}, nil
}

// This root is deliberately absent from the ordinary native acceptance matrix.
// Run only on the separately authorized disposable long-duration host; there is
// no duration/rate override and no accelerated clock or shortened success path.
func TestNativeDestinationTrialRealDay(t *testing.T) {
	if os.Getenv("PSP_NATIVE_TRIAL_DAY") != "24h" {
		t.Fatal("real-day execution requires its explicit dedicated entrypoint")
	}
	if deadline, ok := t.Deadline(); !ok || time.Until(deadline) < 25*time.Hour {
		t.Fatal("real-day execution requires at least25h of actual test lifetime")
	}
	evidence := os.Getenv("PSP_NATIVE_TRIAL_DAY_METRICS")
	root, e1 := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	dir, e2 := filepath.EvalSymlinks(filepath.Dir(evidence))
	if e1 != nil || e2 != nil || root != dir || filepath.Base(evidence) != "destination-trial-day-metrics.json" {
		t.Fatal("real-day evidence must belong to its isolated host directory")
	}
	var recorder nativeDayNodeRecorder
	c := nativeBootstrapOwnedCohort(t, nativeGlobalCohortOptions{auditMetrics: true, observeNode: recorder.observe, transport: func(original http.Handler, peers []*nativeGlobalPeer) http.Handler {
		hist := metrics.NewHistogram("fixture_destination_sync_handler_ms", "Owned real-day handler completion.", "ms", metrics.LatencyBucketsMS)
		timed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			began := time.Now()
			if r.URL.Path == "/v1/node/sync" {
				defer hist.ObserveSince(began)
			}
			original.ServeHTTP(w, r)
		})
		metrics.PollStageDuration.With("sink_flush")
		for _, kind := range nativeMixedKinds {
			metrics.DestAuditBeginMS.With(kind)
			metrics.DestAuditChunkMS.With(kind)
		}
		handler, err := nativeGlobalMixedTransport(timed, peers)
		nativeMust(t, err, "bind real-day actual wire transport")
		return handler
	}}, 10, "destination-trial-day-bootstrap-metrics.json", nativeBuildTrialDayFixture)
	f, a := c.fixture, c.fixture.a
	if len(c.peers) != 10 || a.cfg.DBKind() != "sqlite" {
		t.Fatal("real-day fixture lacks ten actual owners and SQLite")
	}
	admin := &domain.User{UPN: "trial-day-admin@example.test", Email: "trial-day-admin@example.test", Role: domain.RoleAdmin, Enabled: true, SSOProvider: domain.SSOProviderLocal, SSOSubject: "trial-day-admin@example.test", UUID: "99999999-9999-4999-8999-999999999999", SubToken: "owned-real-day-admin-token"}
	nativeMust(t, a.repos.User.Create(t.Context(), admin), "create real-day administrator")
	token := nativeDayToken(t, a, admin)
	for _, peer := range c.peers {
		peer.node.Sniffing = `{"enabled":true,"destOverride":["http","tls"],"routeOnly":true}`
		nativeMust(t, a.repos.Node.Update(t.Context(), peer.node), "enable owned route-only day identities")
	}
	stage := "capability"
	users, group, policy, blockAddress, blockPort, blockReached := nativePrepareTrialLoad(t, f, c.server.Client(), c.server.URL, token, f.node.DesiredPort, nativeGlobalBatchOutput(c.peers), &stage, nil)
	nativeDayPublish(t, c, token, users, group.ID, policy.ID, blockPort)
	domains, valid := nativeTrialDayDomains()
	if !valid {
		t.Fatal("real-day PSL domain set invalid")
	}
	domainWire := strings.Join(domains[:], "\n")
	digest := sha256.Sum256([]byte(domainWire))
	_, address, targetPort, nonce, reached := nativeTarget(t)
	_, controlAddress, controlPort, controlNonce, _ := nativeTargetAtPort(t, nativePort(t))
	panels := map[int64]bool{}
	for _, peer := range c.peers {
		panels[peer.agent.PanelID] = true
		nativeMust(t, nativeVLESSHost(t.Context(), peer.node.DesiredPort, address, targetPort, f.user.UUID, nonce, domains[0]), "verify real-day route-only nonce")
		peer.live = nativeBindLiveCore(t, peer.process.Process.Pid, filepath.Join(peer.dir, "node"), "xray")
	}
	state, err := a.destDefinitions.State(t.Context())
	nativeMust(t, err, "bind day pages generation")
	own := nativePageIdentity{user: f.user.ID, group: group.ID, policy: policy.ID, generation: state.PublishedGeneration}
	m := nativeDayReport{Schema: 1, Candidate: "61f30239e09d62b05bd59df7d9d28a5071790556", Core: "26.6.27", Database: "sqlite", Owners: 10, Users: 200, Sites: 300, DomainSHA: hex.EncodeToString(digest[:]), Pages: nativePageReport{Schema: 1, Queries: []nativePageQuery{{Name: "hits_first_page", Samples: []nativePageSample{}}, {Name: "policies_overview", Samples: []nativePageSample{}}}}}
	var attempts, delivered, blockAttempts, blockDelivered, blockDenied, failures, late atomic.Uint64
	var perUser [24][200]atomic.Uint64
	var perOwner [24][10]atomic.Uint64
	var coverage [24][200][5]atomic.Uint64
	write := func() {
		m.TrialAttempted, m.TrialDelivered, m.BlockAttempted, m.BlockDelivered, m.BlockDenied, m.Failures, m.Late = attempts.Load(), delivered.Load(), blockAttempts.Load(), blockDelivered.Load(), blockDenied.Load(), failures.Load(), late.Load()
		m.NodeHours, _ = recorder.snapshot()
		wire, e := json.Marshal(m)
		nativeMust(t, e, "encode closed real-day checkpoint")
		nativeMust(t, os.WriteFile(evidence, append(wire, '\n'), 0600), "persist real-day numeric checkpoint")
	}
	t.Cleanup(write)
	baseline := reached.Load()
	nativeWait(t, 30*time.Second, "quiescent real-day node counters before measured traffic", recorder.ready)
	nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(rows),0),COALESCE(SUM(events),0),COALESCE(SUM(unmatched),0) FROM dest_audit_loss_hourly").Scan(&m.LossBefore[0], &m.LossBefore[1], &m.LossBefore[2]), "bind original pre-window SQL loss units")
	before := nativeTrialLatencySnapshot(t)
	start := time.Now()
	m.StartedMS = start.UnixMilli()
	if !recorder.begin(start) {
		t.Fatal("real-day start lost its quiescent original node baseline")
	}
	m.NodeBaseline = recorder.baseline()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type job struct {
		slot  int
		owner int
		block bool
	}
	jobs := make(chan job, 512)
	var workers, producers sync.WaitGroup
	var closeJobs sync.Once
	for range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			var probe nativeVLESSWorkerProbe
			for j := range jobs {
				peer := c.peers[j.owner]
				if j.block {
					blockAttempts.Add(1)
					header, e := nativeVLESSRequest(f.user.UUID, blockAddress, blockPort, "trial-day-block.test")
					if e != nil {
						failures.Add(1)
						continue
					}
					ok := nativeLoadBlockedRequest(ctx, peer.node.DesiredPort, header, func() {
						blockDelivered.Add(1)
						if time.Since(start) >= nativeDayDuration {
							late.Add(1)
						}
					})
					if ok {
						blockDenied.Add(1)
					} else {
						failures.Add(1)
					}
				} else {
					attempts.Add(1)
					s, _ := nativeTrialDaySlot(j.slot)
					if probe.request(ctx, peer.node.DesiredPort, address, targetPort, users[s.user].UUID, nonce, domains[s.site]) != nil {
						failures.Add(1)
						continue
					}
					delivered.Add(1)
					perUser[s.hour][s.user].Add(1)
					perOwner[s.hour][s.owner].Add(1)
					coverage[s.hour][s.user][s.site/64].Or(uint64(1) << uint(s.site%64))
					if int(time.Since(start)/time.Hour) != s.hour {
						late.Add(1)
					}
				}
			}
		}()
	}
	var endMS atomic.Int64
	var durationMS atomic.Int64
	producers.Add(2)
	go func() {
		defer producers.Done()
		for index := 0; index < nativeDaySlots; index++ {
			if nativeDayWait(ctx, start.Add(time.Duration(index)*nativeDayInterval)) != nil {
				return
			}
			s, _ := nativeTrialDaySlot(index)
			select {
			case jobs <- job{slot: index, owner: s.owner}:
			default:
				failures.Add(1)
			}
		}
		if nativeDayWait(ctx, start.Add(nativeDayDuration)) == nil {
			durationMS.Store(time.Since(start).Milliseconds())
			endMS.Store(time.Now().UnixMilli())
		}
	}()
	go func() {
		defer producers.Done()
		for second := 0; second < 86400; second++ {
			if nativeDayWait(ctx, start.Add(time.Duration(second)*time.Second)) != nil {
				return
			}
			for owner := range 10 {
				select {
				case jobs <- job{owner: owner, block: true}:
				default:
					failures.Add(1)
				}
			}
		}
	}()
	t.Cleanup(func() { cancel(); producers.Wait(); closeJobs.Do(func() { close(jobs) }); workers.Wait() })
	for minute := 0; minute < 1440; minute++ {
		nativeMust(t, nativeDayWait(ctx, start.Add(time.Duration(minute)*time.Minute)), "wait original real-day observation clock")
		if minute%30 == 0 {
			token = nativeDayToken(t, a, admin)
		}
		for _, peer := range c.peers {
			peer.live()
			peer.probe.mu.Lock()
			invalid := peer.probe.invalid
			peer.probe.mu.Unlock()
			if invalid {
				t.Fatal("real-day node metrics or transport became invalid")
			}
			nativeMust(t, nativeVLESSHost(ctx, peer.node.DesiredPort, controlAddress, controlPort, f.user.UUID, controlNonce, "trial-day-control.test"), "real-day independent nonce control")
			m.Controls++
		}
		if _, fresh := recorder.snapshot(); !fresh {
			t.Fatal("real-day node metrics stopped arriving")
		}
		for i := range 2 {
			sample, e := nativeDayPage(ctx, c.server.Client(), c.server.URL, token, i == 1, own, panels, start)
			nativeMust(t, e, "actual real-day first-screen API")
			m.Pages.Queries[i].Samples = append(m.Pages.Queries[i].Samples, sample)
		}
		if time.Since(start) >= time.Duration(minute+1)*time.Minute {
			t.Fatal("real-day observation missed its original minute")
		}
		write()
	}
	producers.Wait()
	closeJobs.Do(func() { close(jobs) })
	workers.Wait()
	m.WindowMS, m.EndedMS = durationMS.Load(), endMS.Load()
	m.Pages.WindowMS = m.WindowMS
	m.TrialTarget = reached.Load() - baseline
	for hour := range 24 {
		for user := range 200 {
			m.UserHours[hour][user] = perUser[hour][user].Load()
			for word := range 5 {
				m.SiteHours[hour][user] += bits.OnesCount64(coverage[hour][user][word].Load())
			}
		}
		for owner := range 10 {
			m.OwnerHours[hour][owner] = perOwner[hour][owner].Load()
		}
	}
	after := nativeTrialLatencySnapshot(t)
	m.LatencyMS = time.Since(start).Milliseconds()
	if before.sinceUnixMS != after.sinceUnixMS {
		t.Fatal("production day histograms reset")
	}
	for i := range before.histograms {
		h, ok := nativeLatencyDifference(before.histograms[i], after.histograms[i])
		if !ok {
			t.Fatal("day latency snapshot changed")
		}
		m.Histograms = append(m.Histograms, h)
	}
	drainStart := time.Now()
	nativeWait(t, 180*time.Second, "all real-day trial and block events durable in original SQLite", func() bool {
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COUNT(*),COALESCE(SUM(count),0),COALESCE(SUM(CASE WHEN user_id<>0 OR port<>0 THEN 1 ELSE 0 END),0) FROM dest_hits WHERE source=? AND action='observe'", fmt.Sprintf("g%d", group.ID)).Scan(&m.Rows, &m.Events, &m.Identified), "read real-day anonymous SQL")
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(count),0) FROM dest_hits WHERE source=? AND action='block'", fmt.Sprintf("p%d", policy.ID)).Scan(&m.BlockEvents), "read real-day block SQL")
		return m.Events == nativeDaySlots && m.BlockEvents == 864000
	})
	m.SQLDrainMS = time.Since(drainStart).Milliseconds()
	allowedSites := map[string]bool{}
	for _, site := range domains {
		allowedSites[site] = true
	}
	for owner, peer := range c.peers {
		r := nativeDaySQLNode{Owner: owner}
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COUNT(*),COALESCE(SUM(count),0),COUNT(DISTINCT dest) FROM dest_hits WHERE panel_id=? AND source=? AND action='observe'", peer.agent.PanelID, fmt.Sprintf("g%d", group.ID)).Scan(&r.Rows, &r.TrialEvents, &r.Sites), "read each actual owner's trial SQL")
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(count),0) FROM dest_hits WHERE panel_id=? AND source=? AND action='block'", peer.agent.PanelID, fmt.Sprintf("p%d", policy.ID)).Scan(&r.BlockEvents), "read each actual owner's block SQL")
		if r.Rows <= 0 || r.Rows > 7500 || r.TrialEvents != 144000 || r.BlockEvents != 86400 || r.Sites != 300 {
			t.Fatal("real-day SQL lost an owner or failed node/domain aggregation")
		}
		rows, err := a.database.QueryContext(t.Context(), "SELECT DISTINCT dest FROM dest_hits WHERE panel_id=? AND source=?", peer.agent.PanelID, fmt.Sprintf("g%d", group.ID))
		nativeMust(t, err, "read original stored day sites")
		for rows.Next() {
			var site string
			nativeMust(t, rows.Scan(&site), "read stored site")
			if !allowedSites[site] {
				_ = rows.Close()
				t.Fatal("real-day SQL site set differs from the production PSL domain set")
			}
		}
		nativeMust(t, rows.Err(), "finish stored sites")
		nativeMust(t, rows.Close(), "close stored sites")
		m.SQLNodes = append(m.SQLNodes, r)
	}
	nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(rows),0),COALESCE(SUM(events),0),COALESCE(SUM(unmatched),0) FROM dest_audit_loss_hourly").Scan(&m.LossAfter[0], &m.LossAfter[1], &m.LossAfter[2]), "read original day loss units")
	for i := range 3 {
		if m.LossAfter[i] < m.LossBefore[i] {
			t.Fatal("original SQL loss counters regressed")
		}
	}
	m.LossRows, m.LossEvents, m.LossUnmatched = m.LossAfter[0]-m.LossBefore[0], m.LossAfter[1]-m.LossBefore[1], m.LossAfter[2]-m.LossBefore[2]
	rows, err := a.database.QueryContext(t.Context(), "SELECT hour_ms,COUNT(CASE WHEN source=? THEN 1 END),COALESCE(SUM(CASE WHEN source=? THEN count ELSE 0 END),0),COALESCE(SUM(CASE WHEN source=? THEN count ELSE 0 END),0) FROM dest_hits GROUP BY hour_ms ORDER BY hour_ms", fmt.Sprintf("g%d", group.ID), fmt.Sprintf("g%d", group.ID), fmt.Sprintf("p%d", policy.ID))
	nativeMust(t, err, "read real UTC boundary buckets")
	for rows.Next() {
		var r nativeDayUTCHour
		nativeMust(t, rows.Scan(&r.HourMS, &r.TrialRows, &r.TrialEvents, &r.BlockEvents), "read UTC row")
		m.UTC = append(m.UTC, r)
	}
	nativeMust(t, rows.Err(), "finish UTC rows")
	nativeMust(t, rows.Close(), "close UTC read")
	var pages, size int64
	nativeMust(t, a.database.QueryRowContext(t.Context(), "PRAGMA page_count").Scan(&pages), "read SQLite pages")
	nativeMust(t, a.database.QueryRowContext(t.Context(), "PRAGMA page_size").Scan(&size), "read SQLite page size")
	m.DBBytes = pages * size
	nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name='dest_hits' OR name IN (SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='dest_hits')").Scan(&m.HitsBytes), "read actual dest_hits and index allocated bytes")
	m.FileBytes = nativeDayFileSizes(t, a)
	write()
	if m.LatencyMS < m.WindowMS || m.LatencyMS > m.WindowMS+10000 || m.SQLDrainMS > 181000 {
		t.Fatal("real-day latency or SQL drain window exceeded its declared scope")
	}
	if m.WindowMS < 86400000 || m.WindowMS > 86401000 || m.EndedMS-m.StartedMS < 86400000 || m.EndedMS-m.StartedMS > 86405000 || m.TrialAttempted != nativeDaySlots || m.TrialDelivered != nativeDaySlots || m.TrialTarget != nativeDaySlots || m.BlockAttempted != 864000 || m.BlockDelivered != 864000 || m.BlockDenied != 864000 || m.Failures != 0 || m.Late != 0 || m.Identified != 0 || m.LossRows != 0 || m.LossEvents != 0 || m.LossUnmatched != 0 || blockReached.Load() != 0 || m.Rows <= 0 || m.Rows > 75000 || m.HitsBytes <= 0 || m.DBBytes < m.HitsBytes || len(m.UTC) < 24 || len(m.UTC) > 25 || m.Controls != 14400 {
		t.Fatal("real-day delivery, clock, privacy, storage or independent block gate failed")
	}
	for hour := range 24 {
		for user := range 200 {
			if m.UserHours[hour][user] != 300 || m.SiteHours[hour][user] != 300 {
				t.Fatal("real day omitted user/hour/domain coverage")
			}
		}
		for owner := range 10 {
			if m.OwnerHours[hour][owner] != 6000 {
				t.Fatal("real day omitted owner/hour coverage")
			}
		}
	}
	for _, h := range m.NodeHours {
		if h.Samples == 0 || h.Heap == 0 || h.AccessDropped != 0 || h.AccessUnmatched != 0 || h.AccessStale != 0 || h.PendingDropped != 0 || h.PendingUnmatched != 0 || h.Dropped != [4]uint64{} || h.Unmatched != [4]uint64{} {
			t.Fatal("real-day node keys/drop evidence incomplete")
		}
	}
	for _, h := range m.Histograms {
		if h.Count <= 0 {
			t.Fatal("actual SQLite ingest or simultaneous traffic poll writes missing")
		}
	}
	m.Completed = 1
	write()
}
