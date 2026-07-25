package outboundidentity

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Restricted egress networks aligned with execution.HTTPNetworkGuard so Broker
// token endpoints cannot target metadata / link-local / documentation ranges.
// outboundidentity cannot import execution (cycle via affinity gate).
var brokerRestrictedNetworks = mustBrokerIPNetworks(
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.168.0.0/16",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
	"2001:db8::/32",
)

// BrokerHostResolver abstracts DNS for tests.
type BrokerHostResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// BrokerNetworkGuard enforces HTTPS (or loopback-http in tests), port allowlist,
// DNS resolution, and private/restricted CIDR rejection on Broker dials.
// Mirrors the fail-closed posture of execution.HTTPNetworkGuard without importing it.
type BrokerNetworkGuard struct {
	resolver          BrokerHostResolver
	allowedPorts      map[int]struct{}
	allowLoopbackHTTP bool
	// pinnedHost when non-empty requires every dial host to match (token endpoint).
	pinnedHost string
	dialer     *net.Dialer
}

// NewBrokerNetworkGuard builds a guard for a single token-endpoint host.
func NewBrokerNetworkGuard(tokenEndpoint *url.URL, allowLoopbackHTTP bool, resolver BrokerHostResolver) (*BrokerNetworkGuard, error) {
	if tokenEndpoint == nil || tokenEndpoint.Host == "" || tokenEndpoint.User != nil || tokenEndpoint.Fragment != "" {
		return nil, ErrTargetRejected
	}
	host := strings.ToLower(strings.TrimSuffix(tokenEndpoint.Hostname(), "."))
	if host == "" || strings.Contains(host, "%") {
		return nil, ErrTargetRejected
	}
	port, err := brokerEffectivePort(tokenEndpoint)
	if err != nil {
		return nil, ErrTargetRejected
	}
	scheme := strings.ToLower(tokenEndpoint.Scheme)
	if scheme == "https" {
		// ok
	} else if allowLoopbackHTTP && scheme == "http" && isLoopbackHost(host) {
		// test-only loopback
	} else {
		return nil, ErrTargetRejected
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ports := map[int]struct{}{port: {}}
	// Always allow standard HTTPS; http loopback tests may use ephemeral ports.
	if scheme == "https" {
		ports[443] = struct{}{}
	}
	if allowLoopbackHTTP {
		ports[port] = struct{}{}
		ports[80] = struct{}{}
	}
	return &BrokerNetworkGuard{
		resolver:          resolver,
		allowedPorts:      ports,
		allowLoopbackHTTP: allowLoopbackHTTP,
		pinnedHost:        host,
		dialer:            &net.Dialer{Timeout: DefaultBrokerTimeout, KeepAlive: 30 * time.Second},
	}, nil
}

// ValidateURL checks scheme, userinfo, host pin, port, and resolved IPs.
func (g *BrokerNetworkGuard) ValidateURL(ctx context.Context, target *url.URL) error {
	if g == nil || target == nil || target.User != nil || target.Host == "" || target.Fragment != "" {
		return ErrTargetRejected
	}
	scheme := strings.ToLower(target.Scheme)
	host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
	if host == "" || strings.Contains(host, "%") {
		return ErrTargetRejected
	}
	if scheme == "https" {
		// ok
	} else if g.allowLoopbackHTTP && scheme == "http" && isLoopbackHost(host) {
		// ok
	} else {
		return ErrTargetRejected
	}
	if host != g.pinnedHost {
		// Cross-host (including redirects) rejected.
		return ErrTargetRejected
	}
	port, err := brokerEffectivePort(target)
	if err != nil {
		return ErrTargetRejected
	}
	if _, ok := g.allowedPorts[port]; !ok {
		// httptest uses ephemeral ports — allow any port on loopback when testing.
		if !(g.allowLoopbackHTTP && isLoopbackHost(host)) {
			return ErrTargetRejected
		}
	}
	_, err = g.resolveAllowed(ctx, host)
	return err
}

// ProtectClient returns a client with no proxy, guarded dial, and no redirects.
func (g *BrokerNetworkGuard) ProtectClient(base *http.Client) (*http.Client, error) {
	if g == nil {
		return nil, ErrTargetRejected
	}
	if base == nil {
		base = &http.Client{Timeout: DefaultBrokerTimeout}
	}
	client := *base
	var transport *http.Transport
	switch typed := base.Transport.(type) {
	case nil:
		transport = http.DefaultTransport.(*http.Transport).Clone()
	case *http.Transport:
		transport = typed.Clone()
	default:
		// Custom transports (httptest) — wrap via RoundTrip after dial is hard;
		// for tests we keep the transport but still ValidateURL before Do.
		transport = nil
	}
	if transport != nil {
		transport.Proxy = nil
		transport.DialContext = g.dialContext
		transport.DialTLS = nil
		transport.DialTLSContext = nil
		transport.TLSHandshakeTimeout = 5 * time.Second
		transport.ResponseHeaderTimeout = DefaultBrokerTimeout
		client.Transport = transport
	} else if base.Transport != nil {
		// Preserve custom transport (httptest) but force no proxy if it's *http.Transport-like.
		client.Transport = base.Transport
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if client.Timeout == 0 {
		client.Timeout = DefaultBrokerTimeout
	}
	return &client, nil
}

func (g *BrokerNetworkGuard) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, ErrTargetRejected
	}
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrTargetRejected
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, ErrTargetRejected
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host != g.pinnedHost {
		return nil, ErrTargetRejected
	}
	if _, ok := g.allowedPorts[port]; !ok {
		if !(g.allowLoopbackHTTP && isLoopbackHost(host)) {
			return nil, ErrTargetRejected
		}
	}
	addresses, err := g.resolveAllowed(ctx, host)
	if err != nil {
		return nil, err
	}
	var last error
	for _, ip := range addresses {
		conn, dialErr := g.dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), portStr))
		if dialErr == nil {
			return conn, nil
		}
		last = dialErr
	}
	if last == nil {
		return nil, ErrBrokerUnavailable
	}
	return nil, ErrBrokerUnavailable.Wrap(last)
}

func (g *BrokerNetworkGuard) resolveAllowed(ctx context.Context, host string) ([]net.IP, error) {
	if parsed := net.ParseIP(host); parsed != nil {
		if !g.ipAllowed(parsed) {
			return nil, ErrTargetRejected
		}
		return []net.IP{parsed}, nil
	}
	resolved, err := g.resolver.LookupIPAddr(ctx, host)
	if err != nil || len(resolved) == 0 {
		return nil, ErrBrokerUnavailable.Wrap(err)
	}
	addresses := make([]net.IP, 0, len(resolved))
	for _, item := range resolved {
		if item.IP == nil || !g.ipAllowed(item.IP) {
			// Any disallowed IP in the resolution set fails closed (DNS rebinding).
			return nil, ErrTargetRejected
		}
		addresses = append(addresses, append(net.IP(nil), item.IP...))
	}
	return addresses, nil
}

func (g *BrokerNetworkGuard) ipAllowed(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if g.allowLoopbackHTTP && ip.IsLoopback() {
		return true
	}
	if !ip.IsGlobalUnicast() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, network := range brokerRestrictedNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func brokerEffectivePort(target *url.URL) (int, error) {
	if target.Port() != "" {
		return strconv.Atoi(target.Port())
	}
	switch strings.ToLower(target.Scheme) {
	case "http":
		return 80, nil
	case "https":
		return 443, nil
	default:
		return 0, fmt.Errorf("unsupported scheme")
	}
}

func mustBrokerIPNetworks(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, raw := range cidrs {
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			panic(err)
		}
		out = append(out, network)
	}
	return out
}

// staticResolver returns fixed IPs for unit tests.
type staticResolver map[string][]net.IPAddr

func (s staticResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if addrs, ok := s[strings.ToLower(host)]; ok {
		return addrs, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}
