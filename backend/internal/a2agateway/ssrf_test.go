package a2agateway

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateOutboundURL_SSRF(t *testing.T) {
	t.Parallel()
	if err := ValidateOutboundURL("https://evil.example/x", []string{"good.example"}); err == nil {
		t.Fatal("expected deny non-allowlisted")
	}
	// Literal public IP is allowed when host is in allowlist.
	if err := ValidateOutboundURL("https://1.1.1.1/a2a", []string{"1.1.1.1"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOutboundURL("http://127.0.0.1/meta", nil); err == nil {
		t.Fatal("expected deny loopback without allowlist")
	}
	if err := ValidateOutboundURL("https://user:pass@good.example/", []string{"good.example"}); err == nil {
		t.Fatal("userinfo denied")
	}
	if err := ValidateOutboundURL("file:///etc/passwd", nil); err == nil {
		t.Fatal("scheme")
	}
	// Production rejects plain http even for public hosts.
	if err := ValidateOutboundURLCtx(context.Background(), "http://good.example/a2a", []string{"good.example"}, EgressPolicy{}); err == nil {
		t.Fatal("http denied in production policy")
	}
	// Loopback http only with AllowHTTP.
	if err := ValidateOutboundURLCtx(context.Background(), "http://127.0.0.1/a2a", nil, EgressPolicy{AllowHTTP: true}); err != nil {
		t.Fatal(err)
	}
	// Private literal IP denied.
	if err := ValidateOutboundURL("https://10.0.0.5/x", []string{"10.0.0.5"}); err == nil {
		t.Fatal("private IP denied")
	}
	// Link-local / metadata denied.
	if err := ValidateOutboundURL("https://169.254.169.254/latest", []string{"169.254.169.254"}); err == nil {
		t.Fatal("metadata IP denied")
	}
}

type fixedResolver struct {
	addrs []net.IPAddr
	err   error
}

func (f fixedResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return f.addrs, f.err
}

func TestValidateOutboundURL_DNSPrivateBlocked(t *testing.T) {
	t.Parallel()
	policy := EgressPolicy{Resolver: fixedResolver{addrs: []net.IPAddr{{IP: net.ParseIP("10.1.2.3")}}}}
	err := ValidateOutboundURLCtx(context.Background(), "https://evil.internal/a2a", []string{"evil.internal"}, policy)
	if err == nil {
		t.Fatal("expected deny DNS→private")
	}
	policy2 := EgressPolicy{Resolver: fixedResolver{addrs: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}}}
	if err := ValidateOutboundURLCtx(context.Background(), "https://good.example/a2a", []string{"good.example"}, policy2); err != nil {
		t.Fatal(err)
	}
}

// rebindingResolver returns public IP first, then private — classic DNS rebinding.
type rebindingResolver struct {
	n int
}

func (r *rebindingResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	r.n++
	if r.n == 1 {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	return []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}}, nil
}

func TestSecureHTTPClient_DialTimeRebindingBlocked(t *testing.T) {
	t.Parallel()
	res := &rebindingResolver{}
	policy := EgressPolicy{Resolver: res}
	// First validate (public) succeeds.
	if err := ValidateOutboundURLCtx(context.Background(), "https://flip.example/a2a", []string{"flip.example"}, policy); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	// Dial-time re-resolve hits private IP and must fail.
	client := SecureHTTPClient(2*time.Second, []string{"flip.example"}, policy)
	if !IsSecureHTTPClient(client) {
		t.Fatal("expected SecureHTTPClient marker")
	}
	// Force dial via underlying pinned transport DialContext
	pt, ok := client.Transport.(*pinnedTransport)
	if !ok || pt.inner == nil || pt.inner.DialContext == nil {
		t.Fatal("expected pinned DialContext")
	}
	_, err := pt.inner.DialContext(context.Background(), "tcp", "flip.example:443")
	if err == nil {
		t.Fatal("expected dial-time rebinding deny")
	}
	if !strings.Contains(err.Error(), "rebinding") && !strings.Contains(err.Error(), "private") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestNewOutboundTool_RejectsDefaultTransport(t *testing.T) {
	t.Parallel()
	audit := &memInboundAudit{}
	_, err := NewOutboundTool(OutboundConfig{
		Binding: RemoteBinding{
			CallableName: "r", EndpointURL: "https://1.1.1.1/a2a",
			AllowedHosts: []string{"1.1.1.1"}, Version: 1,
		},
		Audit:      audit,
		HTTPClient: &http.Client{Transport: http.DefaultTransport},
	})
	if err == nil {
		t.Fatal("expected reject DefaultTransport")
	}
}

func TestNewOutboundTool_AllowHTTP_LoopbackEndpointAccepted(t *testing.T) {
	t.Parallel()
	audit := &memInboundAudit{}
	// Construction with AllowHTTP + loopback http must succeed (InvokableRun uses same policy).
	tool, err := NewOutboundTool(OutboundConfig{
		Binding: RemoteBinding{
			CallableName: "loop", EndpointURL: "http://127.0.0.1:9/a2a",
			AllowedHosts: []string{"127.0.0.1"}, Version: 1,
		},
		Audit:     audit,
		AllowHTTP: true,
	})
	if err != nil {
		t.Fatalf("AllowHTTP loopback construct: %v", err)
	}
	if tool == nil {
		t.Fatal("nil tool")
	}
	// Without AllowHTTP, same URL is rejected at construct.
	_, err = NewOutboundTool(OutboundConfig{
		Binding: RemoteBinding{
			CallableName: "loop2", EndpointURL: "http://127.0.0.1:9/a2a",
			AllowedHosts: []string{"127.0.0.1"}, Version: 1,
		},
		Audit: audit,
	})
	if err == nil {
		t.Fatal("expected http rejected without AllowHTTP")
	}
}

func TestConstructValidateTimeout_Bounded(t *testing.T) {
	t.Parallel()
	// Default when TimeoutMs unset: use construct max (never unbounded).
	if d := constructValidateTimeout(0); d != outboundConstructValidateMax {
		t.Fatalf("timeoutMs=0 got %v want %v", d, outboundConstructValidateMax)
	}
	// Cap long binding timeouts so construct DNS cannot wait a full remote timeout.
	if d := constructValidateTimeout(60_000); d != outboundConstructValidateMax {
		t.Fatalf("timeoutMs=60000 got %v want cap %v", d, outboundConstructValidateMax)
	}
	// Mid-range uses binding value.
	if d := constructValidateTimeout(500); d != 500*time.Millisecond {
		t.Fatalf("timeoutMs=500 got %v", d)
	}
	// Floor very short values so construct still has a minimal DNS budget.
	if d := constructValidateTimeout(50); d != outboundConstructValidateMin {
		t.Fatalf("timeoutMs=50 got %v want min %v", d, outboundConstructValidateMin)
	}
}

func TestNewOutboundTool_ConstructValidate_CompletesQuickly(t *testing.T) {
	t.Parallel()
	// IP endpoint (no DNS) + short TimeoutMs must not hang on construct validation.
	audit := &memInboundAudit{}
	start := time.Now()
	_, err := NewOutboundTool(OutboundConfig{
		Binding: RemoteBinding{
			CallableName: "fast", EndpointURL: "https://127.0.0.1:9443/a2a",
			AllowedHosts: []string{"127.0.0.1"}, Version: 1, TimeoutMs: 300,
		},
		Audit: audit,
	})
	// May succeed or fail SSRF/policy; must return within construct bound.
	_ = err
	if elapsed := time.Since(start); elapsed > outboundConstructValidateMax+500*time.Millisecond {
		t.Fatalf("construct validation blocked too long: %v", elapsed)
	}
}

func TestSecureHTTPClient_RedirectRevalidated(t *testing.T) {
	t.Parallel()
	// Target private via redirect must be rejected.
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer private.Close()
	// Redirector on loopback http — only for test with AllowHTTP.
	var redirectURL string
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectURL, http.StatusFound)
	}))
	defer public.Close()
	redirectURL = private.URL // same host loopback — with AllowHTTP both ok; use metadata IP instead.
	// Use explicit redirect to a private IP host that DNS would block.
	client := SecureHTTPClient(2*time.Second, []string{"127.0.0.1"}, EgressPolicy{AllowHTTP: true})
	// First hop allowed (loopback+AllowHTTP). Second hop to 169.254.169.254 denied by CheckRedirect.
	req, _ := http.NewRequest(http.MethodGet, public.URL, nil)
	// Build a server that redirects to metadata IP.
	metaRedirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/", http.StatusFound)
	}))
	defer metaRedirect.Close()
	req, _ = http.NewRequest(http.MethodGet, metaRedirect.URL, nil)
	_, err := client.Do(req)
	if err == nil {
		t.Fatal("expected redirect to metadata denied")
	}
}

func TestSafeExternalRef(t *testing.T) {
	t.Parallel()
	got := SafeExternalRef("https://Agent.Example:443/path?x=1")
	if got == "" || got == "invalid" {
		t.Fatalf("got %q", got)
	}
}
