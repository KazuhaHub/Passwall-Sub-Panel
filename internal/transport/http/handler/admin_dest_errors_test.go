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
