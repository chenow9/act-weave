// Package serviceendpoint defines the versioned, non-secret HTTP endpoint
// contract owned by a Provider.
package serviceendpoint

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const SchemaVersion = 2

var ErrInvalid = errors.New("invalid provider endpoint contract")

type Config struct {
	SchemaVersion  int               `json:"schemaVersion"`
	ServiceBaseURL string            `json:"serviceBaseUrl"`
	Discovery      Discovery         `json:"discovery,omitempty"`
	Verification   Verification      `json:"verification"`
	Egress         EgressPolicy      `json:"egress,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	// Legacy fields are accepted only for existing Providers.
	SourceURI string `json:"sourceUri,omitempty"`
	BaseURL   string `json:"baseUrl,omitempty"`
	URL       string `json:"url,omitempty"`
}

type Discovery struct {
	DocumentURL    string `json:"documentUrl,omitempty"`
	SourceRevision string `json:"sourceRevision,omitempty"`
}

type Verification struct {
	Method           string `json:"method"`
	Path             string `json:"path,omitempty"`
	ExpectedStatuses []int  `json:"expectedStatuses,omitempty"`
}

type EgressPolicy struct {
	AllowedHosts []string `json:"allowedHosts,omitempty"`
	AllowedPorts []int    `json:"allowedPorts,omitempty"`
	AllowedCIDRs []string `json:"allowedCIDRs,omitempty"`
	MaxRedirects int      `json:"maxRedirects,omitempty"`
}

func Parse(raw json.RawMessage) (Config, error) {
	var value Config
	if json.Unmarshal(raw, &value) != nil {
		return Config{}, ErrInvalid
	}
	if value.SchemaVersion == 0 {
		return normalizeLegacy(value)
	}
	if value.SchemaVersion != SchemaVersion {
		return Config{}, ErrInvalid
	}
	value.ServiceBaseURL = strings.TrimRight(strings.TrimSpace(value.ServiceBaseURL), "/")
	value.Discovery.DocumentURL = strings.TrimSpace(value.Discovery.DocumentURL)
	value.Discovery.SourceRevision = strings.TrimSpace(value.Discovery.SourceRevision)
	if !validServiceBaseURL(value.ServiceBaseURL) ||
		(value.Discovery.DocumentURL != "" && !validHTTPURL(value.Discovery.DocumentURL)) ||
		(value.Discovery.DocumentURL == "" && value.Discovery.SourceRevision != "") {
		return Config{}, ErrInvalid
	}
	if err := normalizeVerification(&value.Verification); err != nil {
		return Config{}, err
	}
	if err := validateEgress(value.Egress); err != nil {
		return Config{}, err
	}
	if err := validateHeaders(value.Headers); err != nil {
		return Config{}, err
	}
	return value, nil
}

// HasDiscovery reports whether this Provider has an online discovery source.
// Runtime execution and Connection verification only require ServiceBaseURL;
// discovery is an optional capability of the Provider.
func (value Config) HasDiscovery() bool {
	return strings.TrimSpace(value.Discovery.DocumentURL) != ""
}

func normalizeLegacy(value Config) (Config, error) {
	discovery := strings.TrimSpace(value.SourceURI)
	base := firstNonBlank(value.BaseURL, value.URL)
	if discovery == "" {
		discovery = base
	}
	if !validHTTPURL(discovery) || (base != "" && !validHTTPURL(base)) {
		return Config{}, ErrInvalid
	}
	value.ServiceBaseURL = strings.TrimRight(base, "/")
	value.Discovery.DocumentURL = discovery
	if err := normalizeVerification(&value.Verification); err != nil {
		return Config{}, err
	}
	if err := validateEgress(value.Egress); err != nil {
		return Config{}, err
	}
	if err := validateHeaders(value.Headers); err != nil {
		return Config{}, err
	}
	return value, nil
}

func validateEgress(value EgressPolicy) error {
	if value.MaxRedirects < 0 || value.MaxRedirects > 10 {
		return ErrInvalid
	}
	seenHosts := map[string]struct{}{}
	for _, raw := range value.AllowedHosts {
		host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		if host == "" || strings.ContainsAny(host, " \t\r\n%") ||
			(strings.Contains(host, ":") && net.ParseIP(host) == nil) ||
			(strings.Contains(host, "*") && (!strings.HasPrefix(host, "*.") || strings.Contains(host[2:], "*"))) {
			return ErrInvalid
		}
		if _, duplicate := seenHosts[host]; duplicate {
			return ErrInvalid
		}
		seenHosts[host] = struct{}{}
	}
	seenPorts := map[int]struct{}{}
	for _, port := range value.AllowedPorts {
		if port < 1 || port > 65535 {
			return ErrInvalid
		}
		if _, duplicate := seenPorts[port]; duplicate {
			return ErrInvalid
		}
		seenPorts[port] = struct{}{}
	}
	seenCIDRs := map[string]struct{}{}
	for _, raw := range value.AllowedCIDRs {
		cidr := strings.TrimSpace(raw)
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return ErrInvalid
		}
		if _, duplicate := seenCIDRs[cidr]; duplicate {
			return ErrInvalid
		}
		seenCIDRs[cidr] = struct{}{}
	}
	return nil
}

func validateHeaders(headers map[string]string) error {
	if len(headers) > 64 {
		return ErrInvalid
	}
	for name, value := range headers {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 128 || len(value) > 8192 || strings.ContainsAny(value, "\r\n\x00") {
			return ErrInvalid
		}
		for _, character := range name {
			if (character >= '0' && character <= '9') || (character >= 'A' && character <= 'Z') ||
				(character >= 'a' && character <= 'z') || strings.ContainsRune("!#$%&'*+-.^_`|~", character) {
				continue
			}
			return ErrInvalid
		}
	}
	return nil
}

func normalizeVerification(value *Verification) error {
	value.Method = strings.ToUpper(strings.TrimSpace(value.Method))
	if value.Method == "" {
		value.Method = http.MethodGet
	}
	if value.Method != http.MethodGet && value.Method != http.MethodHead && value.Method != http.MethodPost {
		return ErrInvalid
	}
	value.Path = strings.TrimSpace(value.Path)
	if value.Path != "" {
		parsed, err := url.Parse(value.Path)
		if err != nil || parsed == nil || parsed.IsAbs() || parsed.Host != "" || strings.HasPrefix(value.Path, "//") {
			return ErrInvalid
		}
		if !strings.HasPrefix(value.Path, "/") {
			value.Path = "/" + value.Path
		}
	}
	if len(value.ExpectedStatuses) == 0 {
		value.ExpectedStatuses = []int{http.StatusOK, http.StatusNoContent}
	}
	seen := map[int]struct{}{}
	for _, status := range value.ExpectedStatuses {
		if status < 100 || status > 599 {
			return ErrInvalid
		}
		seen[status] = struct{}{}
	}
	if len(seen) != len(value.ExpectedStatuses) {
		return ErrInvalid
	}
	return nil
}

func (value Config) VerificationURL() string {
	if value.Verification.Path == "" {
		return value.ServiceBaseURL
	}
	return strings.TrimRight(value.ServiceBaseURL, "/") + "/" + strings.TrimLeft(value.Verification.Path, "/")
}

func (value Verification) Accepts(status int) bool {
	for _, expected := range value.ExpectedStatuses {
		if status == expected {
			return true
		}
	}
	return false
}

func validHTTPURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed != nil && parsed.User == nil && parsed.Host != "" && parsed.Fragment == "" &&
		(parsed.Scheme == "https" || parsed.Scheme == "http")
}

func validServiceBaseURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed != nil && parsed.RawQuery == "" && validHTTPURL(value)
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
