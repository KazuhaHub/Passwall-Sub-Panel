package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Health is the liveness probe used by load balancers and operators.
func Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
