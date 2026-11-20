package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// localDistributionPolicy keeps legacy browser/admin endpoints from bypassing
// explicit CLI setup. Capture/trust settings and source updates are managed at
// process startup, not by an unauthenticated request or an MCP tool.
func localDistributionPolicy(ctx *gin.Context) {
	path := ctx.Request.URL.Path
	readOnly := ctx.Request.Method == http.MethodGet || ctx.Request.Method == http.MethodHead
	if strings.HasPrefix(path, "/api/update/") || (!readOnly && (path == "/api/config" || strings.HasPrefix(path, "/api/proxy/") || strings.HasPrefix(path, "/api/service/"))) {
		ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "msg": "administrative mutations are disabled in the local distribution; use explicit wxc startup/configuration", "data": nil})
		return
	}
	ctx.Next()
}
