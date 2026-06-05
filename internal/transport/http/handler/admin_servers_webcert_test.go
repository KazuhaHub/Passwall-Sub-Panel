package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// fakeWebCertClient stubs only GetWebCertFiles; the rest of ports.XUIClient is
// the embedded nil interface since WebCert never calls the other methods.
type fakeWebCertClient struct {
	ports.XUIClient
	wc  *ports.WebCertFiles
	err error
}

func (c fakeWebCertClient) GetWebCertFiles(context.Context) (*ports.WebCertFiles, error) {
	return c.wc, c.err
}

type fakeWebCertPool struct {
	client ports.XUIClient
	getErr error
}

func (p fakeWebCertPool) Get(int64) (ports.XUIClient, error) { return p.client, p.getErr }
func (fakeWebCertPool) List() []*domain.XUIPanel             { return nil }
func (fakeWebCertPool) Add(*domain.XUIPanel) error           { return nil }
func (fakeWebCertPool) Remove(int64) error                   { return nil }

func webCertCtx(rec *httptest.ResponseRecorder, id string) *gin.Context {
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = httptest.NewRequest(http.MethodGet, "/admin/servers/"+id+"/web-cert", nil)
	return c
}

// On a 3.2.7+ panel the cert paths come back and supported is true.
func TestWebCertReturnsPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AdminServersHandler{pool: fakeWebCertPool{client: fakeWebCertClient{
		wc: &ports.WebCertFiles{CertFile: "/opt/1panel/secret/server.crt", KeyFile: "/opt/1panel/secret/server.key"},
	}}}
	rec := httptest.NewRecorder()
	h.WebCert(webCertCtx(rec, "5"))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Supported bool   `json:"supported"`
		CertFile  string `json:"cert_file"`
		KeyFile   string `json:"key_file"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Supported || resp.CertFile != "/opt/1panel/secret/server.crt" || resp.KeyFile != "/opt/1panel/secret/server.key" {
		t.Fatalf("resp = %#v", resp)
	}
}

// A panel older than 3X-UI 3.2.7 (ErrXUIEndpointUnsupported) must degrade to
// HTTP 200 + {"supported":false} — NOT a 4xx/5xx — so the node form greys out
// the "fetch from panel" button without firing the global error toast.
func TestWebCertUnsupportedDegradesTo200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AdminServersHandler{pool: fakeWebCertPool{client: fakeWebCertClient{
		err: fmt.Errorf("getWebCertFiles: %w", ports.ErrXUIEndpointUnsupported),
	}}}
	rec := httptest.NewRecorder()
	h.WebCert(webCertCtx(rec, "5"))
	if rec.Code != http.StatusOK {
		t.Fatalf("unsupported must be 200 (no error toast), got %d", rec.Code)
	}
	var resp struct {
		Supported bool `json:"supported"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Supported {
		t.Fatal("supported must be false for a pre-3.2.7 panel")
	}
}

// A genuine upstream failure (panel unreachable) must surface as a non-2xx so
// the normal error path applies — it must NOT be silently swallowed as
// supported:false.
func TestWebCertRealErrorIsNon2xx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AdminServersHandler{pool: fakeWebCertPool{client: fakeWebCertClient{
		err: fmt.Errorf("connection refused"),
	}}}
	rec := httptest.NewRecorder()
	h.WebCert(webCertCtx(rec, "5"))
	if rec.Code/100 == 2 {
		t.Fatalf("a real error must be non-2xx, got %d", rec.Code)
	}
}
