package xui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// TestNew_AuthMethodResolution pins how the explicit auth method maps onto the
// client's apiToken field (which the auth path keys on): password mode forces
// cookie login even when a token is stored; token/auto keep the token.
func TestNew_AuthMethodResolution(t *testing.T) {
	cases := []struct {
		method  domain.XUIAuthMethod
		token   string
		wantTok string // expected effective c.apiToken
	}{
		{domain.XUIAuthPassword, "tok-abc", ""},     // password mode → force cookie
		{domain.XUIAuthToken, "tok-abc", "tok-abc"}, // token mode → use it
		{domain.XUIAuthAuto, "tok-abc", "tok-abc"},  // auto + token present → infer Bearer
		{domain.XUIAuthAuto, "", ""},                // auto + no token → cookie
	}
	for _, tc := range cases {
		c, err := New(&domain.XUIPanel{Name: "p", URL: "https://x.example", APIToken: tc.token,
			Username: "u", Password: "pw", AuthMethod: tc.method})
		if err != nil {
			t.Fatalf("New(%q): %v", tc.method, err)
		}
		if c.apiToken != tc.wantTok {
			t.Errorf("method=%q token=%q → apiToken=%q, want %q", tc.method, tc.token, c.apiToken, tc.wantTok)
		}
	}
}

func TestCapabilitiesIncludeGranularInboundWrites(t *testing.T) {
	want := map[ports.PanelCapability]bool{
		ports.CapabilityInboundWrite:  true,
		ports.CapabilityInboundCreate: true,
		ports.CapabilityInboundUpdate: true,
		ports.CapabilityInboundDelete: true,
		ports.CapabilityInboundEnable: true,
	}
	for _, capability := range (&Client{}).Capabilities() {
		delete(want, capability)
	}
	if len(want) != 0 {
		t.Fatalf("missing capabilities: %#v", want)
	}
}

func TestReplaceSettingsClientsPreservesCurrentClients(t *testing.T) {
	next := `{"method":"2022-blake3-aes-256-gcm","clients":[]}`
	current := `{"method":"old","clients":[{"id":"a","email":"a@example.test"}]}`

	got, err := replaceSettingsClients(next, current)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Method  string `json:"method"`
		Clients []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"clients"`
	}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Method != "2022-blake3-aes-256-gcm" {
		t.Fatalf("method = %q", decoded.Method)
	}
	if len(decoded.Clients) != 1 || decoded.Clients[0].Email != "a@example.test" {
		t.Fatalf("clients not preserved: %#v", decoded.Clients)
	}
}

// Regression for the v3.5 client-wipe bug: settingsWithCurrentClients used to
// short-circuit on blank `nextSettings`, sending an empty `settings` to 3X-UI
// and wiping every live client. With normalised empties ("{}" substitution),
// passing `{}` as next must still re-merge every current client back in.
// doJSON must wrap 4xx (except auth/timeout/rate-limit) in domain.ErrValidation
// so SyncTask runners can mark the task permanently failed. Without this,
// pushing an invalid spec to 3X-UI loops every minute forever.
func TestDoJSON_4xxWrapsAsErrValidation(t *testing.T) {
	for _, code := range []int{400, 403, 404, 422} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"success":false,"msg":"bad spec"}`))
		}))
		c := &Client{baseURL: srv.URL, http: srv.Client(), apiToken: "t"}
		err := c.doJSON(context.Background(), http.MethodPost, "/panel/api/inbounds/update/1", nil, nil)
		srv.Close()
		if err == nil {
			t.Fatalf("status %d should error", code)
		}
		if !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("status %d: want errors.Is(err, ErrValidation), got %v", code, err)
		}
	}
}

// Transient 4xx (timeout, rate-limit) must NOT be wrapped — those should
// stay raw so the task runner retries them.
func TestDoJSON_TransientCodesStayRaw(t *testing.T) {
	for _, code := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		c := &Client{baseURL: srv.URL, http: srv.Client(), apiToken: "t"}
		err := c.doJSON(context.Background(), http.MethodPost, "/panel/api/inbounds/update/1", nil, nil)
		srv.Close()
		if err == nil {
			t.Fatalf("status %d should error", code)
		}
		if errors.Is(err, domain.ErrValidation) {
			t.Fatalf("status %d must NOT be wrapped as ErrValidation (it's transient)", code)
		}
	}
}

// Regression: a panel whose /login keeps succeeding but whose API path keeps
// returning 401 (reverse-proxy path-scoped auth, webBasePath mismatch, rotated
// session secret) must NOT drive unbounded re-auth recursion. Before the fix
// doJSON re-authenticated and re-called itself with no depth guard, growing the
// goroutine stack until a fatal stack overflow that recover() cannot shield —
// crashing the whole process. The retry must be bounded to exactly one re-auth
// (two API hits) and then surface a normal error.
func TestDoJSON_Persistent401CookieAuthDoesNotRecurseForever(t *testing.T) {
	var apiHits, loginHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			loginHits++
			_, _ = w.Write([]byte(`{"success":true}`))
		case "/csrf-token":
			// Cookie-mode login now fetches a CSRF token; answer it so the
			// fetch doesn't count against the API-path hit budget asserted below.
			_, _ = w.Write([]byte(`{"success":true,"obj":"csrf"}`))
		default:
			apiHits++
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	// Cookie mode: empty apiToken, username/password set so loginLocked succeeds.
	c := &Client{baseURL: srv.URL, http: srv.Client(), username: "u", password: "p"}

	err := c.doJSON(context.Background(), http.MethodGet, "/panel/api/inbounds/list", nil, nil)
	if err == nil {
		t.Fatal("persistent 401 must surface an error, not loop")
	}
	if !strings.Contains(err.Error(), "after re-authentication") {
		t.Fatalf("want a bounded re-auth error, got: %v", err)
	}
	// Exactly one retry: the original API hit + one post-re-auth hit.
	if apiHits != 2 {
		t.Fatalf("API path hit %d times, want exactly 2 (one retry only)", apiHits)
	}
	if loginHits != 2 {
		t.Fatalf("login hit %d times, want exactly 2 (initial + one re-auth)", loginHits)
	}
}

func TestReplaceSettingsClientsHandlesEmptyNext(t *testing.T) {
	current := `{"method":"aes-128-gcm","clients":[{"id":"a","email":"a@example.test"},{"id":"b","email":"b@example.test"}]}`

	got, err := replaceSettingsClients("{}", current)
	if err != nil {
		t.Fatalf("empty-next must not error after the substitution fix: %v", err)
	}
	var decoded struct {
		Clients []struct {
			Email string `json:"email"`
		} `json:"clients"`
	}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Clients) != 2 {
		t.Fatalf("blank next must not drop clients: got %d, want 2 — %s", len(decoded.Clients), got)
	}
}

// --- 3.2.0 first-class /clients/* adapter contract ---

type capturedReq struct {
	method, path, query, body, contentType string
}

// captureReq spins up a one-shot server that records the method, decoded path,
// raw query, and body of the request it receives, then replies with the given
// JSON envelope. Returns a Client wired to it.
func captureReq(t *testing.T, reply string, got *capturedReq) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.path, got.query, got.body, got.contentType = r.Method, r.URL.Path, r.URL.RawQuery, string(b), r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return &Client{baseURL: srv.URL, http: srv.Client(), apiToken: "t"}
}

func TestScanRealityTargetsRunsOnSelectedPanelWithFormPayload(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true,"obj":[{"target":"www.example.com:443","host":"www.example.com","ip":"203.0.113.10","port":443,"feasible":true,"tls13":true,"tlsVersion":"1.3","h2":true,"alpn":"h2","x25519":true,"curveID":"X25519","certValid":true,"certSubject":"example.com","certIssuer":"Example CA","notAfter":"2027-01-01T00:00:00Z","serverNames":["example.com","www.example.com"],"latencyMs":42,"reason":""}]}`, &got)

	items, err := c.ScanRealityTargets(context.Background(), "www.example.com:443,1.1.1.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != "/panel/api/server/scanRealityTargets" {
		t.Fatalf("method/path = %s %s", got.method, got.path)
	}
	if got.contentType != "application/x-www-form-urlencoded" {
		t.Fatalf("content-type = %q, want form encoding", got.contentType)
	}
	form, err := url.ParseQuery(got.body)
	if err != nil {
		t.Fatalf("parse form: %v", err)
	}
	if form.Get("targets") != "www.example.com:443,1.1.1.0/24" {
		t.Fatalf("targets = %q", form.Get("targets"))
	}
	if len(items) != 1 || !items[0].Feasible || items[0].CurveID != "X25519" || items[0].LatencyMs != 42 {
		t.Fatalf("decoded result = %#v", items)
	}
	if len(items[0].ServerNames) != 2 || items[0].ServerNames[1] != "www.example.com" {
		t.Fatalf("serverNames = %#v", items[0].ServerNames)
	}
}

func TestScanRealityTargetsEmptyInputUsesEmptyForm(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true,"obj":[]}`, &got)
	items, err := c.ScanRealityTargets(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %#v", items)
	}
	if got.body != "" || got.contentType != "application/x-www-form-urlencoded" {
		t.Fatalf("body/content-type = %q / %q", got.body, got.contentType)
	}
}

func TestAddClientPostsToClientsAdd(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true}`, &got)
	if err := c.AddClient(context.Background(), 7, ports.ClientSpec{ID: "uuid-1", Email: "u3-n9@psp.local"}); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != "/panel/api/clients/add" {
		t.Fatalf("method/path = %s %s", got.method, got.path)
	}
	var body struct {
		Client     map[string]any `json:"client"`
		InboundIDs []int          `json:"inboundIds"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, got.body)
	}
	if len(body.InboundIDs) != 1 || body.InboundIDs[0] != 7 {
		t.Fatalf("inboundIds = %#v", body.InboundIDs)
	}
	if body.Client["email"] != "u3-n9@psp.local" || body.Client["id"] != "uuid-1" {
		t.Fatalf("client = %#v", body.Client)
	}
	// tgId MUST serialize as a JSON number, not a string — 3X-UI 3.2.0 rejects
	// a string with "cannot unmarshal string into ... tgId of type int64",
	// which would fail every add/update. (Verified live against 3.2.0.)
	if _, isStr := body.Client["tgId"].(string); isStr {
		t.Fatalf("tgId must be a JSON number, not a string: %#v", body.Client["tgId"])
	}
}

func TestUpdateClientPostsToClientsUpdateByEmail(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true}`, &got)
	// old uuid in the (now vestigial) arg; new uuid rides in spec.ID under the
	// unchanged email key.
	if err := c.UpdateClient(context.Background(), 7, "old-uuid", ports.ClientSpec{ID: "new-uuid", Email: "u3-n9@psp.local", Enable: true}); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != "/panel/api/clients/update/u3-n9@psp.local" {
		t.Fatalf("method/path = %s %s", got.method, got.path)
	}
	// Body is the flat client object, NOT wrapped in {client:...} like /add.
	var client map[string]any
	if err := json.Unmarshal([]byte(got.body), &client); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, got.body)
	}
	if _, wrapped := client["client"]; wrapped {
		t.Fatalf("update body must not wrap client: %s", got.body)
	}
	if client["email"] != "u3-n9@psp.local" || client["id"] != "new-uuid" {
		t.Fatalf("client = %#v", client)
	}
}

func TestUpdateClientRequiresEmail(t *testing.T) {
	c := &Client{baseURL: "http://unused", apiToken: "t"}
	if err := c.UpdateClient(context.Background(), 7, "uuid", ports.ClientSpec{Email: ""}); err == nil {
		t.Fatal("UpdateClient with empty email must error before any HTTP call")
	}
}

func TestDelClientByEmailPostsToClientsDel(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true}`, &got)
	if err := c.DelClientByEmail(context.Background(), 7, "u3-n9@psp.local"); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != "/panel/api/clients/del/u3-n9@psp.local" {
		t.Fatalf("method/path = %s %s", got.method, got.path)
	}
	if got.query != "keepTraffic=0" {
		t.Fatalf("query = %q, want keepTraffic=0", got.query)
	}
}

// Failure path: a 200 envelope with success:false must surface as an error so
// the sync-task runner retries / marks the task failed.
func TestUpdateClientSurfacesPanelError(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":false,"msg":"client not found"}`, &got)
	err := c.UpdateClient(context.Background(), 7, "uuid", ports.ClientSpec{Email: "x@psp.local"})
	if err == nil || !strings.Contains(err.Error(), "client not found") {
		t.Fatalf("want error containing panel msg, got %v", err)
	}
}

// A permanent client-level rejection (duplicate email on add, returned as
// HTTP 200 + success:false) must be wrapped in ErrValidation so the sync-task
// runner fails fast instead of burning the full ~100-attempt retry budget.
func TestAddClientDuplicateIsErrValidation(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":false,"msg":"Duplicate email: u3-n9@psp.local"}`, &got)
	err := c.AddClient(context.Background(), 7, ports.ClientSpec{ID: "u", Email: "u3-n9@psp.local"})
	if err == nil || !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("duplicate add must wrap ErrValidation (fail-fast), got %v", err)
	}
}

// --- 3.2.x slim list / clients-get / bulk adapter contract ---

func TestListInboundsSlimHitsSlimPath(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true,"obj":[]}`, &got)
	if _, err := c.ListInboundsSlim(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodGet || got.path != "/panel/api/inbounds/list/slim" {
		t.Fatalf("method/path = %s %s, want GET /panel/api/inbounds/list/slim", got.method, got.path)
	}
}

// GetClient maps the {client:{...},inboundIds:[]} envelope: ID comes from
// client.uuid (the xray client id), NOT client.id (the numeric DB row id).
func TestGetClientParsesNestedClientUUID(t *testing.T) {
	var got capturedReq
	reply := `{"success":true,"obj":{"client":{"id":42,"uuid":"the-uuid","email":"u3-n9@psp.local","enable":true,"flow":"xtls-rprx-vision","password":"pw","auth":"au","expiryTime":111,"totalGB":222},"inboundIds":[1]}}`
	c := captureReq(t, reply, &got)
	cd, err := c.GetClient(context.Background(), "u3-n9@psp.local")
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodGet || got.path != "/panel/api/clients/get/u3-n9@psp.local" {
		t.Fatalf("method/path = %s %s", got.method, got.path)
	}
	if cd == nil {
		t.Fatal("want a client, got nil")
	}
	if cd.ID != "the-uuid" {
		t.Fatalf("ID = %q, want the uuid (not the numeric DB id)", cd.ID)
	}
	if cd.Email != "u3-n9@psp.local" || !cd.Enable || cd.Flow != "xtls-rprx-vision" ||
		cd.Password != "pw" || cd.Auth != "au" || cd.ExpiryTime != 111 || cd.TotalGB != 222 {
		t.Fatalf("client fields not mapped: %#v", *cd)
	}
}

// A missing email comes back as HTTP 200 + {success:false," (record not
// found)"}; GetClient must report it as (nil, nil), not an error.
func TestGetClientNotFoundReturnsNilNil(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":false,"msg":" (record not found)","obj":null}`, &got)
	cd, err := c.GetClient(context.Background(), "ghost@psp.local")
	if err != nil {
		t.Fatalf("not-found must be (nil,nil), got err %v", err)
	}
	if cd != nil {
		t.Fatalf("not-found must be (nil,nil), got %#v", cd)
	}
}

func TestGetClientEmptyEmailErrorsBeforeHTTP(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true}`, &got)
	if _, err := c.GetClient(context.Background(), ""); err == nil {
		t.Fatal("empty email must error before any HTTP call")
	}
	if got.method != "" {
		t.Fatalf("no HTTP call expected, got %s %s", got.method, got.path)
	}
}

// bulkDel body must be {emails,keepTraffic} (a bare array is rejected by the
// panel); keepTraffic is false so xray traffic rows are dropped.
func TestBulkDelByEmailPostsEmailsObject(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true,"obj":{"deleted":2}}`, &got)
	n, err := c.BulkDelByEmail(context.Background(), []string{"a@psp.local", "b@psp.local"})
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != "/panel/api/clients/bulkDel" {
		t.Fatalf("method/path = %s %s", got.method, got.path)
	}
	var body struct {
		Emails      []string `json:"emails"`
		KeepTraffic bool     `json:"keepTraffic"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body not JSON object: %v (%s)", err, got.body)
	}
	if len(body.Emails) != 2 || body.Emails[0] != "a@psp.local" {
		t.Fatalf("emails = %#v", body.Emails)
	}
	if body.KeepTraffic {
		t.Fatal("keepTraffic must be false (drop traffic rows)")
	}
	if n != 2 {
		t.Fatalf("deleted = %d, want 2", n)
	}
}

func TestBulkDelByEmailEmptyIsNoop(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true}`, &got)
	n, err := c.BulkDelByEmail(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.method != "" {
		t.Fatalf("empty emails must not hit the API, got %s %s", got.method, got.path)
	}
	if n != 0 {
		t.Fatalf("deleted = %d, want 0", n)
	}
}

// --- v3.9.0 multi-inbound client surface (attach/detach + multi-add) ---

// AddClientToInbounds must put EVERY id into the inboundIds array (the whole
// point — one client, many inbounds, one Xray restart).
func TestAddClientToInboundsPostsAllInboundIds(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true}`, &got)
	if err := c.AddClientToInbounds(context.Background(), []int{3, 5}, ports.ClientSpec{ID: "uuid-1", Email: "u3@psp.local"}); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != "/panel/api/clients/add" {
		t.Fatalf("method/path = %s %s", got.method, got.path)
	}
	var body struct {
		Client     map[string]any `json:"client"`
		InboundIDs []int          `json:"inboundIds"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, got.body)
	}
	if len(body.InboundIDs) != 2 || body.InboundIDs[0] != 3 || body.InboundIDs[1] != 5 {
		t.Fatalf("inboundIds = %#v, want [3 5]", body.InboundIDs)
	}
}

func TestAddClientToInboundsEmptyErrorsBeforeHTTP(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true}`, &got)
	if err := c.AddClientToInbounds(context.Background(), nil, ports.ClientSpec{Email: "u3@psp.local"}); err == nil {
		t.Fatal("empty inboundIDs must error before any HTTP call")
	}
	if got.method != "" {
		t.Fatalf("empty inboundIDs must not hit the API, got %s %s", got.method, got.path)
	}
}

func TestAttachClientPostsToAttachPath(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true}`, &got)
	if err := c.AttachClient(context.Background(), "u3@psp.local", []int{7, 9}); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != "/panel/api/clients/u3@psp.local/attach" {
		t.Fatalf("method/path = %s %s", got.method, got.path)
	}
	var body struct {
		InboundIDs []int `json:"inboundIds"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, got.body)
	}
	if len(body.InboundIDs) != 2 || body.InboundIDs[0] != 7 || body.InboundIDs[1] != 9 {
		t.Fatalf("inboundIds = %#v, want [7 9]", body.InboundIDs)
	}
}

func TestDetachClientPostsToDetachPath(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true}`, &got)
	if err := c.DetachClient(context.Background(), "u3@psp.local", []int{9}); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != "/panel/api/clients/u3@psp.local/detach" {
		t.Fatalf("method/path = %s %s", got.method, got.path)
	}
}

func TestAttachDetachEmptyInboundsIsNoop(t *testing.T) {
	for _, name := range []string{"attach", "detach"} {
		var got capturedReq
		c := captureReq(t, `{"success":true}`, &got)
		var err error
		if name == "attach" {
			err = c.AttachClient(context.Background(), "u3@psp.local", nil)
		} else {
			err = c.DetachClient(context.Background(), "u3@psp.local", nil)
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.method != "" {
			t.Fatalf("%s with empty inbounds must not hit the API, got %s %s", name, got.method, got.path)
		}
	}
}

func TestAttachDetachRequireEmail(t *testing.T) {
	c := &Client{baseURL: "http://unused", apiToken: "t"}
	if err := c.AttachClient(context.Background(), "", []int{1}); err == nil {
		t.Fatal("AttachClient with empty email must error before any HTTP call")
	}
	if err := c.DetachClient(context.Background(), "", []int{1}); err == nil {
		t.Fatal("DetachClient with empty email must error before any HTTP call")
	}
}

func TestBulkAttachPostsAndParsesResult(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true,"obj":{"attached":["a","b"],"skipped":["b"],"errors":[]}}`, &got)
	res, err := c.BulkAttach(context.Background(), []string{"a", "b"}, []int{7, 9})
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != "/panel/api/clients/bulkAttach" {
		t.Fatalf("method/path = %s %s", got.method, got.path)
	}
	var body struct {
		Emails     []string `json:"emails"`
		InboundIDs []int    `json:"inboundIds"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, got.body)
	}
	if len(body.Emails) != 2 || len(body.InboundIDs) != 2 {
		t.Fatalf("body = %#v", body)
	}
	if len(res.Done) != 2 || res.Done[0] != "a" || len(res.Skipped) != 1 || res.Skipped[0] != "b" {
		t.Fatalf("result = %#v", res)
	}
}

func TestBulkDetachParsesDetachedField(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true,"obj":{"detached":["a"],"skipped":[],"errors":[]}}`, &got)
	res, err := c.BulkDetach(context.Background(), []string{"a"}, []int{7})
	if err != nil {
		t.Fatal(err)
	}
	if got.path != "/panel/api/clients/bulkDetach" {
		t.Fatalf("path = %s", got.path)
	}
	if len(res.Done) != 1 || res.Done[0] != "a" {
		t.Fatalf("result = %#v", res)
	}
}

func TestBulkAttachEmptyIsNoop(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true}`, &got)
	// empty emails OR empty inbounds → no request
	if _, err := c.BulkAttach(context.Background(), nil, []int{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.BulkAttach(context.Background(), []string{"a"}, nil); err != nil {
		t.Fatal(err)
	}
	if got.method != "" {
		t.Fatalf("empty bulk inputs must not hit the API, got %s %s", got.method, got.path)
	}
}

// --- v3.6.4 getWebCertFiles (cert_source=from_panel) ---

// GetWebCertFiles maps the {webCertFile,webKeyFile} obj into ports.WebCertFiles.
// These are filesystem PATHS on the panel host, never the certificate bytes.
func TestGetWebCertFilesParsesPaths(t *testing.T) {
	var got capturedReq
	reply := `{"success":true,"obj":{"webCertFile":"/opt/1panel/secret/server.crt","webKeyFile":"/opt/1panel/secret/server.key"}}`
	c := captureReq(t, reply, &got)
	wc, err := c.GetWebCertFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodGet || got.path != "/panel/api/server/getWebCertFiles" {
		t.Fatalf("method/path = %s %s", got.method, got.path)
	}
	if wc == nil || wc.CertFile != "/opt/1panel/secret/server.crt" || wc.KeyFile != "/opt/1panel/secret/server.key" {
		t.Fatalf("WebCertFiles not mapped: %#v", wc)
	}
}

// A panel older than 3X-UI 3.2.7 has no getWebCertFiles route → HTTP 404.
// GetWebCertFiles must surface ports.ErrXUIEndpointUnsupported so the handler
// degrades gracefully (grey out "fetch cert from panel") instead of treating
// it as a generic validation failure.
func TestGetWebCertFiles404IsUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("404 page not found"))
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: srv.Client(), apiToken: "t"}
	_, err := c.GetWebCertFiles(context.Background())
	if err == nil {
		t.Fatal("404 must error")
	}
	if !errors.Is(err, ports.ErrXUIEndpointUnsupported) {
		t.Fatalf("want errors.Is(err, ErrXUIEndpointUnsupported), got %v", err)
	}
}

// The 404→unsupported marking lives in doJSON so any version-gated endpoint
// benefits. It must NOT disturb the existing 404→ErrValidation invariant
// (sync-task runners rely on 404 being a permanent failure), and other 4xx
// must NOT be marked endpoint-unsupported.
func TestDoJSON_404MarksEndpointUnsupportedButStaysValidation(t *testing.T) {
	srv404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv404.Close()
	c := &Client{baseURL: srv404.URL, http: srv404.Client(), apiToken: "t"}
	err := c.doJSON(context.Background(), http.MethodGet, "/panel/api/server/getWebCertFiles", nil, nil)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("404 must still be ErrValidation (permanent), got %v", err)
	}
	if !errors.Is(err, ports.ErrXUIEndpointUnsupported) {
		t.Fatalf("404 must also be ErrXUIEndpointUnsupported, got %v", err)
	}

	srv400 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv400.Close()
	c2 := &Client{baseURL: srv400.URL, http: srv400.Client(), apiToken: "t"}
	err2 := c2.doJSON(context.Background(), http.MethodGet, "/x", nil, nil)
	if errors.Is(err2, ports.ErrXUIEndpointUnsupported) {
		t.Fatalf("400 must NOT be marked endpoint-unsupported, got %v", err2)
	}
}

// --- 3X-UI 3.2.x+ cookie-mode CSRF (regression: cookie writes were silently
// broken because no X-CSRF-Token was ever sent) ---

// Cookie (username/password) mode must fetch a CSRF token after login and send
// it as X-CSRF-Token on unsafe (POST) requests. 3X-UI 3.2.x+ rejects cookie-mode
// writes without it, which silently broke every enroll/update/delete on
// username/password panels. Bearer mode is exempt (see TestBearerMode_NoCSRFFetch).
func TestCookieMode_SendsCSRFTokenOnWrites(t *testing.T) {
	var gotCSRF string
	var csrfFetched, loginHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			loginHits++
			_, _ = w.Write([]byte(`{"success":true}`))
		case "/csrf-token":
			csrfFetched++
			_, _ = w.Write([]byte(`{"success":true,"obj":"TESTCSRF123"}`))
		default:
			gotCSRF = r.Header.Get("X-CSRF-Token")
			_, _ = w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: srv.Client(), username: "u", password: "p"}

	if err := c.doJSON(context.Background(), http.MethodPost, "/panel/api/clients/add", map[string]any{"x": 1}, nil); err != nil {
		t.Fatalf("cookie-mode write errored: %v", err)
	}
	if csrfFetched != 1 {
		t.Fatalf("csrf-token fetched %d times, want 1", csrfFetched)
	}
	if gotCSRF != "TESTCSRF123" {
		t.Fatalf("X-CSRF-Token on write = %q, want TESTCSRF123", gotCSRF)
	}
	if loginHits != 1 {
		t.Fatalf("login hit %d times, want 1 (CSRF fetch must not re-login)", loginHits)
	}
}

// Safe methods (GET) must NOT carry X-CSRF-Token — the token is scoped to
// unsafe methods, matching the panel's CSRF middleware.
func TestCookieMode_NoCSRFHeaderOnReads(t *testing.T) {
	var gotCSRF string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"success":true}`))
		case "/csrf-token":
			_, _ = w.Write([]byte(`{"success":true,"obj":"TESTCSRF123"}`))
		default:
			gotCSRF = r.Header.Get("X-CSRF-Token")
			_, _ = w.Write([]byte(`{"success":true,"obj":[]}`))
		}
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: srv.Client(), username: "u", password: "p"}
	var out []rawInbound
	if err := c.doJSON(context.Background(), http.MethodGet, "/panel/api/inbounds/list", nil, &out); err != nil {
		t.Fatalf("cookie-mode read errored: %v", err)
	}
	if gotCSRF != "" {
		t.Fatalf("GET must not carry X-CSRF-Token, got %q", gotCSRF)
	}
}

// Bearer (API token) mode must not touch /csrf-token at all and must not send
// X-CSRF-Token — the panel short-circuits CSRF for token auth. Guards against
// the CSRF fix leaking into the common token path.
func TestBearerMode_NoCSRFFetch(t *testing.T) {
	var csrfFetched int
	var gotCSRF, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/csrf-token":
			csrfFetched++
			_, _ = w.Write([]byte(`{"success":true,"obj":"x"}`))
		default:
			gotCSRF = r.Header.Get("X-CSRF-Token")
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"success":true}`))
		}
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: srv.Client(), apiToken: "tok"}
	if err := c.doJSON(context.Background(), http.MethodPost, "/panel/api/clients/add", map[string]any{"x": 1}, nil); err != nil {
		t.Fatalf("bearer write errored: %v", err)
	}
	if csrfFetched != 0 {
		t.Fatalf("bearer mode must not fetch csrf-token, fetched %d", csrfFetched)
	}
	if gotCSRF != "" {
		t.Fatalf("bearer mode must not send X-CSRF-Token, got %q", gotCSRF)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("Authorization = %q, want 'Bearer tok'", gotAuth)
	}
}

// A cookie-mode write rejected with 403 (stale/missing CSRF after a session
// rotation) must re-login, re-fetch the CSRF token, and retry exactly once.
// Mirrors the bounded 401 re-auth so a persistent 403 can't recurse.
func TestCookieMode_403RefetchesCSRFAndRetriesOnce(t *testing.T) {
	var apiHits, csrfFetched, loginHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			loginHits++
			_, _ = w.Write([]byte(`{"success":true}`))
		case "/csrf-token":
			csrfFetched++
			_, _ = w.Write([]byte(`{"success":true,"obj":"TESTCSRF123"}`))
		default:
			apiHits++
			if apiHits == 1 {
				w.WriteHeader(http.StatusForbidden) // first write: CSRF rejected
				return
			}
			_, _ = w.Write([]byte(`{"success":true}`)) // retry succeeds
		}
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, http: srv.Client(), username: "u", password: "p"}

	if err := c.doJSON(context.Background(), http.MethodPost, "/panel/api/clients/add", map[string]any{"x": 1}, nil); err != nil {
		t.Fatalf("403 retry should recover, got: %v", err)
	}
	if apiHits != 2 {
		t.Fatalf("API path hit %d times, want 2 (one retry)", apiHits)
	}
	if loginHits != 2 {
		t.Fatalf("login hit %d times, want 2 (initial + re-auth)", loginHits)
	}
	if csrfFetched != 2 {
		t.Fatalf("csrf-token fetched %d times, want 2 (initial + re-fetch on 403)", csrfFetched)
	}
}

// BulkCreateClients posts a JSON ARRAY of {client, inboundIds} to /clients/bulkCreate
// (one Xray restart for the whole fan-out) and parses obj.created.
func TestBulkCreateClients(t *testing.T) {
	var gotBody []byte
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"success":true,"msg":"","obj":{"created":2}}`))
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, apiToken: "t", http: srv.Client()}

	res, err := c.BulkCreateClients(context.Background(), []ports.BulkCreateClientItem{
		{Spec: ports.ClientSpec{ID: "uuid-a", Email: "a@psp.local", Enable: true}, InboundIDs: []int{7}},
		{Spec: ports.ClientSpec{ID: "uuid-b", Email: "b@psp.local", Enable: true, Password: "pw"}, InboundIDs: []int{7, 9}},
	})
	if err != nil {
		t.Fatalf("BulkCreateClients: %v", err)
	}
	if res.Created != 2 {
		t.Fatalf("Created = %d, want 2", res.Created)
	}
	if gotPath != "/panel/api/clients/bulkCreate" {
		t.Fatalf("path = %q", gotPath)
	}
	// Body must be a top-level JSON array of {client, inboundIds} items.
	var items []struct {
		Client     map[string]any `json:"client"`
		InboundIDs []int          `json:"inboundIds"`
	}
	if err := json.Unmarshal(gotBody, &items); err != nil {
		t.Fatalf("body is not a JSON array: %v (%s)", err, gotBody)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].Client["email"] != "a@psp.local" || len(items[0].InboundIDs) != 1 || items[0].InboundIDs[0] != 7 {
		t.Fatalf("item0 = %+v", items[0])
	}
	if items[1].Client["email"] != "b@psp.local" || len(items[1].InboundIDs) != 2 {
		t.Fatalf("item1 = %+v", items[1])
	}
}

// Empty / no-target input is a no-op (no HTTP call, no error).
func TestBulkCreateClients_Empty(t *testing.T) {
	c := &Client{baseURL: "http://unused", apiToken: "t"}
	if res, err := c.BulkCreateClients(context.Background(), nil); err != nil || res.Created != 0 {
		t.Fatalf("nil items must no-op, got res=%+v err=%v", res, err)
	}
	if res, err := c.BulkCreateClients(context.Background(), []ports.BulkCreateClientItem{
		{Spec: ports.ClientSpec{Email: "x@psp.local"}, InboundIDs: nil},
	}); err != nil || res.Created != 0 {
		t.Fatalf("no-target item must be dropped (no-op), got res=%+v err=%v", res, err)
	}
}
