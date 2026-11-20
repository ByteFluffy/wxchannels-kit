package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLocalPolicyBlocksAdministrativeMutationsBeforeHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, request := range []struct{ method, path string }{
		{http.MethodPost, "/api/proxy/system/enable"}, {http.MethodPost, "/api/proxy/certificate/install"},
		{http.MethodPost, "/api/proxy/certificate/uninstall_by_name"}, {http.MethodPost, "/api/service/start"},
		{http.MethodPost, "/api/config"}, {http.MethodGet, "/api/update/check"},
	} {
		engine := gin.New()
		engine.Use(localDistributionPolicy)
		called := false
		engine.Handle(request.method, request.path, func(ctx *gin.Context) { called = true; ctx.Status(http.StatusOK) })
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(request.method, request.path, nil))
		if response.Code != http.StatusForbidden || called {
			t.Fatalf("unsafe request reached handler: %s %s / %d", request.method, request.path, response.Code)
		}
	}
}

func TestLocalPolicyAllowsNativeTaskCreation(t *testing.T) {
	engine := gin.New()
	engine.Use(localDistributionPolicy)
	engine.POST("/api/v1/download_task/create", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/download_task/create", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("native task creation was blocked: %d", response.Code)
	}
}
