// Package egressguard holds outbound URL / allowlist / secret-ref policy shared
// by a2agateway repository writes and agent_graph_snapshot freeze parse.
// It must not import agentdelegation or a2agateway (breaks cycles).
//
// The policy is split into two layers that must not be collapsed:
//
//	Syntax layer (zero I/O):    scheme, userinfo, host shape, blocked host names,
//	                            allowlist membership, literal-IP SSRF class, and
//	                            secret-ref format + tenant binding.
//	Resolve layer (needs ctx):  DNS resolution plus IP-level SSRF checks on the
//	                            resolved addresses.
//
// Frozen-snapshot parsing uses the syntax layer only, so validating a hostile or
// corrupt snapshot never emits network traffic. Real egress (a2agateway write
// path and dial path) runs both layers and always passes the caller's context
// into the resolve layer — never context.Background().
package egressguard

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// Sentinel errors (stable for errors.Is across packages).
var (
	ErrInvalid    = fmt.Errorf("egressguard: invalid")
	ErrSSRFDenied = fmt.Errorf("egressguard: ssrf denied")
)

// EgressPolicy controls outbound URL validation.
type EgressPolicy struct {
	// AllowHTTP permits http:// only for loopback hosts (tests). Production leaves false.
	AllowHTTP bool
	// Resolver for DNS; nil uses net.DefaultResolver. Resolve layer only.
	Resolver interface {
		LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
	}
}

// ValidateOutboundURLSyntax runs every policy check that can be decided without
// touching the network: scheme, userinfo, host presence, blocked host names,
// allowlist membership, and the SSRF class of a literal IP host.
//
// It never resolves DNS. A hostname host therefore passes this layer whenever
// it is covered by the allowlist; the resolve layer decides the rest.
func ValidateOutboundURLSyntax(raw string, allowedHosts []string, policy EgressPolicy) error {
	_, _, err := outboundURLSyntax(raw, allowedHosts, policy)
	return err
}

// ValidateOutboundURLCtx enforces the syntax layer and then the resolve layer
// (DNS + IP SSRF) using the caller's context. This is the full-strength check
// used for real egress.
func ValidateOutboundURLCtx(ctx context.Context, raw string, allowedHosts []string, policy EgressPolicy) error {
	_, err := resolveOutboundURL(ctx, raw, allowedHosts, policy)
	return err
}

// ValidateRemoteAllowlistSyntax requires non-empty allowedHosts and that both
// endpoint and optional agentCardURL pass the zero-I/O syntax layer.
// Frozen agent_graph_snapshot parsing uses this form.
func ValidateRemoteAllowlistSyntax(endpointURL, agentCardURL string, allowedHosts []string) error {
	hosts, err := normalizeAllowedHosts(allowedHosts)
	if err != nil {
		return err
	}
	if err := ValidateOutboundURLSyntax(endpointURL, hosts, EgressPolicy{}); err != nil {
		return err
	}
	if card := strings.TrimSpace(agentCardURL); card != "" {
		if err := ValidateOutboundURLSyntax(card, hosts, EgressPolicy{}); err != nil {
			return fmt.Errorf("agentCardURL: %w", err)
		}
	}
	return nil
}

// ValidateRemoteAllowlist requires non-empty allowedHosts and that both endpoint
// and optional agentCardURL pass syntax + DNS/IP SSRF coverage.
// Matches a2agateway.repository validateRemoteAllowlist.
func ValidateRemoteAllowlist(ctx context.Context, endpointURL, agentCardURL string, allowedHosts []string) error {
	hosts, err := normalizeAllowedHosts(allowedHosts)
	if err != nil {
		return err
	}
	if err := ValidateOutboundURLCtx(ctx, endpointURL, hosts, EgressPolicy{}); err != nil {
		return err
	}
	if card := strings.TrimSpace(agentCardURL); card != "" {
		if err := ValidateOutboundURLCtx(ctx, card, hosts, EgressPolicy{}); err != nil {
			return fmt.Errorf("agentCardURL: %w", err)
		}
	}
	return nil
}

func normalizeAllowedHosts(allowedHosts []string) ([]string, error) {
	hosts := make([]string, 0, len(allowedHosts))
	for _, h := range allowedHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			hosts = append(hosts, h)
		}
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("%w: allowedHosts must be non-empty", ErrInvalid)
	}
	return hosts, nil
}

// ValidateAuthSecretRef enforces secret:<workspaceUUID>:<secretUUID>.
// Empty ref is allowed (no outbound auth).
//
// A non-empty ref always requires a resolvable binding workspace: callers that
// cannot supply one fail closed rather than skipping the cross-tenant check.
func ValidateAuthSecretRef(bindingWorkspaceID, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	parts := strings.Split(ref, ":")
	if len(parts) != 3 || parts[0] != "secret" {
		return fmt.Errorf("%w: authSecretRef must be secret:<workspaceId>:<secretId>", ErrInvalid)
	}
	refWS, secretID := strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
	// Match a2agateway repository: parseable UUID (not necessarily lowercase form).
	if _, err := uuid.Parse(refWS); err != nil {
		return fmt.Errorf("%w: authSecretRef workspace/secret must be UUIDs", ErrInvalid)
	}
	if _, err := uuid.Parse(secretID); err != nil {
		return fmt.Errorf("%w: authSecretRef workspace/secret must be UUIDs", ErrInvalid)
	}
	ws := strings.TrimSpace(bindingWorkspaceID)
	if _, err := uuid.Parse(ws); err != nil {
		return fmt.Errorf("%w: authSecretRef requires a known binding workspace", ErrInvalid)
	}
	if !strings.EqualFold(refWS, ws) {
		return fmt.Errorf("%w: authSecretRef workspace must match binding workspace", ErrInvalid)
	}
	return nil
}

// CanonicalUUID reports whether v is a UUID whose uuid.Parse form equals v exactly
// (lowercase hex, no braces, no whitespace).
func CanonicalUUID(v string) bool {
	return canonicalUUID(v)
}

func canonicalUUID(v string) bool {
	id, err := uuid.Parse(v)
	if err != nil {
		return false
	}
	return id.String() == v
}

type resolvedDial struct {
	host  string
	addrs []net.IP
}

// outboundURLSyntax is the zero-I/O half of the policy. It returns the lowercase
// host and, when the host is a literal address, the parsed IP (already checked
// against the private/special-use classes).
func outboundURLSyntax(raw string, allowedHosts []string, policy EgressPolicy) (string, net.IP, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil, ErrInvalid
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", nil, fmt.Errorf("%w: parse: %v", ErrInvalid, err)
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", nil, ErrInvalid
	}
	if u.User != nil {
		return "", nil, fmt.Errorf("%w: userinfo not allowed", ErrSSRFDenied)
	}
	switch scheme {
	case "https":
	case "http":
		if !policy.AllowHTTP || !isLoopbackHost(host) {
			return "", nil, fmt.Errorf("%w: http only allowed for loopback in test policy", ErrSSRFDenied)
		}
	default:
		return "", nil, fmt.Errorf("%w: scheme %q", ErrSSRFDenied, scheme)
	}
	if isBlockedHostName(host) && !HostAllowed(host, allowedHosts) {
		return "", nil, fmt.Errorf("%w: host %q", ErrSSRFDenied, host)
	}
	if len(allowedHosts) > 0 && !HostAllowed(host, allowedHosts) {
		return "", nil, fmt.Errorf("%w: host %q not in allowlist", ErrSSRFDenied, host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsPrivateOrSpecialIP(ip) && !(policy.AllowHTTP && ip.IsLoopback()) {
			return "", nil, fmt.Errorf("%w: private/special IP", ErrSSRFDenied)
		}
		return host, ip, nil
	}
	return host, nil, nil
}

func resolveOutboundURL(ctx context.Context, raw string, allowedHosts []string, policy EgressPolicy) (*resolvedDial, error) {
	host, literal, err := outboundURLSyntax(raw, allowedHosts, policy)
	if err != nil {
		return nil, err
	}
	if literal != nil {
		return &resolvedDial{host: host, addrs: []net.IP{literal}}, nil
	}
	addrs, err := lookupIPs(ctx, host, policy)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		if IsPrivateOrSpecialIP(a.IP) && !(policy.AllowHTTP && a.IP.IsLoopback()) {
			return nil, fmt.Errorf("%w: resolved private IP %s", ErrSSRFDenied, a.IP)
		}
		ips = append(ips, a.IP)
	}
	return &resolvedDial{host: host, addrs: ips}, nil
}

func lookupIPs(ctx context.Context, host string, policy EgressPolicy) ([]net.IPAddr, error) {
	resolver := policy.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("%w: dns: %v", ErrSSRFDenied, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%w: dns empty", ErrSSRFDenied)
	}
	return addrs, nil
}

// HostAllowed reports whether host matches the allowlist (exact or *.suffix).
func HostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if a == host {
			return true
		}
		if strings.HasPrefix(a, "*.") && (strings.HasSuffix(host, a[1:]) || host == a[2:]) {
			return true
		}
	}
	return false
}

func isBlockedHostName(host string) bool {
	switch host {
	case "localhost", "metadata.google.internal", "metadata", "metadata.google.com":
		return true
	}
	if strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return true
	}
	return false
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// IsPrivateOrSpecialIP reports whether ip is private, loopback, link-local,
// documentation, CGNAT, or other special-use addresses blocked for egress.
func IsPrivateOrSpecialIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 0 {
			return true
		}
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
		if ip4[0] == 192 && ip4[1] == 0 && (ip4[2] == 0 || ip4[2] == 2) {
			return true
		}
		if ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19 || (ip4[1] == 51 && ip4[2] == 100)) {
			return true
		}
		if ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113 {
			return true
		}
		if ip4[0] >= 240 {
			return true
		}
		return false
	}
	if len(ip) == net.IPv6len {
		if ip[0]&0xfe == 0xfc {
			return true
		}
		if ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8 {
			return true
		}
	}
	return false
}
