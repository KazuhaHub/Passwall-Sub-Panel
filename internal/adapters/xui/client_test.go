package xui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

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
		if r.URL.Path == "/login" {
			loginHits++
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}
		apiHits++
		w.WriteHeader(http.StatusUnauthorized)
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
	method, path, query, body string
}

// captureReq spins up a one-shot server that records the method, decoded path,
// raw query, and body of the request it receives, then replies with the given
// JSON envelope. Returns a Client wired to it.
func captureReq(t *testing.T, reply string, got *capturedReq) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.path, got.query, got.body = r.Method, r.URL.Path, r.URL.RawQuery, string(b)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return &Client{baseURL: srv.URL, http: srv.Client(), apiToken: "t"}
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

// bulkCreate body is a JSON array of {client,inboundIds}; the result parses
// created + skipped[{email,reason}].
func TestBulkAddToInboundPostsArrayAndParsesResult(t *testing.T) {
	var got capturedReq
	reply := `{"success":true,"obj":{"created":1,"skipped":[{"email":"dup@psp.local","reason":"email already in use: dup@psp.local"}]}}`
	c := captureReq(t, reply, &got)
	res, err := c.BulkAddToInbound(context.Background(), 7, []ports.ClientSpec{
		{ID: "uuid-1", Email: "new@psp.local"},
		{ID: "uuid-2", Email: "dup@psp.local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != "/panel/api/clients/bulkCreate" {
		t.Fatalf("method/path = %s %s", got.method, got.path)
	}
	var items []struct {
		Client     map[string]any `json:"client"`
		InboundIDs []int          `json:"inboundIds"`
	}
	if err := json.Unmarshal([]byte(got.body), &items); err != nil {
		t.Fatalf("body is not a JSON array: %v (%s)", err, got.body)
	}
	if len(items) != 2 || len(items[0].InboundIDs) != 1 || items[0].InboundIDs[0] != 7 {
		t.Fatalf("items = %#v", items)
	}
	if items[0].Client["email"] != "new@psp.local" {
		t.Fatalf("item[0] client = %#v", items[0].Client)
	}
	if res.Created != 1 {
		t.Fatalf("Created = %d, want 1", res.Created)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Email != "dup@psp.local" ||
		!strings.Contains(res.Skipped[0].Reason, "already in use") {
		t.Fatalf("Skipped = %#v", res.Skipped)
	}
}

func TestBulkAddToInboundEmptyIsNoop(t *testing.T) {
	var got capturedReq
	c := captureReq(t, `{"success":true}`, &got)
	res, err := c.BulkAddToInbound(context.Background(), 7, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.method != "" {
		t.Fatalf("empty specs must not hit the API, got %s %s", got.method, got.path)
	}
	if res.Created != 0 || len(res.Skipped) != 0 {
		t.Fatalf("want zero result, got %#v", res)
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
