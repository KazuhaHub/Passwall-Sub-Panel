package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/gin-gonic/gin"
)

// A template added twice and a name already taken are different answers: the
// chooser says "already added" only for the first.
func TestDestError_TemplateExistsIsNotANameCollision(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		err  error
		code string
	}{
		{fmt.Errorf("%w: template %q was already added", domain.ErrDestTemplateExists, "pt"), "dest_template_exists"},
		{fmt.Errorf("%w: a policy with this name exists", domain.ErrAlreadyExists), "dest_name_taken"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		(&AdminDestHandler{}).destError(c, tc.err, errCodes{exists: "dest_name_taken"})
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != http.StatusConflict || body.Code != tc.code {
			t.Fatalf("%v → %d %q, want 409 %q", tc.err, w.Code, body.Code, tc.code)
		}
	}
}

// resolved_ips tells three things apart: omitted (the answer for a hostname is
// conditional), an empty list (a failed resolution) and a list.
func TestDestTestRequest_ResolvedIPsKeepsOmittedApartFromEmpty(t *testing.T) {
	for body, want := range map[string]struct {
		nilList bool
		n       int
	}{
		`{"target":"a"}`:                                   {true, 0},
		`{"target":"a","resolved_ips":null}`:               {true, 0},
		`{"target":"a","resolved_ips":[]}`:                 {false, 0},
		`{"target":"a","resolved_ips":["10.0.0.1","::1"]}`: {false, 2},
	} {
		var req destTestRequest
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		in := req.toTestInput(0)
		if (in.ResolvedIPs == nil) != want.nilList || len(in.ResolvedIPs) != want.n {
			t.Errorf("%s: resolved = %#v", body, in.ResolvedIPs)
		}
	}
}
