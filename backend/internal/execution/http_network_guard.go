package execution

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaximumRedirects  = 3
	absoluteMaximumRedirects = 10
)

var restrictedEgressNetworks = mustIPNetworks(
	"0.0.0.0/8",
	"100.64.0.0/10",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"240.0.0.0/4",
	"2001:db8::/32",
)

type HostResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type HTTPNetworkGuard struct {
	resolver     HostResolver
	allowedHosts []string
	allowedPorts map[int]struct{}
	allowedCIDRs []*net.IPNet
	maxRedirects int
	dialer       *net.Dialer
}

func NewHTTPNetworkGuard(policy EgressPolicy, resolver HostResolver) (*HTTPNetworkGuard, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	guard := &HTTPNetworkGuard{
		resolver: resolver, allowedPorts: make(map[int]struct{}, len(policy.AllowedPorts)),
		maxRedirects: policy.MaxRedirects,
		dialer:       &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second},
	}
	for _, host := range policy.AllowedHosts {
		host = normalizeAllowedHost(host)
		if host == "" || strings.Contains(host, "%") ||
			(strings.Contains(host, ":") && net.ParseIP(host) == nil) ||
			(strings.Contains(host, "*") && (!strings.HasPrefix(host, "*.") || strings.Contains(host[2:], "*"))) {
			return nil, NewError(ErrorCodeEgressDenied, "POLICY", false, 0, nil)
		}
		guard.allowedHosts = append(guard.allowedHosts, host)
	}
	if len(guard.allowedHosts) == 0 {
		return nil, NewError(ErrorCodeEgressDenied, "POLICY", false, 0, nil)
	}
	sort.Strings(guard.allowedHosts)
	guard.allowedHosts = compactStrings(guard.allowedHosts)
	for _, port := range policy.AllowedPorts {
		if port < 1 || port > 65535 {
			return nil, NewError(ErrorCodeEgressDenied, "POLICY", false, 0, nil)
		}
		guard.allowedPorts[port] = struct{}{}
	}
	if len(guard.allowedPorts) == 0 {
		return nil, NewError(ErrorCodeEgressDenied, "POLICY", false, 0, nil)
	}
	for _, rawCIDR := range policy.AllowedCIDRs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(rawCIDR))
		if err != nil {
			return nil, NewError(ErrorCodeEgressDenied, "POLICY", false, 0, err)
		}
		guard.allowedCIDRs = append(guard.allowedCIDRs, network)
	}
	if guard.maxRedirects == 0 {
		guard.maxRedirects = defaultMaximumRedirects
	}
	if guard.maxRedirects < 0 || guard.maxRedirects > absoluteMaximumRedirects {
		return nil, NewError(ErrorCodeEgressDenied, "POLICY", false, 0, nil)
	}
	return guard, nil
}

func (guard *HTTPNetworkGuard) ValidateURL(ctx context.Context, target *url.URL) error {
	if guard == nil || target == nil || target.User != nil || target.Host == "" ||
		(target.Scheme != "http" && target.Scheme != "https") || target.Fragment != "" {
		return NewError(ErrorCodeEgressDenied, "POLICY", false, 0, nil)
	}
	host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
	if host == "" || strings.Contains(host, "%") || !guard.hostAllowed(host) {
		return NewError(ErrorCodeEgressDenied, "POLICY", false, 0, nil)
	}
	port, err := effectiveURLPort(target)
	if err != nil {
		return NewError(ErrorCodeEgressDenied, "POLICY", false, 0, err)
	}
	if _, allowed := guard.allowedPorts[port]; !allowed {
		return NewError(ErrorCodeEgressDenied, "POLICY", false, 0, nil)
	}
	_, err = guard.resolveAllowed(ctx, host)
	return err
}

func (guard *HTTPNetworkGuard) ProtectClient(base *http.Client, sensitiveHeaders []string) (*http.Client, error) {
	if guard == nil {
		return nil, NewError(ErrorCodeEgressDenied, "POLICY", false, 0, nil)
	}
	if base == nil {
		base = &http.Client{}
	}
	client := *base
	var transport *http.Transport
	switch typed := base.Transport.(type) {
	case nil:
		transport = http.DefaultTransport.(*http.Transport).Clone()
	case *http.Transport:
		transport = typed.Clone()
	default:
		return nil, NewError(ErrorCodeEgressDenied, "POLICY", false, 0, errors.New("unsupported HTTP transport"))
	}
	transport.Proxy = nil
	transport.DialContext = guard.dialContext
	transport.DialTLS = nil
	transport.DialTLSContext = nil
	client.Transport = transport
	previousRedirectCheck := base.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) > guard.maxRedirects {
			return NewError(ErrorCodeEgressDenied, "POLICY", false, 0, nil)
		}
		if err := guard.ValidateURL(request.Context(), request.URL); err != nil {
			return err
		}
		if len(via) > 0 && !sameOrigin(via[0].URL, request.URL) {
			for _, name := range sensitiveHeaders {
				request.Header.Del(name)
			}
			for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie"} {
				request.Header.Del(name)
			}
		}
		if previousRedirectCheck != nil {
			return previousRedirectCheck(request, via)
		}
		return nil
	}
	return &client, nil
}

func (guard *HTTPNetworkGuard) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, NewError(ErrorCodeEgressDenied, "POLICY", false, 0, nil)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, NewError(ErrorCodeEgressDenied, "POLICY", false, 0, err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		return nil, NewError(ErrorCodeEgressDenied, "POLICY", false, 0, err)
	}
	if _, allowed := guard.allowedPorts[portNumber]; !allowed || !guard.hostAllowed(strings.ToLower(strings.TrimSuffix(host, "."))) {
		return nil, NewError(ErrorCodeEgressDenied, "POLICY", false, 0, nil)
	}
	addresses, err := guard.resolveAllowed(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastError error
	for _, ip := range addresses {
		connection, dialErr := guard.dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastError = dialErr
	}
	return nil, lastError
}

func (guard *HTTPNetworkGuard) resolveAllowed(ctx context.Context, host string) ([]net.IP, error) {
	if parsed := net.ParseIP(host); parsed != nil {
		if !guard.ipAllowed(parsed) {
			return nil, NewError(ErrorCodeEgressDenied, "POLICY", false, 0, nil)
		}
		return []net.IP{parsed}, nil
	}
	resolved, err := guard.resolver.LookupIPAddr(ctx, host)
	if err != nil || len(resolved) == 0 {
		return nil, NewError(ErrorCodeEgressDenied, "NETWORK", true, 0, err)
	}
	addresses := make([]net.IP, 0, len(resolved))
	for _, item := range resolved {
		if item.IP == nil || !guard.ipAllowed(item.IP) {
			return nil, NewError(ErrorCodeEgressDenied, "POLICY", false, 0, nil)
		}
		addresses = append(addresses, append(net.IP(nil), item.IP...))
	}
	return addresses, nil
}

func (guard *HTTPNetworkGuard) hostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, allowed := range guard.allowedHosts {
		if strings.HasPrefix(allowed, "*.") {
			suffix := strings.TrimPrefix(allowed, "*")
			if strings.HasSuffix(host, suffix) && host != strings.TrimPrefix(suffix, ".") {
				return true
			}
			continue
		}
		if host == allowed {
			return true
		}
	}
	return false
}

func (guard *HTTPNetworkGuard) ipAllowed(ip net.IP) bool {
	for _, network := range guard.allowedCIDRs {
		if network.Contains(ip) {
			return true
		}
	}
	if !ip.IsGlobalUnicast() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, network := range restrictedEgressNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func normalizeAllowedHost(host string) string {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if strings.HasPrefix(host, "*.") && len(host) > 2 {
		return host
	}
	return host
}

func effectiveURLPort(target *url.URL) (int, error) {
	if target.Port() != "" {
		return strconv.Atoi(target.Port())
	}
	switch target.Scheme {
	case "http":
		return 80, nil
	case "https":
		return 443, nil
	default:
		return 0, fmt.Errorf("unsupported URL scheme")
	}
}

func sameOrigin(first, second *url.URL) bool {
	if first == nil || second == nil || first.Scheme != second.Scheme ||
		!strings.EqualFold(first.Hostname(), second.Hostname()) {
		return false
	}
	firstPort, firstError := effectiveURLPort(first)
	secondPort, secondError := effectiveURLPort(second)
	return firstError == nil && secondError == nil && firstPort == secondPort
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	compacted := values[:1]
	for _, value := range values[1:] {
		if value != compacted[len(compacted)-1] {
			compacted = append(compacted, value)
		}
	}
	return compacted
}

func mustIPNetworks(values ...string) []*net.IPNet {
	networks := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			panic(err)
		}
		networks = append(networks, network)
	}
	return networks
}
