package handler

import (
	"context"
	"errors"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDestinationStatusUnwiredAndStorageFailuresAreSafe(t *testing.T) {
	for _, read := range []func(context.Context) (destpolicy.DestinationStatus, error){nil, func(context.Context) (destpolicy.DestinationStatus, error) {
		return destpolicy.DestinationStatus{}, errors.Join(domain.ErrUnavailable, errors.New("private-status-storage-marker"))
	}} {
		r := gin.New()
		r.GET("/status", NewAdminDestinationStatusHandler(read).Get)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/status", nil))
		if w.Code != 503 || strings.Contains(w.Body.String(), "private-status-storage-marker") {
			t.Fatal("status failure missing safe unavailable response")
		}
	}
}
