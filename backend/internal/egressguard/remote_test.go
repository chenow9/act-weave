package egressguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
)

// countingResolver records every DNS lookup so tests can assert that the syntax
// layer emits zero network traffic.
type countingResolver struct {
	lookups atomic.Int64
	hosts   chan string
	result  map[string][]net.IPAddr
	err     error
}

func newCountingResolver() *countingResolver {
	return &countingResolver{hosts: make(chan string, 64), result: map[string][]net.IPAddr{}}
}

func (r *countingResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	r.lookups.Add(1)
	select {
	case r.hosts <- host:
	default:
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.err != nil {
		return nil, r.err
	}
	if addrs, ok := r.result[host]; ok {
		return addrs, nil
	}
	return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
}

func ipAddrs(ips ...string) []net.IPAddr {
	out := make([]net.IPAddr, 0, len(ips))
	for _, s := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out
}

// --- syntax layer: zero I/O ------------------------------------------------

func TestValidateOutboundURLSyntax_NeverResolvesDNS(t *testing.T) {
	res := newCountingResolver()
	policy := EgressPolicy{Resolver: res}
	inputs := []string{
		"https://good.example/a2a",
		"https://sub.good.example/a2a",
		"https://not-listed.example/a2a",
		"https://evil.internal/a2a",
		"http://good.example/a2a",
		"file:///etc/passwd",
		"https://user:pass@good.example/",
		"https://10.0.0.5/x",
		"https://1.1.1.1/a2a",
	}
	for _, in := range inputs {
		// Result is irrelevant here; the invariant is that no lookup happened.
		_ = ValidateOutboundURLSyntax(in, []string{"good.example", "*.good.example", "1.1.1.1", "evil.internal", "10.0.0.5"}, policy)
	}
	if n := res.lookups.Load(); n != 0 {
		t.Fatalf("syntax layer performed %d DNS lookups, want 0", n)
	}
}

func TestValidateOutboundURLSyntax_Matrix(t *testing.T) {
	allowed := []string{"good.example", "*.wild.example", "1.1.1.1"}
	cases := []struct {
		name    string
		raw     string
		policy  EgressPolicy
		wantErr error // nil means accept
	}{
		{name: "https_allowlisted_ok", raw: "https://good.example/a2a"},
		{name: "https_wildcard_subdomain_ok", raw: "https://a.wild.example/a2a"},
		{name: "https_wildcard_apex_ok", raw: "https://wild.example/a2a"},
		{name: "public_literal_ip_allowlisted_ok", raw: "https://1.1.1.1/a2a"},

		{name: "empty", raw: "   ", wantErr: ErrInvalid},
		{name: "relative", raw: "/a2a", wantErr: ErrInvalid},
		{name: "no_host", raw: "https:///a2a", wantErr: ErrInvalid},
		// Hostless file:// is rejected as malformed before the scheme check;
		// a hosted file:// URL is rejected by the scheme allowlist.
		{name: "file_scheme_hostless", raw: "file:///etc/passwd", wantErr: ErrInvalid},
		{name: "file_scheme_hosted", raw: "file://good.example/etc/passwd", wantErr: ErrSSRFDenied},
		{name: "ftp_scheme", raw: "ftp://good.example/x", wantErr: ErrSSRFDenied},
		{name: "userinfo", raw: "https://u:p@good.example/", wantErr: ErrSSRFDenied},
		{name: "http_without_allow", raw: "http://good.example/", wantErr: ErrSSRFDenied},
		{name: "http_allowed_but_not_loopback", raw: "http://good.example/", policy: EgressPolicy{AllowHTTP: true}, wantErr: ErrSSRFDenied},
		{name: "host_not_in_allowlist", raw: "https://evil.example/", wantErr: ErrSSRFDenied},
		{name: "metadata_host", raw: "https://metadata.google.internal/", wantErr: ErrSSRFDenied},
		{name: "dot_internal_suffix", raw: "https://svc.internal/", wantErr: ErrSSRFDenied},
		{name: "dot_local_suffix", raw: "https://svc.local/", wantErr: ErrSSRFDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOutboundURLSyntax(tc.raw, allowed, tc.policy)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("want accept, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateOutboundURLSyntax_LiteralPrivateIPsBlockedWithoutDNS(t *testing.T) {
	res := newCountingResolver()
	// Even when the operator explicitly allowlists the literal address, the
	// private/special-use class check still denies it.
	for _, raw := range []string{
		"https://10.0.0.5/x",
		"https://192.168.1.1/x",
		"https://172.16.0.1/x",
		"https://169.254.169.254/latest/meta-data",
		"https://127.0.0.1/x",
		"https://0.0.0.0/x",
		"https://100.64.0.1/x",
		"https://198.18.0.1/x",
		"https://203.0.113.1/x",
		"https://240.0.0.1/x",
		"https://[fd00::1]/x",
		"https://[::1]/x",
		"https://[2001:db8::1]/x",
	} {
		host := strings.TrimSuffix(strings.TrimPrefix(raw, "https://"), "/x")
		host = strings.TrimSuffix(host, "/latest/meta-data")
		host = strings.Trim(host, "[]")
		err := ValidateOutboundURLSyntax(raw, []string{host}, EgressPolicy{Resolver: res})
		if !errors.Is(err, ErrSSRFDenied) {
			t.Fatalf("%s: want ssrf denied, got %v", raw, err)
		}
	}
	if n := res.lookups.Load(); n != 0 {
		t.Fatalf("literal-IP path performed %d DNS lookups, want 0", n)
	}
}

func TestValidateOutboundURLSyntax_LoopbackHTTPOnlyUnderTestPolicy(t *testing.T) {
	if err := ValidateOutboundURLSyntax("http://127.0.0.1:8080/a2a", nil, EgressPolicy{AllowHTTP: true}); err != nil {
		t.Fatalf("loopback http under AllowHTTP: %v", err)
	}
	if err := ValidateOutboundURLSyntax("http://127.0.0.1:8080/a2a", nil, EgressPolicy{}); !errors.Is(err, ErrSSRFDenied) {
		t.Fatalf("loopback http without AllowHTTP must deny, got %v", err)
	}
	// "localhost" is a blocked host name; AllowHTTP does not un-block it unless
	// the caller explicitly allowlists it.
	if err := ValidateOutboundURLSyntax("http://localhost:8080/a2a", nil, EgressPolicy{AllowHTTP: true}); !errors.Is(err, ErrSSRFDenied) {
		t.Fatalf("localhost must be denied without explicit allowlist, got %v", err)
	}
	if err := ValidateOutboundURLSyntax("http://localhost:8080/a2a", []string{"localhost"}, EgressPolicy{AllowHTTP: true}); err != nil {
		t.Fatalf("explicitly allowlisted localhost under AllowHTTP: %v", err)
	}
}

// --- resolve layer: DNS + IP SSRF, caller ctx ------------------------------

func TestValidateOutboundURLCtx_ResolvesAndBlocksPrivateAnswers(t *testing.T) {
	res := newCountingResolver()
	res.result["rebind.example"] = ipAddrs("127.0.0.1")
	err := ValidateOutboundURLCtx(
		context.Background(), "https://rebind.example/a2a",
		[]string{"rebind.example"}, EgressPolicy{Resolver: res},
	)
	if !errors.Is(err, ErrSSRFDenied) {
		t.Fatalf("resolved loopback must be denied, got %v", err)
	}
	if res.lookups.Load() != 1 {
		t.Fatalf("lookups=%d want 1", res.lookups.Load())
	}
}

func TestValidateOutboundURLCtx_AcceptsPublicAnswer(t *testing.T) {
	res := newCountingResolver()
	res.result["good.example"] = ipAddrs("93.184.216.34")
	if err := ValidateOutboundURLCtx(
		context.Background(), "https://good.example/a2a",
		[]string{"good.example"}, EgressPolicy{Resolver: res},
	); err != nil {
		t.Fatalf("public answer must be accepted: %v", err)
	}
}

func TestValidateOutboundURLCtx_MixedAnswerFailsClosed(t *testing.T) {
	res := newCountingResolver()
	res.result["mixed.example"] = ipAddrs("93.184.216.34", "10.1.2.3")
	err := ValidateOutboundURLCtx(
		context.Background(), "https://mixed.example/a2a",
		[]string{"mixed.example"}, EgressPolicy{Resolver: res},
	)
	if !errors.Is(err, ErrSSRFDenied) {
		t.Fatalf("any private answer must deny the whole URL, got %v", err)
	}
}

func TestValidateOutboundURLCtx_EmptyAnswerFailsClosed(t *testing.T) {
	res := newCountingResolver()
	res.result["empty.example"] = []net.IPAddr{}
	err := ValidateOutboundURLCtx(
		context.Background(), "https://empty.example/a2a",
		[]string{"empty.example"}, EgressPolicy{Resolver: res},
	)
	if !errors.Is(err, ErrSSRFDenied) {
		t.Fatalf("empty DNS answer must deny, got %v", err)
	}
}

// TestValidateOutboundURLCtx_HonoursCallerContext is the MAJOR-7 regression:
// the resolve layer must use the caller's context, so a cancelled caller
// cancels the lookup instead of running on context.Background().
func TestValidateOutboundURLCtx_HonoursCallerContext(t *testing.T) {
	res := newCountingResolver()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := ValidateOutboundURLCtx(ctx, "https://good.example/a2a", []string{"good.example"}, EgressPolicy{Resolver: res})
	if err == nil {
		t.Fatal("cancelled caller context must fail the resolve layer")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected cancellation to propagate, got %v", err)
	}
}

func TestValidateOutboundURLCtx_SyntaxRejectionSkipsResolve(t *testing.T) {
	res := newCountingResolver()
	// Host is syntactically denied (not allowlisted) → resolve layer never runs.
	err := ValidateOutboundURLCtx(
		context.Background(), "https://evil.example/a2a",
		[]string{"good.example"}, EgressPolicy{Resolver: res},
	)
	if !errors.Is(err, ErrSSRFDenied) {
		t.Fatalf("want ssrf denied, got %v", err)
	}
	if n := res.lookups.Load(); n != 0 {
		t.Fatalf("syntax rejection performed %d lookups, want 0", n)
	}
}

func TestValidateOutboundURLCtx_ResolverErrorFailsClosed(t *testing.T) {
	res := newCountingResolver()
	res.err = fmt.Errorf("nxdomain")
	err := ValidateOutboundURLCtx(
		context.Background(), "https://good.example/a2a",
		[]string{"good.example"}, EgressPolicy{Resolver: res},
	)
	if !errors.Is(err, ErrSSRFDenied) {
		t.Fatalf("resolver error must deny, got %v", err)
	}
}

// --- remote allowlist wrappers ---------------------------------------------

func TestValidateRemoteAllowlistSyntax_RequiresNonEmptyAllowlist(t *testing.T) {
	res := newCountingResolver()
	_ = res
	if err := ValidateRemoteAllowlistSyntax("https://good.example/a2a", "", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty allowlist must be invalid, got %v", err)
	}
	if err := ValidateRemoteAllowlistSyntax("https://good.example/a2a", "", []string{"  ", ""}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank-only allowlist must be invalid, got %v", err)
	}
}

func TestValidateRemoteAllowlistSyntax_ChecksAgentCardURL(t *testing.T) {
	if err := ValidateRemoteAllowlistSyntax(
		"https://good.example/a2a", "https://evil.example/card", []string{"good.example"},
	); !errors.Is(err, ErrSSRFDenied) {
		t.Fatalf("agentCardURL outside allowlist must deny, got %v", err)
	}
	if err := ValidateRemoteAllowlistSyntax(
		"https://good.example/a2a", "https://good.example/card", []string{"good.example"},
	); err != nil {
		t.Fatalf("well-formed remote must pass syntax layer: %v", err)
	}
}

func TestValidateRemoteAllowlistSyntax_ZeroLookups(t *testing.T) {
	// The freeze parse path must never emit DNS for attacker-supplied hosts.
	// The syntax wrapper hard-codes an empty policy, so the only way to observe
	// a lookup here would be an accidental call into the resolve layer; assert
	// acceptance of a host that does not resolve at all.
	if err := ValidateRemoteAllowlistSyntax(
		"https://this-host-does-not-exist.invalid/a2a", "",
		[]string{"this-host-does-not-exist.invalid"},
	); err != nil {
		t.Fatalf("syntax layer must not depend on resolvability: %v", err)
	}
}

func TestValidateRemoteAllowlist_RunsBothLayers(t *testing.T) {
	res := newCountingResolver()
	res.result["good.example"] = ipAddrs("93.184.216.34")
	if err := ValidateRemoteAllowlist(
		context.Background(), "https://good.example/a2a", "", []string{"good.example"},
	); err != nil {
		// Uses the default resolver; good.example is reserved and may not resolve
		// in CI, so only assert that it is not an *unexpected* class of error.
		if !errors.Is(err, ErrSSRFDenied) {
			t.Fatalf("unexpected error class: %v", err)
		}
	}
	// Literal public IP needs no DNS and must pass both layers deterministically.
	if err := ValidateRemoteAllowlist(
		context.Background(), "https://1.1.1.1/a2a", "", []string{"1.1.1.1"},
	); err != nil {
		t.Fatalf("public literal IP must pass both layers: %v", err)
	}
	// Literal private IP must be denied by the syntax layer even with allowlist.
	if err := ValidateRemoteAllowlist(
		context.Background(), "https://10.0.0.1/a2a", "", []string{"10.0.0.1"},
	); !errors.Is(err, ErrSSRFDenied) {
		t.Fatalf("private literal IP must be denied, got %v", err)
	}
}

// --- secret ref binding -----------------------------------------------------

func TestValidateAuthSecretRef_Matrix(t *testing.T) {
	const ws = "a11ce000-0000-4000-8000-000000000001"
	const other = "b22ce000-0000-4000-8000-000000000002"
	const secret = "c33ce000-0000-4000-8000-000000000003"

	cases := []struct {
		name    string
		binding string
		ref     string
		wantErr bool
	}{
		{name: "empty_ref_ok", binding: ws, ref: "", wantErr: false},
		{name: "empty_ref_blank_binding_ok", binding: "", ref: "   ", wantErr: false},
		{name: "matching_workspace_ok", binding: ws, ref: "secret:" + ws + ":" + secret},
		{name: "case_insensitive_workspace_ok", binding: ws, ref: "secret:" + strings.ToUpper(ws) + ":" + secret},

		{name: "cross_tenant_denied", binding: ws, ref: "secret:" + other + ":" + secret, wantErr: true},
		{name: "unknown_binding_denied", binding: "", ref: "secret:" + ws + ":" + secret, wantErr: true},
		{name: "blank_binding_denied", binding: "   ", ref: "secret:" + ws + ":" + secret, wantErr: true},
		{name: "non_uuid_binding_denied", binding: "not-a-uuid", ref: "secret:" + ws + ":" + secret, wantErr: true},
		{name: "wrong_prefix_denied", binding: ws, ref: "vault:" + ws + ":" + secret, wantErr: true},
		{name: "too_few_parts_denied", binding: ws, ref: "secret:" + ws, wantErr: true},
		{name: "too_many_parts_denied", binding: ws, ref: "secret:" + ws + ":" + secret + ":x", wantErr: true},
		{name: "non_uuid_workspace_denied", binding: ws, ref: "secret:nope:" + secret, wantErr: true},
		{name: "non_uuid_secret_denied", binding: ws, ref: "secret:" + ws + ":nope", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAuthSecretRef(tc.binding, tc.ref)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("want ErrInvalid, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("want accept, got %v", err)
			}
		})
	}
}

// TestValidateAuthSecretRef_NeverEchoesRef guards the error hygiene contract:
// secret locators must not leak into messages that end up in logs / API errors.
func TestValidateAuthSecretRef_NeverEchoesRef(t *testing.T) {
	const ws = "a11ce000-0000-4000-8000-000000000001"
	const other = "b22ce000-0000-4000-8000-000000000002"
	const secret = "c33ce000-0000-4000-8000-000000000003"
	ref := "secret:" + other + ":" + secret
	err := ValidateAuthSecretRef(ws, ref)
	if err == nil {
		t.Fatal("expected cross-tenant denial")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), other) {
		t.Fatalf("error leaked secret locator: %v", err)
	}
}

// --- helpers ---------------------------------------------------------------

func TestHostAllowed(t *testing.T) {
	cases := []struct {
		host    string
		allowed []string
		want    bool
	}{
		{"good.example", []string{"good.example"}, true},
		{"GOOD.example", []string{"good.example"}, true},
		{"good.example", []string{" good.example "}, true},
		{"a.wild.example", []string{"*.wild.example"}, true},
		{"wild.example", []string{"*.wild.example"}, true},
		{"evilwild.example", []string{"*.wild.example"}, false},
		{"good.example.evil.com", []string{"good.example"}, false},
		{"good.example", nil, false},
		{"good.example", []string{""}, false},
	}
	for _, tc := range cases {
		if got := HostAllowed(tc.host, tc.allowed); got != tc.want {
			t.Fatalf("HostAllowed(%q,%v)=%v want %v", tc.host, tc.allowed, got, tc.want)
		}
	}
}

func TestCanonicalUUID(t *testing.T) {
	const lower = "a11ce000-0000-4000-8000-000000000001"
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{lower, true},
		{strings.ToUpper(lower), false},
		{"{" + lower + "}", false},
		{" " + lower, false},
		{"a11ce000000040008000000000000001", false},
		{"", false},
		{"not-a-uuid", false},
	} {
		if got := CanonicalUUID(tc.in); got != tc.want {
			t.Fatalf("CanonicalUUID(%q)=%v want %v", tc.in, got, tc.want)
		}
	}
}

func TestIsPrivateOrSpecialIP(t *testing.T) {
	blocked := []string{
		"0.0.0.0", "10.1.2.3", "127.0.0.1", "169.254.169.254", "172.16.0.1",
		"192.0.0.1", "192.0.2.1", "192.168.0.1", "198.18.0.1", "198.51.100.1",
		"203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"100.64.0.1", "::1", "fd00::1", "fe80::1", "2001:db8::1",
		// CGNAT range edges: the check is a byte range, so both ends matter.
		"100.64.0.0", "100.127.255.255", "fcff::1",
	}
	for _, s := range blocked {
		if !IsPrivateOrSpecialIP(net.ParseIP(s)) {
			t.Fatalf("%s must be blocked", s)
		}
	}
	// Just outside CGNAT on either side stays routable; an over-broad range
	// would quietly block legitimate partners.
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700::1111",
		"100.63.255.255", "100.128.0.1"} {
		if IsPrivateOrSpecialIP(net.ParseIP(s)) {
			t.Fatalf("%s must be allowed", s)
		}
	}
	if !IsPrivateOrSpecialIP(nil) {
		t.Fatal("nil IP must fail closed")
	}
}
