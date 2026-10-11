package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

func TestNativeSyncPartitionRetainsOnlyImmutableAuditEvidence(t *testing.T) {
	partition := &nativeSyncPartition{}
	observation := protocol.AuditObservation{BatchID: strings.Repeat("a", 32), Kind: "block", Hour: time.Now().UTC().Truncate(time.Hour).UnixMilli(), CollectRevision: 1}
	observation.Hits = []protocol.AuditHit{{Hour: observation.Hour, RuleID: "p1", Action: "block", Subject: protocol.NewSubjectKey(2), Dest: "2001:db8::1", Port: 443, Count: 3, FirstMS: time.Now().UnixMilli(), LastMS: time.Now().UnixMilli()}}
	if err := protocol.ValidateAuditObservation(observation); err != nil {
		t.Fatal(err)
	}
	encode := func(audit *protocol.AuditObservation) string {
		body, err := json.Marshal(struct {
			Audit *protocol.AuditObservation `json:"audit,omitempty"`
		}{audit})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	partition.observe(strings.NewReader(encode(&observation)))
	partition.observe(strings.NewReader(encode(&observation)))
	attempts, batches := partition.snapshot()
	got := batches["block"]
	if attempts != 2 || got.Seen != 2 || got.Events != 3 || got.Rows != 1 || got.Changed || got.Digest == ([32]byte{}) {
		t.Fatal("partition did not identify the exact frozen replay")
	}
	observation.Hits[0].Count++
	partition.observe(strings.NewReader(encode(&observation)))
	_, batches = partition.snapshot()
	if !batches["block"].Changed || batches["block"].Digest != got.Digest || batches["block"].Events != got.Events {
		t.Fatal("partition silently replaced its first immutable batch")
	}
	partition.observe(strings.NewReader(encode(nil)))
	partition.observe(strings.NewReader(`{"audit":{"kind":"block"}}`))
	partition.observe(strings.NewReader("malformed private data"))
	partition.observe(strings.NewReader(`{"private":"` + strings.Repeat("x", 2<<20) + `","audit":null}`))
	attempts, batches = partition.snapshot()
	if attempts != 7 || len(batches) != 1 || batches["block"].Seen != 3 {
		t.Fatal("partition treated absent, invalid or oversized audit as evidence")
	}
	delete(batches, "block")
	_, preserved := partition.snapshot()
	if len(preserved) != 1 {
		t.Fatal("caller mutated retained evidence")
	}
}

func TestNativeSyncPartitionBlocksOnlySyncAndKeepsOriginalAdminHandler(t *testing.T) {
	partition := &nativeSyncPartition{}
	gateway := &nativePanelGateway{}
	var seen atomic.Int64
	gateway.replace(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	gateway.partitionSync(partition)
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/v1/node/sync", http.StatusServiceUnavailable},
		{"GET", "/api/admin/dest/status", http.StatusNoContent},
		{"PUT", "/api/admin/dest/pause", http.StatusNoContent},
	} {
		response := httptest.NewRecorder()
		gateway.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"audit":null}`)))
		if response.Code != tc.want {
			t.Fatal("partition changed unrelated HTTP behavior")
		}
	}
	if attempts, _ := partition.snapshot(); attempts != 1 || seen.Load() != 2 {
		t.Fatal("partition did not isolate node synchronization")
	}
	gateway.partitionSync(nil)
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest("POST", "/v1/node/sync", strings.NewReader(`{"audit":null}`)))
	if response.Code != http.StatusNoContent || seen.Load() != 3 {
		t.Fatal("restored sync did not reach the original handler")
	}
}

func TestNativeSyncResponseLossProcessesOriginalRequestsAndDropsRealResponses(t *testing.T) {
	loss := &nativeSyncResponseLoss{}
	gateway := &nativePanelGateway{}
	const body = `{"audit":null,"private":"fixture-only"}`
	var seen atomic.Int64
	gateway.replace(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil || (r.Method == "POST" && string(data) != body) {
			t.Error("response loss changed the original request")
		}
		seen.Add(1)
		w.Header().Set("X-Original", "preserved")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "real private response")
	}))
	server := httptest.NewTLSServer(gateway)
	defer server.Close()
	gateway.loseSyncResponses(loss)
	for range 2 {
		response, err := server.Client().Post(server.URL+"/v1/node/sync", "application/json", strings.NewReader(body))
		if response != nil {
			response.Body.Close()
		}
		if err == nil {
			t.Fatal("node received an acknowledgement during response loss")
		}
	}
	if attempts, batches, successes := loss.snapshot(); attempts != 2 || len(batches) != 0 || successes != 2 {
		t.Fatal("response loss did not count actual production handler responses")
	}
	response, err := server.Client().Get(server.URL + "/api/admin/dest/status")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.Header.Get("X-Original") != "preserved" || string(data) != "real private response" {
		t.Fatal("response loss changed unrelated administrator responses")
	}
	gateway.loseSyncResponses(nil)
	response, err = server.Client().Post(server.URL+"/v1/node/sync", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(data) != "real private response" || seen.Load() != 4 {
		t.Fatal("restored sync did not preserve the actual handler response")
	}
}

// The TLS listener belongs to the fixture, while each handler belongs to one
// production App. Replacing it drains old requests before their SQL pool closes.
type nativePanelGateway struct {
	mu        sync.RWMutex
	next      http.Handler
	partition *nativeSyncPartition
	loss      *nativeSyncResponseLoss
}

// The production handler consumes its original request and owns ingestion. Only
// its network response is discarded; neither payload nor acknowledgement is kept.
type nativeSyncResponseLoss struct {
	evidence nativeSyncPartition
	mu       sync.Mutex
	accepted uint64
}

func (l *nativeSyncResponseLoss) snapshot() (uint64, map[string]nativeAuditEvidence, uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempts, batches := l.evidence.snapshot()
	return attempts, batches, l.accepted
}

func (l *nativeSyncResponseLoss) serve(next http.Handler, r *http.Request) {
	body := &nativeBoundedReport{}
	r.Body = &nativeTeeBody{Reader: io.TeeReader(r.Body, body), Closer: r.Body}
	response := &nativeDiscardResponse{header: make(http.Header)}
	next.ServeHTTP(response, r)
	l.mu.Lock()
	if body.truncated {
		l.evidence.observe(strings.NewReader(""))
	} else {
		l.evidence.observe(&body.buf)
	}
	if response.code == 0 || response.code >= 200 && response.code < 300 {
		l.accepted++
	}
	l.mu.Unlock()
	// net/http closes the real connection without a synthesized HTTP response.
	panic(http.ErrAbortHandler)
}

type nativeDiscardResponse struct {
	header http.Header
	code   int
}

func (w *nativeDiscardResponse) Header() http.Header { return w.header }
func (w *nativeDiscardResponse) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
}
func (w *nativeDiscardResponse) Write(data []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return len(data), nil
}

func (g *nativePanelGateway) loseSyncResponses(loss *nativeSyncResponseLoss) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loss = loss
}

type nativeAuditEvidence struct {
	Digest     [32]byte
	Seen, Rows int
	Events     uint64
	Changed    bool
}

// A fixture network partition consumes real TLS requests while returning 503.
// It retains only counts and the first batch's hash, never rows or credentials.
type nativeSyncPartition struct {
	mu       sync.Mutex
	attempts uint64
	batches  map[string]nativeAuditEvidence
}

func (p *nativeSyncPartition) observe(reader io.Reader) {
	body := &nativeBoundedReport{}
	_, _ = io.Copy(body, reader)
	var report struct {
		Audit json.RawMessage `json:"audit"`
	}
	var audit protocol.AuditObservation
	valid := !body.truncated && json.Unmarshal(body.buf.Bytes(), &report) == nil && len(report.Audit) > 0 && json.Unmarshal(report.Audit, &audit) == nil && protocol.ValidateAuditObservation(audit) == nil
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts++
	if !valid {
		return
	}
	if p.batches == nil {
		p.batches = make(map[string]nativeAuditEvidence, 4)
	}
	evidence := p.batches[audit.Kind]
	digest := sha256.Sum256(report.Audit)
	if evidence.Seen == 0 {
		evidence.Digest = digest
		evidence.Rows = len(audit.Hits) + len(audit.Usage)
		for _, row := range audit.Hits {
			evidence.Events += uint64(row.Count)
		}
		for _, row := range audit.Usage {
			evidence.Events += uint64(row.Count)
		}
	} else if evidence.Digest != digest {
		evidence.Changed = true
	}
	evidence.Seen++
	p.batches[audit.Kind] = evidence
}

func (p *nativeSyncPartition) snapshot() (uint64, map[string]nativeAuditEvidence) {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make(map[string]nativeAuditEvidence, len(p.batches))
	for kind, evidence := range p.batches {
		result[kind] = evidence
	}
	return p.attempts, result
}

func (g *nativePanelGateway) partitionSync(partition *nativeSyncPartition) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.partition = partition
}

func (g *nativePanelGateway) replace(next http.Handler) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next = next
}

func (g *nativePanelGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.partition != nil && r.Method == "POST" && r.URL.Path == "/v1/node/sync" {
		g.partition.observe(r.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if g.next == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if g.loss != nil && r.Method == "POST" && r.URL.Path == "/v1/node/sync" {
		g.loss.serve(g.next, r)
		return
	}
	g.next.ServeHTTP(w, r)
}

func TestNativePanelGatewayDrainsBeforeReplacingHandler(t *testing.T) {
	entered, release, done, swapped := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	gateway := &nativePanelGateway{}
	gateway.replace(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	oldResponse := httptest.NewRecorder()
	go func() {
		gateway.ServeHTTP(oldResponse, httptest.NewRequest("GET", "/", nil))
		close(done)
	}()
	<-entered
	go func() { gateway.replace(nil); close(swapped) }()
	select {
	case <-swapped:
		t.Error("gateway retired an app while it still owned a request")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	<-done
	<-swapped
	if oldResponse.Code != http.StatusNoContent {
		t.Fatal("in-flight request lost its original handler")
	}
	paused := httptest.NewRecorder()
	gateway.ServeHTTP(paused, httptest.NewRequest("GET", "/", nil))
	if paused.Code != http.StatusServiceUnavailable {
		t.Fatal("restart window reached a closed app")
	}
	gateway.replace(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	restored := httptest.NewRecorder()
	gateway.ServeHTTP(restored, httptest.NewRequest("GET", "/", nil))
	if restored.Code != http.StatusAccepted {
		t.Fatal("restored request reached a retired handler")
	}
}

// Observe the first real request after arm, or the first valid rejected status
// when explicitly requested. The production handler receives the original bytes;
// private report payloads are discarded and prior armed requests cannot be reused.
type nativeRestartProbe struct {
	mu                    sync.Mutex
	armed, seen, accepted bool
	status                *protocol.PolicyStatus
	wantedState           string
	generation            uint64
}

func (p *nativeRestartProbe) arm() {
	p.armState("")
}
func (p *nativeRestartProbe) armRejected() {
	p.armState("rejected")
}
func (p *nativeRestartProbe) armState(state string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.armed, p.seen, p.accepted, p.status = true, false, false, nil
	p.wantedState = state
	p.generation++
}
func (p *nativeRestartProbe) first() (*protocol.PolicyStatus, bool, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status == nil {
		return nil, p.seen, p.accepted
	}
	status := *p.status
	status.Listeners = append([]protocol.ListenerKey(nil), status.Listeners...)
	return &status, p.seen, p.accepted
}
func (p *nativeRestartProbe) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		capture := p.armed && r.Method == "POST" && r.URL.Path == "/v1/node/sync"
		wanted, generation := p.wantedState, p.generation
		if capture && wanted == "" {
			p.armed = false
		}
		p.mu.Unlock()
		if !capture {
			next.ServeHTTP(w, r)
			return
		}
		body := &nativeBoundedReport{}
		r.Body = &nativeTeeBody{Reader: io.TeeReader(r.Body, body), Closer: r.Body}
		response := &nativeProbeResponse{ResponseWriter: w}
		next.ServeHTTP(response, r)
		var report struct {
			PolicyStatus *protocol.PolicyStatus `json:"policy_status"`
		}
		valid := !body.truncated && json.Unmarshal(body.buf.Bytes(), &report) == nil && report.PolicyStatus != nil
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.generation != generation || wanted != "" && (!p.armed || !valid || report.PolicyStatus.State != wanted || protocol.ValidatePolicyStatus(*report.PolicyStatus) != nil) {
			return
		}
		p.armed = false
		p.seen, p.accepted = true, valid && (response.code == 0 || response.code == 200)
		if valid {
			p.status = report.PolicyStatus
		}
	})
}

type nativeTeeBody struct {
	io.Reader
	io.Closer
}
type nativeBoundedReport struct {
	buf       bytes.Buffer
	truncated bool
}

func (b *nativeBoundedReport) Write(data []byte) (int, error) {
	n := len(data)
	if remaining := (1 << 20) - b.buf.Len(); n > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	_, _ = b.buf.Write(data)
	return n, nil
}

type nativeProbeResponse struct {
	http.ResponseWriter
	code int
}

func (w *nativeProbeResponse) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func TestNativeRestartProbePreservesHandlerAndFirstReport(t *testing.T) {
	probe := &nativeRestartProbe{}
	const first = `{"policy_status":{"state":"applied","digest":"first"},"private":"not retained"}`
	const second = `{"policy_status":{"state":"rejected","digest":"second"}}`
	want := first
	handler := probe.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil || string(data) != want {
			t.Fatal("probe changed the production request body")
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "unchanged response")
	}))
	probe.arm()
	for _, body := range []string{first, second} {
		want = body
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/node/sync", strings.NewReader(body)))
		if response.Code != 200 || response.Body.String() != "unchanged response" {
			t.Fatal("probe changed the production response")
		}
	}
	got, seen, accepted := probe.first()
	if !seen || !accepted || got == nil || got.State != "applied" || got.Digest != "first" {
		t.Fatal("probe did not retain only the first real report")
	}
	probe.arm()
	if _, seen, _ := probe.first(); seen {
		t.Fatal("probe reused a report before the restart")
	}
}

func TestNativeRestartProbeRejectsUnacceptedAndTruncatedReport(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"refused", `{"policy_status":{"state":"applied"}}`, 401},
		{"malformed", "private not JSON", 200},
		{"oversized", `{"private":"` + strings.Repeat("x", 2<<20) + `","policy_status":{"state":"applied"}}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &nativeRestartProbe{}
			probe.arm()
			handler := probe.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(tc.status)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/node/sync", strings.NewReader(tc.body)))
			_, seen, accepted := probe.first()
			if !seen || accepted {
				t.Fatal("probe treated an invalid or refused report as accepted")
			}
		})
	}
}
