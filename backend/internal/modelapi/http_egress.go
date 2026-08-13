package modelapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"actweave/backend/internal/execution"
)

// EgressPolicy contains deployment-controlled exceptions for model endpoints.
// The API base hostname and port are always pinned to the selected model
// configuration; AllowedCIDRs is only needed for explicitly trusted private or
// loopback model gateways.
type EgressPolicy struct {
	AllowedCIDRs []string
}

// LoopbackEgressPolicy is intended for tests and explicit local-only wiring.
// Production should use deployment configuration instead of this helper.
func LoopbackEgressPolicy() EgressPolicy {
	return EgressPolicy{AllowedCIDRs: []string{"127.0.0.0/8", "::1/128"}}
}

// RoundTripperWrapper lets observability/validation transports preserve their
// behavior while the network guard replaces the innermost dial transport.
type RoundTripperWrapper interface {
	WrappedRoundTripper() http.RoundTripper
	WithWrappedRoundTripper(http.RoundTripper) http.RoundTripper
}

// ProtectHTTPClientForAPIBase returns a copy of client whose transport can only
// dial the configured API base host and port. DNS is resolved again by the
// guarded DialContext, preventing rebinding to loopback, link-local, or private
// addresses unless the deployment explicitly allowlists the destination CIDR.
func ProtectHTTPClientForAPIBase(
	ctx context.Context,
	client *http.Client,
	rawAPIBase string,
	policy EgressPolicy,
) (*http.Client, error) {
	apiBase, err := validateAgenticAPIBase(rawAPIBase)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(apiBase)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed URL", ErrAgenticInvalidAPIBase)
	}
	port, err := modelAPIPort(parsed)
	if err != nil {
		return nil, err
	}
	guard, err := execution.NewHTTPNetworkGuard(execution.EgressPolicy{
		AllowedHosts: []string{strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))},
		AllowedPorts: []int{port},
		AllowedCIDRs: append([]string(nil), policy.AllowedCIDRs...),
		// Model clients reject redirects independently. Keeping the network guard
		// at one redirect also protects callers that use this helper directly.
		MaxRedirects: 1,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: egress policy rejected API base", ErrAgenticInvalidAPIBase)
	}
	baseClient, wrappers, err := unwrapHTTPClientTransport(client)
	if err != nil {
		return nil, err
	}
	protected, err := guard.ProtectClient(baseClient, []string{"Authorization"})
	if err != nil {
		return nil, fmt.Errorf("%w: unsupported HTTP transport", ErrAgenticInvalidAPIBase)
	}
	for index := len(wrappers) - 1; index >= 0; index-- {
		protected.Transport = wrappers[index].WithWrappedRoundTripper(protected.Transport)
	}
	// Reject literal private/loopback addresses during construction. Hostnames
	// remain dial-time validated so transient DNS failures do not prevent model
	// objects from being assembled before their first request.
	if isLiteralIP(parsed.Hostname()) {
		if err := guard.ValidateURL(ctx, parsed); err != nil {
			return nil, fmt.Errorf("%w: API base address is not allowed", ErrAgenticInvalidAPIBase)
		}
	}
	return protected, nil
}

func unwrapHTTPClientTransport(
	client *http.Client,
) (*http.Client, []RoundTripperWrapper, error) {
	if client == nil {
		client = NewStreamingHTTPClient()
	}
	copyClient := *client
	transport := copyClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	var wrappers []RoundTripperWrapper
	for {
		wrapper, ok := transport.(RoundTripperWrapper)
		if !ok {
			break
		}
		wrappers = append(wrappers, wrapper)
		transport = wrapper.WrappedRoundTripper()
		if transport == nil {
			return nil, nil, fmt.Errorf("%w: invalid HTTP transport wrapper", ErrAgenticInvalidAPIBase)
		}
	}
	copyClient.Transport = transport
	return &copyClient, wrappers, nil
}

func modelAPIPort(target *url.URL) (int, error) {
	if raw := target.Port(); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return 0, fmt.Errorf("%w: invalid port", ErrAgenticInvalidAPIBase)
		}
		return port, nil
	}
	switch strings.ToLower(target.Scheme) {
	case "http":
		return 80, nil
	case "https":
		return 443, nil
	default:
		return 0, fmt.Errorf("%w: scheme must be http or https", ErrAgenticInvalidAPIBase)
	}
}

func isLiteralIP(host string) bool {
	return net.ParseIP(host) != nil
}
