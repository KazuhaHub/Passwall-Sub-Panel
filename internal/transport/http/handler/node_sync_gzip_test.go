package handler

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

type storedCapabilities map[string][]string

func (s storedCapabilities) GetByAgentID(_ context.Context, id string) (*domain.NodeAgent, error) {
	caps, ok := s[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &domain.NodeAgent{AgentID: id, ObservedCapabilities: caps}, nil
}

const haveJSON = `"have":{"config":{"applied":{},"etag":""},"roster":{"applied":{},"etag":""},"directives":{"applied":{},"etag":""}}`

func reportJSON(capabilities ...string) string {
	caps := ""
	if len(capabilities) > 0 {
		caps = `,"capabilities":["` + strings.Join(capabilities, `","`) + `"]`
	}
	return `{"agent_id":"agt_expected","partial":false,` + haveJSON + caps + `}`
}

func gzipped(t *testing.T, body string) []byte {
	t.Helper()
	encoded, err := nodeprotocol.EncodeSyncBody([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

type gzipFixture struct {
	handler *NodeSyncHandler
	calls   atomic.Int32
	seen    atomic.Value // the last report's capabilities
}

func newGzipFixture(t *testing.T, stored storedCapabilities, options ...NodeSyncOption) *gzipFixture {
	t.Helper()
	f := &gzipFixture{}
	options = append([]NodeSyncOption{WithNodeCapabilityReader(stored)}, options...)
	handler, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(_ context.Context, report nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
		f.calls.Add(1)
		f.seen.Store(report.Capabilities)
		return nodeprotocol.SyncResponse{Envelope: nodeprotocol.Envelope{NextPollSeconds: 30}}, nil
	}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_expected", nil }), options...)
	if err != nil {
		t.Fatal(err)
	}
	f.handler = handler
	return f
}

func (f *gzipFixture) do(body io.Reader, header map[string][]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/v1/node/sync", body)
	for name, values := range header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

// An agent that never declared the capability sees exactly what it always did:
// no advertisement, a plain body, even when its HTTP client asks for gzip.
func TestSyncPlainAgentIsUnchanged(t *testing.T) {
	f := newGzipFixture(t, storedCapabilities{})
	for _, header := range []map[string][]string{nil, {"Accept-Encoding": {"gzip"}}, {"Content-Encoding": {"identity"}}} {
		response := f.do(strings.NewReader(reportJSON()), header)
		if response.Code != http.StatusOK {
			t.Fatalf("status %d body %s", response.Code, response.Body.String())
		}
		if got := response.Header().Get("Accept-Encoding") + response.Header().Get("Content-Encoding") + response.Header().Get("Vary"); got != "" {
			t.Fatalf("a plain agent got negotiation headers: %v", response.Header())
		}
		if !strings.HasPrefix(response.Body.String(), "{") {
			t.Fatalf("body is not plain JSON: %q", response.Body.String()[:10])
		}
	}
}

func TestSyncAdvertisesGzipOnlyToAnAgentThatDeclaredIt(t *testing.T) {
	f := newGzipFixture(t, storedCapabilities{})
	response := f.do(strings.NewReader(reportJSON(nodeprotocol.CapabilitySyncGzip)), nil)
	if response.Code != http.StatusOK || response.Header().Get("Accept-Encoding") != "gzip" || response.Header().Get("Content-Encoding") != "" {
		t.Fatalf("status %d headers %v: want an advertisement and a plain body", response.Code, response.Header())
	}
	if !strings.HasPrefix(response.Body.String(), "{") {
		t.Fatal("the response was compressed although the agent did not ask")
	}
	// A refused request carries no advertisement.
	bad := f.do(strings.NewReader(`{`), nil)
	if bad.Code != http.StatusBadRequest || bad.Header().Get("Accept-Encoding") != "" {
		t.Fatalf("error response = %d %v", bad.Code, bad.Header())
	}
}

func TestSyncGzipBothWays(t *testing.T) {
	f := newGzipFixture(t, storedCapabilities{"agt_expected": {nodeprotocol.CapabilitySyncGzip}})
	response := f.do(bytes.NewReader(gzipped(t, reportJSON(nodeprotocol.CapabilitySyncGzip))),
		map[string][]string{"Content-Encoding": {"gzip"}, "Accept-Encoding": {"gzip"}})
	if response.Code != http.StatusOK || f.calls.Load() != 1 {
		t.Fatalf("status %d calls %d body %s", response.Code, f.calls.Load(), response.Body.String())
	}
	if response.Header().Get("Content-Encoding") != "gzip" || response.Header().Get("Accept-Encoding") != "gzip" {
		t.Fatalf("headers %v", response.Header())
	}
	decoded, err := nodeprotocol.DecodeSyncBody(bytes.NewReader(response.Body.Bytes()), "gzip")
	if err != nil || !strings.Contains(string(decoded), `"next_poll_seconds":30`) {
		t.Fatalf("decoded %q err %v", decoded, err)
	}
	if caps, _ := f.seen.Load().([]string); len(caps) != 1 || caps[0] != nodeprotocol.CapabilitySyncGzip {
		t.Fatalf("the service saw capabilities %v", caps)
	}
	// A compressed request that does not ask for a compressed response gets a
	// plain one.
	plain := f.do(bytes.NewReader(gzipped(t, reportJSON(nodeprotocol.CapabilitySyncGzip))), map[string][]string{"Content-Encoding": {"gzip"}})
	if plain.Code != http.StatusOK || plain.Header().Get("Content-Encoding") != "" || !strings.HasPrefix(plain.Body.String(), "{") {
		t.Fatalf("status %d headers %v", plain.Code, plain.Header())
	}
	// q=0 is a refusal.
	refused := f.do(strings.NewReader(reportJSON(nodeprotocol.CapabilitySyncGzip)), map[string][]string{"Accept-Encoding": {"gzip;q=0"}})
	if refused.Header().Get("Content-Encoding") != "" {
		t.Fatal("compressed against gzip;q=0")
	}
}

func TestSyncRefusesGzipThatWasNotNegotiated(t *testing.T) {
	body := gzipped(t, reportJSON(nodeprotocol.CapabilitySyncGzip))
	gzipHeader := map[string][]string{"Content-Encoding": {"gzip"}}
	for _, tc := range []struct {
		name   string
		stored storedCapabilities
		body   []byte
		header map[string][]string
		opts   []NodeSyncOption
	}{
		{"agent never declared it", storedCapabilities{"agt_expected": {"other.v1"}}, body, gzipHeader, nil},
		{"agent unknown to the store", storedCapabilities{}, body, gzipHeader, nil},
		{"declared in the body only", storedCapabilities{"agt_expected": nil}, body, gzipHeader, nil},
		{"stored but the report does not declare it", storedCapabilities{"agt_expected": {nodeprotocol.CapabilitySyncGzip}}, gzipped(t, reportJSON()), gzipHeader, nil},
		{"unknown coding", storedCapabilities{"agt_expected": {nodeprotocol.CapabilitySyncGzip}}, body, map[string][]string{"Content-Encoding": {"br"}}, nil},
		{"two codings", storedCapabilities{"agt_expected": {nodeprotocol.CapabilitySyncGzip}}, body, map[string][]string{"Content-Encoding": {"gzip", "gzip"}}, nil},
		{"a list", storedCapabilities{"agt_expected": {nodeprotocol.CapabilitySyncGzip}}, body, map[string][]string{"Content-Encoding": {"gzip, gzip"}}, nil},
	} {
		f := newGzipFixture(t, tc.stored, tc.opts...)
		response := f.do(bytes.NewReader(tc.body), tc.header)
		if response.Code != http.StatusUnsupportedMediaType || f.calls.Load() != 0 {
			t.Errorf("%s: status %d calls %d, want 415 and no sync", tc.name, response.Code, f.calls.Load())
		}
	}
	// A handler built without the capability reader never decodes anything.
	handler, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(context.Context, nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
		t.Fatal("synced a compressed body without a capability reader")
		return nodeprotocol.SyncResponse{}, nil
	}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_expected", nil }))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/node/sync", bytes.NewReader(body))
	request.Header.Set("Content-Encoding", "gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("no capability reader: status %d", response.Code)
	}
}

func TestSyncGzipLimits(t *testing.T) {
	f := newGzipFixture(t, storedCapabilities{"agt_expected": {nodeprotocol.CapabilitySyncGzip}})
	gzipHeader := map[string][]string{"Content-Encoding": {"gzip"}}

	// A bomb: a few KiB that inflate past the decoded limit is refused, and the
	// service is never reached.
	var bomb bytes.Buffer
	writer := gzip.NewWriter(&bomb)
	chunk := make([]byte, 1<<20)
	for i := int64(0); i < nodeprotocol.MaxSyncDecodedBytes/int64(len(chunk))+2; i++ {
		if _, err := writer.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if bomb.Len() > 1<<20 {
		t.Fatalf("test bomb is %d bytes, expected a small input", bomb.Len())
	}
	if response := f.do(bytes.NewReader(bomb.Bytes()), gzipHeader); response.Code != http.StatusRequestEntityTooLarge || f.calls.Load() != 0 {
		t.Fatalf("bomb: status %d calls %d, want 413", response.Code, f.calls.Load())
	}

	// More than the wire limit on the wire, plain or compressed, is 413.
	oversized := bytes.Repeat([]byte{'a'}, int(nodeprotocol.MaxSyncWireBytes)+1)
	if response := f.do(bytes.NewReader(oversized), nil); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("plain oversized: status %d", response.Code)
	}
	// Incompressible data keeps a valid gzip stream above the wire limit.
	random := make([]byte, nodeprotocol.MaxSyncWireBytes+1<<20)
	rand.New(rand.NewSource(1)).Read(random)
	var wire bytes.Buffer
	writer = gzip.NewWriter(&wire)
	if _, err := writer.Write(random); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if int64(wire.Len()) <= nodeprotocol.MaxSyncWireBytes {
		t.Fatalf("test stream is %d bytes, not over the wire limit", wire.Len())
	}
	if response := f.do(bytes.NewReader(wire.Bytes()), gzipHeader); response.Code != http.StatusRequestEntityTooLarge || f.calls.Load() != 0 {
		t.Fatalf("gzip oversized on the wire: status %d", response.Code)
	}

	// Garbage, a truncated stream and trailing bytes are 400.
	good := gzipped(t, reportJSON(nodeprotocol.CapabilitySyncGzip))
	for name, body := range map[string][]byte{
		"not gzip":  []byte("this is not gzip"),
		"truncated": good[:len(good)/2],
		"trailing":  append(append([]byte{}, good...), "junk"...),
	} {
		if response := f.do(bytes.NewReader(body), gzipHeader); response.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, response.Code)
		}
	}
	if f.calls.Load() != 0 {
		t.Fatal("a refused body reached the service")
	}
}

// A slot pays for inflating only: the wire body is read before one is taken, so
// an uploader that stalls mid-body holds nothing and never blocks anyone.
func TestSyncGzipSlowUploaderDoesNotBlockOthers(t *testing.T) {
	f := newGzipFixture(t, storedCapabilities{"agt_expected": {nodeprotocol.CapabilitySyncGzip}}, WithSyncDecodeConcurrency(1))
	body := gzipped(t, reportJSON(nodeprotocol.CapabilitySyncGzip))
	gzipHeader := map[string][]string{"Content-Encoding": {"gzip"}}

	reader, writer := io.Pipe()
	stalled := make(chan int, 1)
	go func() { stalled <- f.do(reader, gzipHeader).Code }()
	go func() { _, _ = writer.Write(body[:4]) }()
	time.Sleep(20 * time.Millisecond) // the stalled request is now inside its read

	if len(f.handler.decodeSlots) != 0 || len(f.handler.decoding) != 0 {
		t.Fatalf("a stalled upload holds %d slots and %d agents", len(f.handler.decodeSlots), len(f.handler.decoding))
	}
	other := f.do(bytes.NewReader(body), gzipHeader)
	if other.Code != http.StatusOK {
		t.Fatalf("a request behind a stalled uploader got %d", other.Code)
	}
	go func() { _, _ = writer.Write(body[4:]); _ = writer.Close() }()
	if code := <-stalled; code != http.StatusOK {
		t.Fatalf("the stalled request finished with %d", code)
	}
}

// With every slot busy a compressed request waits a bounded time and is then
// told to retry (503, documented: the agent keeps its state on a 5xx and sends
// the same request on its next poll); a plain request needs no slot; the slot
// comes back.
func TestSyncGzipSlotFullIs503(t *testing.T) {
	f := newGzipFixture(t, storedCapabilities{"agt_expected": {nodeprotocol.CapabilitySyncGzip}}, WithSyncDecodeConcurrency(1))
	f.handler.decodeWait = 30 * time.Millisecond
	body := gzipped(t, reportJSON(nodeprotocol.CapabilitySyncGzip))
	gzipHeader := map[string][]string{"Content-Encoding": {"gzip"}}

	f.handler.decodeSlots <- struct{}{} // another agent is inflating
	busy := metrics.NodeSyncDecodeBusyTotal.Value()
	response := f.do(bytes.NewReader(body), gzipHeader)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") == "" || f.calls.Load() != 0 {
		t.Fatalf("status %d calls %d, want 503 with Retry-After", response.Code, f.calls.Load())
	}
	if metrics.NodeSyncDecodeBusyTotal.Value() != busy+1 {
		t.Fatal("the 503 was not counted")
	}
	if len(f.handler.decoding) != 0 {
		t.Fatal("a refused request kept its agent marked")
	}
	if plain := f.do(strings.NewReader(reportJSON()), nil); plain.Code != http.StatusOK {
		t.Fatalf("plain request while slots are busy: %d", plain.Code)
	}
	<-f.handler.decodeSlots
	if again := f.do(bytes.NewReader(body), gzipHeader); again.Code != http.StatusOK {
		t.Fatalf("after the slot came back: %d", again.Code)
	}
	if len(f.handler.decodeSlots) != 0 || len(f.handler.decoding) != 0 {
		t.Fatal("a finished request kept a slot")
	}
}

// One agent inflates one body at a time: a second compressed request while its
// first is in the decode is refused at once, and takes no slot.
func TestSyncGzipOneDecodePerAgent(t *testing.T) {
	f := newGzipFixture(t, storedCapabilities{"agt_expected": {nodeprotocol.CapabilitySyncGzip}}, WithSyncDecodeConcurrency(4))
	body := gzipped(t, reportJSON(nodeprotocol.CapabilitySyncGzip))
	f.handler.decoding = map[string]struct{}{"agt_expected": {}}
	response := f.do(bytes.NewReader(body), map[string][]string{"Content-Encoding": {"gzip"}})
	if response.Code != http.StatusServiceUnavailable || len(f.handler.decodeSlots) != 0 {
		t.Fatalf("status %d slots %d", response.Code, len(f.handler.decodeSlots))
	}
	delete(f.handler.decoding, "agt_expected")
	if again := f.do(bytes.NewReader(body), map[string][]string{"Content-Encoding": {"gzip"}}); again.Code != http.StatusOK {
		t.Fatalf("after the first finished: %d", again.Code)
	}
}

// A slot is released when the body is refused, too.
func TestSyncGzipSlotIsReleasedOnFailure(t *testing.T) {
	f := newGzipFixture(t, storedCapabilities{"agt_expected": {nodeprotocol.CapabilitySyncGzip}}, WithSyncDecodeConcurrency(1))
	for i := 0; i < 3; i++ {
		if response := f.do(strings.NewReader("junk"), map[string][]string{"Content-Encoding": {"gzip"}}); response.Code != http.StatusBadRequest {
			t.Fatalf("status %d", response.Code)
		}
	}
	if len(f.handler.decodeSlots) != 0 || len(f.handler.decoding) != 0 {
		t.Fatal("a refused body kept its slot")
	}
}

// Every response to an agent that declared the capability varies with what it
// asked for; one that did not declare it carries no new header at all.
func TestSyncVaryOnDeclaringAgents(t *testing.T) {
	f := newGzipFixture(t, storedCapabilities{})
	for _, accept := range []map[string][]string{nil, {"Accept-Encoding": {"gzip"}}} {
		if got := f.do(strings.NewReader(reportJSON(nodeprotocol.CapabilitySyncGzip)), accept).Header().Get("Vary"); got != "Accept-Encoding" {
			t.Fatalf("Vary = %q on a response to a declaring agent (request %v)", got, accept)
		}
	}
	if got := f.do(strings.NewReader(reportJSON()), nil).Header().Values("Vary"); len(got) != 0 {
		t.Fatalf("a plain agent got Vary %v", got)
	}
}

// 415s and gzip bytes are counted.
func TestSyncGzipMetrics(t *testing.T) {
	f := newGzipFixture(t, storedCapabilities{"agt_expected": {nodeprotocol.CapabilitySyncGzip}})
	refused := metrics.NodeSyncGzipRefusedTotal.With("unknown_coding").Value()
	f.do(strings.NewReader("x"), map[string][]string{"Content-Encoding": {"br"}})
	if metrics.NodeSyncGzipRefusedTotal.With("unknown_coding").Value() != refused+1 {
		t.Fatal("an unknown coding was not counted")
	}
	notNegotiated := metrics.NodeSyncGzipRefusedTotal.With("not_negotiated").Value()
	other := newGzipFixture(t, storedCapabilities{})
	other.do(bytes.NewReader(gzipped(t, reportJSON())), map[string][]string{"Content-Encoding": {"gzip"}})
	if metrics.NodeSyncGzipRefusedTotal.With("not_negotiated").Value() != notNegotiated+1 {
		t.Fatal("a refusal for a coding never negotiated was not counted")
	}
	wire, decoded := metrics.NodeSyncBytesTotal.With("request_wire").Value(), metrics.NodeSyncBytesTotal.With("request_decoded").Value()
	respWire := metrics.NodeSyncBytesTotal.With("response_wire").Value()
	body := gzipped(t, reportJSON(nodeprotocol.CapabilitySyncGzip))
	f.do(bytes.NewReader(body), map[string][]string{"Content-Encoding": {"gzip"}, "Accept-Encoding": {"gzip"}})
	if metrics.NodeSyncBytesTotal.With("request_wire").Value() != wire+int64(len(body)) ||
		metrics.NodeSyncBytesTotal.With("request_decoded").Value() <= decoded ||
		metrics.NodeSyncBytesTotal.With("response_wire").Value() <= respWire {
		t.Fatal("gzip bytes were not counted")
	}
}

type failingReader struct{}

func (failingReader) GetByAgentID(context.Context, string) (*domain.NodeAgent, error) {
	return nil, errors.New("database down")
}

func TestSyncGzipCapabilityLookupFailureIsAServerError(t *testing.T) {
	f := newGzipFixture(t, nil, WithNodeCapabilityReader(failingReader{}))
	response := f.do(bytes.NewReader(gzipped(t, reportJSON(nodeprotocol.CapabilitySyncGzip))), map[string][]string{"Content-Encoding": {"gzip"}})
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "database") {
		t.Fatalf("status %d body %s", response.Code, response.Body.String())
	}
}

func TestSyncLargeResponseRequiresNegotiatedGzip(t *testing.T) {
	handler, err := NewNodeSyncHandler(nodeSyncServiceFunc(func(context.Context, nodeprotocol.NodeReport) (nodeprotocol.SyncResponse, error) {
		var response nodeprotocol.SyncResponse
		response.Roster.ETag = nodeprotocol.ETag(strings.Repeat("a", int(nodeprotocol.MaxSyncWireBytes)))
		return response, nil
	}), NodeAuthenticatorFunc(func(*http.Request) (string, error) { return "agt_expected", nil }))
	if err != nil {
		t.Fatal(err)
	}
	f := gzipFixture{handler: handler}
	header := map[string][]string{"Accept-Encoding": {"gzip"}}
	if plain := f.do(strings.NewReader(reportJSON()), header); plain.Code != http.StatusInternalServerError {
		t.Fatalf("unnegotiated response status = %d", plain.Code)
	}
	zipped := f.do(strings.NewReader(reportJSON(nodeprotocol.CapabilitySyncGzip)), header)
	if zipped.Code != http.StatusOK || zipped.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("negotiated response status = %d, headers = %v", zipped.Code, zipped.Header())
	}
	decoded, err := nodeprotocol.DecodeSyncBody(zipped.Body, "gzip")
	if err != nil || int64(len(decoded)) <= nodeprotocol.MaxSyncWireBytes {
		t.Fatalf("decoded response size = %d, err = %v", len(decoded), err)
	}
}
