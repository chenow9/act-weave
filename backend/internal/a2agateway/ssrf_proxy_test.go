package a2agateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestSecureHTTPClient_IgnoresEnvProxy proves HTTPS_PROXY/HTTP_PROXY cannot hijack
// A2A egress (dial-time DNS pin must hit the real target, not a proxy).
func TestSecureHTTPClient_IgnoresEnvProxy(t *testing.T) {
	// Not parallel: mutates process proxy env via t.Setenv.
	// Fake proxy that would intercept if ProxyFromEnvironment were used.
	var proxyHits int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits++
		http.Error(w, "proxy should not be used", http.StatusBadGateway)
	}))
	defer proxy.Close()

	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("https_proxy", proxy.URL)
	_ = os.Setenv("NO_PROXY", "")
	_ = os.Setenv("no_proxy", "")

	// Target server (loopback allowed with AllowHTTP).
	var targetHits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer target.Close()

	client := SecureHTTPClient(3*time.Second, []string{"127.0.0.1"}, EgressPolicy{AllowHTTP: true})
	if client.Transport == nil {
		t.Fatal("transport nil")
	}
	// Transport must not use environment proxy.
	if tr, ok := client.Transport.(*http.Transport); ok {
		if tr.Proxy != nil {
			// Even if Proxy is set, it must not be ProxyFromEnvironment for this client.
			// SecureHTTPClient sets Proxy: nil.
			req, _ := http.NewRequest(http.MethodGet, target.URL, nil)
			if u, err := tr.Proxy(req); err == nil && u != nil {
				t.Fatalf("SecureHTTPClient must not proxy via env; got %v", u)
			}
		}
	}

	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("direct dial failed: %v", err)
	}
	_ = resp.Body.Close()
	if targetHits != 1 {
		t.Fatalf("target hits=%d want 1", targetHits)
	}
	if proxyHits != 0 {
		t.Fatalf("proxy hits=%d want 0 (env proxy must not intercept)", proxyHits)
	}
}
