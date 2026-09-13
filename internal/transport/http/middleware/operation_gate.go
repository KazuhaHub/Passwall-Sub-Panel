package middleware

import (
	"net/http"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/operationgate"
	"github.com/gin-gonic/gin"
)

// Only the ticket-authenticated completion route acquires exclusive admission.
// Every ordinary request remains admitted until all synchronous writes finish.
func BackendOperationGate(gate *operationgate.Gate) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodPost && c.Request.URL.Path == "/api/node-bootstrap/complete" {
			c.Next()
			return
		}
		ctx, release, err := gate.Read(c.Request.Context())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "backend operation is temporarily unavailable"})
			return
		}
		defer release()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
