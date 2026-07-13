package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

type contextTestAPIHandler struct{}

func (contextTestAPIHandler) HandlerType() string { return "claude" }
func (contextTestAPIHandler) Models() []map[string]any {
	return nil
}

func TestGetContextWithCancelPreservesRequestRoutingValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	request := httptest.NewRequest(http.MethodPost, "/claude-acc-pool/v1/messages", nil)
	selectedID := ""
	requestCtx := WithPoolScope(request.Context(), coreexecutor.PoolScopeClaudeAccountPool)
	requestCtx = WithPinnedAuthID(requestCtx, "account-auth")
	requestCtx = WithExecutionSessionID(requestCtx, "execution-session")
	requestCtx = WithSelectedAuthIDCallback(requestCtx, func(id string) { selectedID = id })
	requestCtx = WithDisallowFreeAuth(requestCtx)
	ginCtx.Request = request.WithContext(requestCtx)

	base := &BaseAPIHandler{Cfg: &sdkconfig.SDKConfig{}}
	got, cancel := base.GetContextWithCancel(contextTestAPIHandler{}, ginCtx, context.Background())
	defer cancel()

	if scope := poolScopeFromContext(got); scope != coreexecutor.PoolScopeClaudeAccountPool {
		t.Fatalf("pool scope = %q", scope)
	}
	if authID := pinnedAuthIDFromContext(got); authID != "account-auth" {
		t.Fatalf("pinned auth id = %q", authID)
	}
	if sessionID := executionSessionIDFromContext(got); sessionID != "execution-session" {
		t.Fatalf("execution session id = %q", sessionID)
	}
	if !disallowFreeAuthFromContext(got) {
		t.Fatal("disallow-free-auth marker was lost")
	}
	callback := selectedAuthIDCallbackFromContext(got)
	if callback == nil {
		t.Fatal("selected-auth callback was lost")
	}
	callback("selected-account")
	if selectedID != "selected-account" {
		t.Fatalf("selected auth callback received %q", selectedID)
	}
	metadata := requestExecutionMetadata(got)
	if metadata[coreexecutor.PoolScopeMetadataKey] != coreexecutor.PoolScopeClaudeAccountPool {
		t.Fatalf("execution metadata = %#v", metadata)
	}
}
