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
