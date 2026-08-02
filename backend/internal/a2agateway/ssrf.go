package a2agateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// EgressPolicy controls outbound A2A URL validation.
type EgressPolicy struct {
	// AllowHTTP permits http:// only for loopback hosts (tests). Production should leave false.
	AllowHTTP bool
	// Resolver for DNS; nil uses net.DefaultResolver.
	Resolver interface {
		LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
	}
}

// resolvedDial holds DNS-validated IPs for one hostname at dial time.
type resolvedDial struct {
	host  string
	addrs []net.IP
}

// ValidateOutboundURL enforces HTTPS (or http only for loopback when AllowHTTP),
// host allowlist, DNS resolution of private/link-local/metadata addresses (SSRF).
func ValidateOutboundURL(raw string, allowedHosts []string) error {
	return ValidateOutboundURLCtx(context.Background(), raw, allowedHosts, EgressPolicy{})
}

func ValidateOutboundURLCtx(ctx context.Context, raw string, allowedHosts []string, policy EgressPolicy) error {
	_, err := resolveOutboundURL(ctx, raw, allowedHosts, policy)
	return err
}

// resolveOutboundURL validates and returns the allowed IPs for dialing (empty for literal IP already dialable).
func resolveOutboundURL(ctx context.Context, raw string, allowedHosts []string, policy EgressPolicy) (*resolvedDial, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrInvalid
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: parse: %v", ErrInvalid, err)
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, ErrInvalid
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: userinfo not allowed", ErrSSRFDenied)
	}
	switch scheme {
	case "https":
	case "http":
		if !policy.AllowHTTP || !isLoopbackHost(host) {
			return nil, fmt.Errorf("%w: http only allowed for loopback in test policy", ErrSSRFDenied)
		}
	default:
		return nil, fmt.Errorf("%w: scheme %q", ErrSSRFDenied, scheme)
	}
	if isBlockedHostName(host) && !hostAllowed(host, allowedHosts) {
		return nil, fmt.Errorf("%w: host %q", ErrSSRFDenied, host)
	}
	if len(allowedHosts) > 0 && !hostAllowed(host, allowedHosts) {
		return nil, fmt.Errorf("%w: host %q not in allowlist", ErrSSRFDenied, host)
	}
	// Literal IP
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateOrSpecialIP(ip) && !(policy.AllowHTTP && ip.IsLoopback()) {
			return nil, fmt.Errorf("%w: private/special IP", ErrSSRFDenied)
		}
		return &resolvedDial{host: host, addrs: []net.IP{ip}}, nil
	}
	// DNS resolve and re-check every address (prevent DNS rebinding to private IPs).
	addrs, err := lookupIPs(ctx, host, policy)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		if isPrivateOrSpecialIP(a.IP) && !(policy.AllowHTTP && a.IP.IsLoopback()) {
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

// secureTransportMarker is embedded so OutboundTool can reject untrusted clients
// (e.g. http.DefaultTransport) that bypass dial-time DNS pin.
// policyFP is an immutable fingerprint of allowedHosts+AllowHTTP captured at
// SecureHTTPClient construction — injected clients with a broader allowlist
// cannot satisfy a narrower binding.
type secureTransportMarker struct {
	policyFP string
}

// PolicyFingerprint returns a stable hash of egress allowlist + AllowHTTP.
// Used to bind SecureHTTPClient instances to a specific Binding policy.
func PolicyFingerprint(allowedHosts []string, policy EgressPolicy) string {
	hosts := make([]string, 0, len(allowedHosts))
	for _, h := range allowedHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			hosts = append(hosts, h)
		}
	}
	sort.Strings(hosts)
	raw := strings.Join(hosts, ",") + "|allowHTTP="
	if policy.AllowHTTP {
		raw += "1"
	} else {
		raw += "0"
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// pinnedTransport wraps http.Transport and carries the secure marker.
// Response bodies are bounded so Agent Card / JSON-RPC cannot OOM the process.
type pinnedTransport struct {
	secureTransportMarker
	inner   *http.Transport
	maxBody int64
}

func (p *pinnedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := p.inner.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	limit := p.maxBody
	if limit <= 0 {
		limit = MaxOutboundResponseBytes
	}
	resp.Body = &maxBytesReadCloser{r: resp.Body, n: limit}
	return resp, nil
}

// maxBytesReadCloser returns ErrSSRFDenied only when more than n bytes are present.
// Reading exactly n bytes then EOF is success; the next non-EOF byte after n is oversize.
type maxBytesReadCloser struct {
	r        io.ReadCloser
	n        int64 // remaining allowed bytes; -1 means oversize already observed
	oversize bool
}

func (m *maxBytesReadCloser) Read(p []byte) (int, error) {
	if m.oversize {
		return 0, fmt.Errorf("%w: response body exceeds limit", ErrSSRFDenied)
	}
	if m.n == 0 {
		// Budget exhausted: only oversize if more data exists; EOF is OK (exact limit).
		var one [1]byte
		k, e2 := m.r.Read(one[:])
		if k > 0 {
			m.oversize = true
			return 0, fmt.Errorf("%w: response body exceeds limit", ErrSSRFDenied)
		}
		if e2 != nil {
			return 0, e2
		}
		return 0, io.EOF
	}
	if int64(len(p)) > m.n {
		p = p[:m.n]
	}
	n, err := m.r.Read(p)
	m.n -= int64(n)
	if err == nil && m.n == 0 {
		// Peek: if more bytes exist, fail this read so callers never accept oversize.
		var one [1]byte
		if k, e2 := m.r.Read(one[:]); k > 0 {
			m.oversize = true
			// Return bytes already within limit; next Read will report oversize.
			// Do not return oversize on the exact-limit chunk itself.
			return n, nil
		} else if e2 != nil && e2 != io.EOF {
			return n, e2
		}
		// EOF after exact limit: propagate as success on this read.
	}
	return n, err
}

func (m *maxBytesReadCloser) Close() error {
	if m.r == nil {
		return nil
	}
	return m.r.Close()
}

// IsSecureHTTPClient reports whether c was built by SecureHTTPClient (or a
// marker-preserving wrapper such as authPinnedTransport). Does NOT prove the
// client's allowlist matches a Binding — use IsSecureHTTPClientMatching.
func IsSecureHTTPClient(c *http.Client) bool {
	fp := clientPolicyFingerprint(c)
	return fp != ""
}

// IsSecureHTTPClientMatching requires a SecureHTTPClient whose immutable
// policy fingerprint equals PolicyFingerprint(allowedHosts, policy).
// Prevents broad-allowlist clients from being reused with a narrower Binding.
func IsSecureHTTPClientMatching(c *http.Client, allowedHosts []string, policy EgressPolicy) bool {
	fp := clientPolicyFingerprint(c)
	if fp == "" {
		return false
	}
	return fp == PolicyFingerprint(allowedHosts, policy)
}

func clientPolicyFingerprint(c *http.Client) string {
	if c == nil || c.Transport == nil {
		return ""
	}
	switch t := c.Transport.(type) {
	case *pinnedTransport:
		return t.policyFP
	case *authPinnedTransport:
		return t.policyFP
	default:
		return ""
	}
}

// authPinnedTransport injects Authorization only for the bound origin
// (scheme+host+port). Redirects to another host never receive credentials.
type authPinnedTransport struct {
	secureTransportMarker
	base        http.RoundTripper
	token       string
	boundOrigin string // e.g. https://agent.example:443
}

func (a *authPinnedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if a.boundOrigin != "" && requestOrigin(req) == a.boundOrigin {
		req.Header.Set("Authorization", a.token)
	} else {
		// Cross-origin hop (redirect): never forward credentials.
		stripCredentialHeaders(req)
	}
	return a.base.RoundTrip(req)
}

// stripCredentialHeaders removes Authorization and common API-key headers.
func stripCredentialHeaders(req *http.Request) {
	if req == nil {
		return
	}
	req.Header.Del("Authorization")
	req.Header.Del("X-Api-Key")
	req.Header.Del("X-API-Key")
	req.Header.Del("Api-Key")
	req.Header.Del("api-key")
}

func requestOrigin(req *http.Request) string {
	if req == nil || req.URL == nil {
		return ""
	}
	scheme := strings.ToLower(req.URL.Scheme)
	host := strings.ToLower(req.URL.Host)
	if scheme == "" || host == "" {
		return ""
	}
	// Normalize default ports away for comparison consistency.
	if (scheme == "https" && strings.HasSuffix(host, ":443")) ||
		(scheme == "http" && strings.HasSuffix(host, ":80")) {
		host = strings.Split(host, ":")[0]
	}
	return scheme + "://" + host
}

func originFromURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	if (scheme == "https" && strings.HasSuffix(host, ":443")) ||
		(scheme == "http" && strings.HasSuffix(host, ":80")) {
		host = strings.Split(host, ":")[0]
	}
	return scheme + "://" + host
}

// MaxOutboundResponseBytes bounds Agent Card / JSON-RPC response bodies.
const MaxOutboundResponseBytes = 4 << 20 // 4 MiB

// SecureHTTPClient returns an HTTP client that:
//  1. re-validates redirects against allowlist+SSRF (no empty-allowlist bypass on hops)
//  2. dials only IPs that pass DNS validation at dial-time (anti rebinding)
//  3. bounds response body reads (Agent Card / JSON-RPC)
//
// Callers MUST not replace Transport with an insecure one; use this factory only.
// Production remotes must pass a non-empty allowedHosts; empty allowlist is test-only
// and still blocks private/special IPs and blocked host names.
func SecureHTTPClient(timeout time.Duration, allowedHosts []string, policy EgressPolicy) *http.Client {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	baseDialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	inner := &http.Transport{
		// Disable env proxy (HTTPS_PROXY/HTTP_PROXY). Dial-time DNS pin must apply
		// to the real target host, not a proxy hop that re-resolves independently.
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			// address is host:port
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("%w: dial address: %v", ErrSSRFDenied, err)
			}
			host = strings.ToLower(host)
			// Reconstruct URL for validation (scheme unknown here; validate host/DNS only).
			// Dial-time re-resolve and pin: never trust a prior resolve from Validate alone.
			var ips []net.IP
			if ip := net.ParseIP(host); ip != nil {
				if isPrivateOrSpecialIP(ip) && !(policy.AllowHTTP && ip.IsLoopback()) {
					return nil, fmt.Errorf("%w: dial private/special IP", ErrSSRFDenied)
				}
				// Allowlist is mandatory when configured — never skip for literals.
				if len(allowedHosts) > 0 && !hostAllowed(host, allowedHosts) {
					return nil, fmt.Errorf("%w: dial host not allowlisted", ErrSSRFDenied)
				}
				ips = []net.IP{ip}
			} else {
				if len(allowedHosts) > 0 && !hostAllowed(host, allowedHosts) {
					return nil, fmt.Errorf("%w: dial host %q not in allowlist", ErrSSRFDenied, host)
				}
				if isBlockedHostName(host) && !hostAllowed(host, allowedHosts) {
					return nil, fmt.Errorf("%w: dial blocked host %q", ErrSSRFDenied, host)
				}
				addrs, lerr := lookupIPs(ctx, host, policy)
				if lerr != nil {
					return nil, lerr
				}
				for _, a := range addrs {
					if isPrivateOrSpecialIP(a.IP) && !(policy.AllowHTTP && a.IP.IsLoopback()) {
						return nil, fmt.Errorf("%w: dial-time resolved private IP %s (rebinding)", ErrSSRFDenied, a.IP)
					}
					ips = append(ips, a.IP)
				}
			}
			var last error
			for _, ip := range ips {
				var target string
				if ip4 := ip.To4(); ip4 != nil {
					target = net.JoinHostPort(ip4.String(), port)
				} else {
					target = net.JoinHostPort(ip.String(), port)
				}
				conn, derr := baseDialer.DialContext(ctx, network, target)
				if derr == nil {
					return conn, nil
				}
				last = derr
			}
			if last == nil {
				last = fmt.Errorf("%w: no dial targets", ErrSSRFDenied)
			}
			return nil, last
		},
		ForceAttemptHTTP2: true, MaxIdleConns: 32,
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
	}
	fp := PolicyFingerprint(allowedHosts, policy)
	return &http.Client{
		Timeout: timeout,
		Transport: &pinnedTransport{
			secureTransportMarker: secureTransportMarker{policyFP: fp},
			inner:                 inner,
			maxBody:               MaxOutboundResponseBytes,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("%w: too many redirects", ErrSSRFDenied)
			}
			// Cross-origin (and same-origin) redirects: target MUST pass full
			// allowlist + SSRF validation. Empty allowlist never bypasses private IP checks.
			// When allowedHosts is non-empty, off-list hosts are rejected before dial —
			// preventing 307/308 POST body leakage to unlisted origins.
			if err := ValidateOutboundURLCtx(req.Context(), req.URL.String(), allowedHosts, policy); err != nil {
				return err
			}
			if len(allowedHosts) > 0 {
				host := strings.ToLower(req.URL.Hostname())
				if !hostAllowed(host, allowedHosts) {
					return fmt.Errorf("%w: redirect host %q not in allowlist", ErrSSRFDenied, host)
				}
			}
			// Cross-origin redirect hops must never carry Authorization / API keys.
			if len(via) > 0 {
				prev := via[len(via)-1]
				if requestOrigin(req) != requestOrigin(prev) {
					stripCredentialHeaders(req)
				}
			}
			return nil
		},
	}
}

func hostAllowed(host string, allowed []string) bool {
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

func isPrivateOrSpecialIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// 0.0.0.0/8 (this network) — broader than IsUnspecified (only 0.0.0.0).
		if ip4[0] == 0 {
			return true
		}
		// 100.64.0.0/10 CGNAT
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
		// 192.0.0.0/24 IETF protocol assignments, 192.0.2.0/24 TEST-NET-1
		if ip4[0] == 192 && ip4[1] == 0 && (ip4[2] == 0 || ip4[2] == 2) {
			return true
		}
		// 198.18.0.0/15 benchmarking, 198.51.100.0/24 TEST-NET-2
		if ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19 || (ip4[1] == 51 && ip4[2] == 100)) {
			return true
		}
		// 203.0.113.0/24 TEST-NET-3
		if ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113 {
			return true
		}
		// 240.0.0.0/4 reserved for future use (includes 255.255.255.255 broadcast).
		if ip4[0] >= 240 {
			return true
		}
		return false
	}
	// IPv6: unique-local fc00::/7, documentation 2001:db8::/32
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

func sanitizeEndpointRef(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "invalid"
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + u.EscapedPath()
}
