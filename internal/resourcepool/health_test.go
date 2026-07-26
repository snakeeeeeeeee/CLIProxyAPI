package resourcepool

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyHealthUsesSingleIPifyRequest(t *testing.T) {
	var calls atomic.Int32
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.String() != "http://api.ipify.test/?format=json" {
			t.Errorf("target URL = %q", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ip":"203.0.113.42"}`))
	}))
	defer proxyServer.Close()

	previousURL := proxyHealthRequestURL
	proxyHealthRequestURL = "http://api.ipify.test/?format=json"
	t.Cleanup(func() { proxyHealthRequestURL = previousURL })

	ok, _, exitIP, err := testProxyExitIP(context.Background(), ProxyResource{
		ProxyURL: proxyServer.URL,
	}, EffectiveProxyHealthConfig{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("testProxyExitIP() error = %v", err)
	}
	if !ok || exitIP != "203.0.113.42" {
		t.Fatalf("result ok=%v exit_ip=%q", ok, exitIP)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("proxy request count = %d, want 1", got)
	}
}

func TestProxyHealthRejectsInvalidIPifyResponses(t *testing.T) {
	for _, test := range []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "non success", statusCode: http.StatusBadGateway, body: `{"ip":"203.0.113.42"}`},
		{name: "invalid JSON", statusCode: http.StatusOK, body: `{`},
		{name: "invalid IP", statusCode: http.StatusOK, body: `{"ip":"not-an-ip"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.body))
			}))
			defer proxyServer.Close()

			previousURL := proxyHealthRequestURL
			proxyHealthRequestURL = "http://api.ipify.test/?format=json"
			t.Cleanup(func() { proxyHealthRequestURL = previousURL })

			ok, _, exitIP, err := testProxyExitIP(context.Background(), ProxyResource{
				ProxyURL: proxyServer.URL,
			}, EffectiveProxyHealthConfig{Timeout: 2 * time.Second})
			if err == nil {
				t.Fatal("testProxyExitIP() error = nil")
			}
			if ok || exitIP != "" {
				t.Fatalf("result ok=%v exit_ip=%q", ok, exitIP)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("proxy request count = %d, want 1", got)
			}
		})
	}
}

func TestUpdateProxyHealthRecordsExitChange(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	proxy, err := store.CreateProxy(ctx, ProxyResourceSeed{
		Name:     "exit-change",
		ProxyURL: "http://127.0.0.1:18080",
	})
	if err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}
	first, err := store.UpdateProxyHealthWithExitIP(ctx, proxy.ID, true, 20*time.Millisecond, "203.0.113.10", nil, 1)
	if err != nil {
		t.Fatalf("first UpdateProxyHealthWithExitIP() error = %v", err)
	}
	if first.ExitChanged {
		t.Fatal("first observed exit IP should not be marked changed")
	}
	second, err := store.UpdateProxyHealthWithExitIP(ctx, proxy.ID, true, 25*time.Millisecond, "203.0.113.11", nil, 1)
	if err != nil {
		t.Fatalf("second UpdateProxyHealthWithExitIP() error = %v", err)
	}
	if !second.ExitChanged || second.ExitIP != "203.0.113.11" {
		t.Fatalf("second health result = %+v", second)
	}
	var events int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pool_events WHERE type = 'proxy_exit_changed'`).Scan(&events); err != nil {
		t.Fatalf("count proxy exit events: %v", err)
	}
	if events != 1 {
		t.Fatalf("proxy exit event count = %d, want 1", events)
	}
	var dataJSON string
	if err := store.db.QueryRowContext(ctx, `SELECT data_json FROM pool_events WHERE type = 'proxy_exit_changed' LIMIT 1`).Scan(&dataJSON); err != nil {
		t.Fatalf("read proxy exit event: %v", err)
	}
	if dataJSON != fmt.Sprintf(`{"proxy_id":%q}`, proxy.ID) {
		t.Fatalf("proxy exit event data = %s", dataJSON)
	}
}
