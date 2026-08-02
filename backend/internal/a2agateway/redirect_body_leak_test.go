package a2agateway_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"actweave/backend/internal/a2agateway"
)

// TestSecureHTTPClient_307CrossOriginNotAllowlisted_NoPOSTBodyLeak proves item 17:
// 307/308 redirect to a host outside the explicit allowlist must not be followed,
// so the POST body never reaches the unlisted origin.
func TestSecureHTTPClient_307CrossOriginNotAllowlisted_NoPOSTBodyLeak(t *testing.T) {
	t.Parallel()
	var evilHits atomic.Int64
	var evilBody atomic.Value
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evilHits.Add(1)
		b, _ := io.ReadAll(r.Body)
		evilBody.Store(string(b))
		w.WriteHeader(http.StatusOK)
	}))
	defer evil.Close()

	// Good origin (allowlisted loopback) issues 307/308 to evil (also loopback but we
	// allowlist only a hostname that evil's IP is NOT registered under via explicit host check).
	// Since both are 127.0.0.1, use Location with a public host not on allowlist instead.
	good307 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Unlisted host — CheckRedirect must reject before dialing.
		http.Redirect(w, r, "https://not-allowlisted.example/steal", http.StatusTemporaryRedirect)
	}))
	defer good307.Close()
	good308 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://not-allowlisted.example/steal", http.StatusPermanentRedirect)
	}))
	defer good308.Close()

	secretBody := []byte(`{"jsonrpc":"2.0","method":"message/send","params":{"secret":"POST-BODY-LEAK-TEST"}}`)
	client := a2agateway.SecureHTTPClient(3*time.Second, []string{"127.0.0.1"}, a2agateway.EgressPolicy{AllowHTTP: true})

	for _, tc := range []struct {
		name string
		url  string
	}{
		{"307", good307.URL + "/start"},
		{"308", good308.URL + "/start"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, tc.url, bytes.NewReader(secretBody))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer must-not-leak")
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
				t.Fatal("expected redirect to non-allowlisted host to fail")
			}
			if !strings.Contains(err.Error(), "ssrf") && !strings.Contains(err.Error(), "allowlist") &&
				!strings.Contains(err.Error(), "denied") {
				t.Fatalf("want SSRF/allowlist error, got %v", err)
			}
		})
	}
	if evilHits.Load() != 0 {
		t.Fatalf("evil origin hits=%d body=%v (POST body leaked)", evilHits.Load(), evilBody.Load())
	}
}

// TestSecureHTTPClient_307AllowlistedCrossOrigin_StripsCredentials keeps following
// only when target is allowlisted, and strips Authorization on cross-origin hop.
func TestSecureHTTPClient_307AllowlistedCrossOrigin_StripsCredentials(t *testing.T) {
	t.Parallel()
	var hop2Auth atomic.Value
	var hop2Body atomic.Value
	hop2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hop2Auth.Store(r.Header.Get("Authorization"))
		b, _ := io.ReadAll(r.Body)
		hop2Body.Store(string(b))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer hop2.Close()
	hop1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, hop2.URL+"/next", http.StatusTemporaryRedirect)
	}))
	defer hop1.Close()

	client := a2agateway.SecureHTTPClient(3*time.Second, []string{"127.0.0.1"}, a2agateway.EgressPolicy{AllowHTTP: true})
	body := []byte(`{"task":"allowed-redirect-body"}`)
	req, _ := http.NewRequest(http.MethodPost, hop1.URL+"/start", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if auth, _ := hop2Auth.Load().(string); auth != "" {
		t.Fatalf("cross-origin hop received Authorization=%q", auth)
	}
	// Body may be re-sent on 307 (method preserved) only to allowlisted target — that is OK.
	// Credentials must never follow.
	gotBody, _ := hop2Body.Load().(string)
	if !strings.Contains(gotBody, "allowed-redirect-body") {
		t.Logf("note: 307 body re-POST to allowlisted hop body=%q", gotBody)
	}
}
