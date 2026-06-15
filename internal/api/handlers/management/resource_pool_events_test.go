package management

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/resourcepool"
)

func TestResourcePoolEventsAllowsQueryKeyAndStreamsEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")

	handler := NewHandler(&config.Config{}, "", nil)
	router := gin.New()
	mgmt := router.Group("/v0/management")
	mgmt.Use(handler.Middleware())
	mgmt.GET("/resource-pools/events", handler.StreamResourcePoolEvents)

	server := httptest.NewServer(router)
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/v0/management/resource-pools/events?key=test-management-key", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("events request error = %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("content type = %q, want text/event-stream", contentType)
	}

	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	if !waitForLine(t, lines, "event: connected") {
		t.Fatalf("connected event not received")
	}

	resourcepool.PublishProxyChanged("proxy-test", "unit")
	if !waitForLine(t, lines, "event: proxy_changed") {
		t.Fatalf("proxy_changed event not received")
	}
}

func waitForLine(t *testing.T, lines <-chan string, want string) bool {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				return false
			}
			if strings.Contains(line, want) {
				return true
			}
		case <-timeout:
			return false
		}
	}
}
