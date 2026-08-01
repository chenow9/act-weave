package aapfile

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SecretResolver resolves aap_workspace_file_processors.secret_ref to a raw HMAC secret.
// v1 tests store the secret value itself in secret_ref (inline).
type SecretResolver interface {
	Resolve(ctx context.Context, workspaceID, secretRef string) (string, error)
}

// InlineSecretResolver returns secret_ref as the secret (bootstrap / tests).
type InlineSecretResolver struct{}

// Resolve implements SecretResolver.
func (InlineSecretResolver) Resolve(_ context.Context, _, secretRef string) (string, error) {
	secretRef = strings.TrimSpace(secretRef)
	if secretRef == "" {
		return "", ErrInvalid
	}
	// Optional "inline:" prefix for clarity in fixtures.
	if strings.HasPrefix(secretRef, "inline:") {
		secretRef = strings.TrimPrefix(secretRef, "inline:")
	}
	if secretRef == "" {
		return "", ErrInvalid
	}
	return secretRef, nil
}

// ProcessorDeliveryPayload is the webhook POST body (file-processor.v1).
type ProcessorDeliveryPayload struct {
	SpecVersion string `json:"specVersion"`
	EventType   string `json:"eventType"`
	DeliveryID  string `json:"deliveryId"`
	WorkspaceID string `json:"workspaceId"`
	AgentID     string `json:"agentId"`
	FileID      string `json:"fileId"`
	MediaType   string `json:"mediaType"`
	SizeBytes   int64  `json:"sizeBytes"`
	SHA256      string `json:"sha256,omitempty"`
	Download    struct {
		URL       string `json:"url"`
		ExpiresAt string `json:"expiresAt"`
		Purpose   string `json:"purpose"`
	} `json:"download"`
	Callback struct {
		URL       string `json:"url"`
		ExpiresAt string `json:"expiresAt"`
	} `json:"callback"`
}

// ProcessorCallbackBody is the partner callback JSON (v1).
type ProcessorCallbackBody struct {
	ProcessorID string `json:"processorId"`
	Status      string `json:"status"`
	Artifacts   []struct {
		Kind          string `json:"kind"`
		MediaType     string `json:"mediaType"`
		ContentBase64 string `json:"contentBase64"`
	} `json:"artifacts"`
	Attributes map[string]any `json:"attributes"`
}

// SignPayload produces X-ActWeave-Signature: t=<unix>,v1=<hex> over t.body.
func SignPayload(secret string, body []byte, at time.Time) string {
	ts := at.UTC().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(ts, 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

// VerifySignature checks X-ActWeave-Signature within ±skew of now.
func VerifySignature(secret, header string, body []byte, now time.Time, skew time.Duration) error {
	header = strings.TrimSpace(header)
	if secret == "" || header == "" {
		return ErrCallbackUnauthorized
	}
	var ts int64
	var sigHex string
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "t=") {
			v, err := strconv.ParseInt(strings.TrimPrefix(part, "t="), 10, 64)
			if err != nil {
				return ErrCallbackUnauthorized
			}
			ts = v
		}
		if strings.HasPrefix(part, "v1=") {
			sigHex = strings.TrimPrefix(part, "v1=")
		}
	}
	if ts == 0 || sigHex == "" {
		return ErrCallbackUnauthorized
	}
	if skew <= 0 {
		skew = CallbackSignatureSkew
	}
	signedAt := time.Unix(ts, 0).UTC()
	delta := now.UTC().Sub(signedAt)
	if delta < 0 {
		delta = -delta
	}
	if delta > skew {
		return ErrCallbackUnauthorized
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(ts, 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	got, err := hex.DecodeString(sigHex)
	if err != nil || !hmac.Equal(expected, got) {
		return ErrCallbackUnauthorized
	}
	return nil
}

// ValidateWebhookURL enforces https and blocks private/link-local/metadata IPs (SSRF).
func ValidateWebhookURL(ctx context.Context, raw string, resolver HostResolver) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("%w: empty url", errors.New(ErrorCodeWebhookSSRF))
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" {
		return fmt.Errorf("%w: invalid url", errors.New(ErrorCodeWebhookSSRF))
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("%w: https required", errors.New(ErrorCodeWebhookSSRF))
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" || strings.Contains(host, "%") {
		return fmt.Errorf("%w: invalid host", errors.New(ErrorCodeWebhookSSRF))
	}
	// Literal IP in host.
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("%w: private ip", errors.New(ErrorCodeWebhookSSRF))
		}
		return nil
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("%w: dns", errors.New(ErrorCodeWebhookSSRF))
	}
	for _, addr := range addrs {
		if !isPublicIP(addr.IP) {
			return fmt.Errorf("%w: private ip", errors.New(ErrorCodeWebhookSSRF))
		}
	}
	return nil
}

// HostResolver is the DNS surface for SSRF checks (tests inject fakes).
type HostResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		!ip.IsGlobalUnicast() {
		return false
	}
	// Extra restricted ranges (CGNAT, docs, benchmarking).
	for _, network := range restrictedWebhookNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

var restrictedWebhookNetworks = mustParseCIDRs(
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

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, network, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out = append(out, network)
	}
	return out
}

// SafeHTTPClient returns an HTTP client that dials only public IPs (SSRF-safe).
func SafeHTTPClient(timeout time.Duration, resolver HostResolver) *http.Client {
	if timeout <= 0 {
		timeout = DefaultWebhookTimeout
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil, // never use env proxy for processor delivery
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if ip := net.ParseIP(host); ip != nil {
				if !isPublicIP(ip) {
					return nil, fmt.Errorf("%w: private dial", errors.New(ErrorCodeWebhookSSRF))
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			}
			addrs, err := resolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			var last error
			for _, addr := range addrs {
				if !isPublicIP(addr.IP) {
					last = fmt.Errorf("%w: private dial", errors.New(ErrorCodeWebhookSSRF))
					continue
				}
				conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
				if dialErr == nil {
					return conn, nil
				}
				last = dialErr
			}
			if last == nil {
				last = fmt.Errorf("%w: no address", errors.New(ErrorCodeWebhookSSRF))
			}
			return nil, last
		},
		// No redirects — avoid open-redirect SSRF.
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// BuildDeliveryPayload constructs the file-processor.v1 JSON body.
func BuildDeliveryPayload(
	file File,
	deliveryID, downloadURL, callbackURL string,
	downloadExp, callbackExp time.Time,
) (ProcessorDeliveryPayload, []byte, error) {
	var payload ProcessorDeliveryPayload
	payload.SpecVersion = ProcessorSpecVersion
	payload.EventType = ProcessorEventUploaded
	payload.DeliveryID = deliveryID
	payload.WorkspaceID = file.WorkspaceID
	payload.AgentID = file.AgentID
	payload.FileID = file.ID
	if file.DetectedMediaType != nil && strings.TrimSpace(*file.DetectedMediaType) != "" {
		payload.MediaType = *file.DetectedMediaType
	} else {
		payload.MediaType = file.DeclaredMediaType
	}
	payload.SizeBytes = file.SizeBytes
	if file.SHA256 != nil {
		payload.SHA256 = *file.SHA256
	}
	payload.Download.URL = downloadURL
	payload.Download.ExpiresAt = downloadExp.UTC().Format(time.RFC3339Nano)
	payload.Download.Purpose = DownloadPurposeProcessorDelivery
	payload.Callback.URL = callbackURL
	payload.Callback.ExpiresAt = callbackExp.UTC().Format(time.RFC3339Nano)
	body, err := json.Marshal(payload)
	if err != nil {
		return ProcessorDeliveryPayload{}, nil, err
	}
	return payload, body, nil
}

// ParseCallbackBody unmarshals and validates callback status.
func ParseCallbackBody(raw []byte) (ProcessorCallbackBody, error) {
	var body ProcessorCallbackBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return ProcessorCallbackBody{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	body.ProcessorID = strings.TrimSpace(body.ProcessorID)
	body.Status = strings.ToLower(strings.TrimSpace(body.Status))
	switch body.Status {
	case "succeeded", "failed":
	default:
		return ProcessorCallbackBody{}, fmt.Errorf("%w: status", ErrInvalid)
	}
	// Bound decoded artifact size.
	var total int
	for _, art := range body.Artifacts {
		// base64 expands ~4/3; count wire length as upper bound proxy then exact decode later.
		total += len(art.ContentBase64)
		if total > CallbackArtifactMaxBytes*2 {
			return ProcessorCallbackBody{}, ErrArtifactTooLarge
		}
	}
	return body, nil
}

// DecodedArtifactBytes returns total decoded base64 bytes; errors if over limit.
func DecodedArtifactBytes(body ProcessorCallbackBody) (int, error) {
	total := 0
	for _, art := range body.Artifacts {
		// Approximate: valid base64 length * 3/4.
		n := len(strings.TrimSpace(art.ContentBase64))
		decoded := n / 4 * 3
		total += decoded
		if total > CallbackArtifactMaxBytes {
			return total, ErrArtifactTooLarge
		}
	}
	return total, nil
}

// ReadLimitedBody reads up to max+1 bytes; errors if over max.
func ReadLimitedBody(r io.Reader, max int64) ([]byte, error) {
	if max < 1 {
		max = CallbackBodyMaxBytes
	}
	limited := io.LimitReader(r, max+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("%w: body too large", ErrInvalid)
	}
	return body, nil
}

// JobResultRequired reports required flag stored in job.result JSON.
func JobResultRequired(result []byte, stage string) bool {
	if IsRequiredBuiltinStage(stage) {
		return true
	}
	if len(result) == 0 {
		return false
	}
	var meta struct {
		Required bool `json:"required"`
	}
	if err := json.Unmarshal(result, &meta); err != nil {
		return false
	}
	return meta.Required
}

// MarshalJobMeta builds a small result JSON with required + optional fields.
func MarshalJobMeta(required bool, extra map[string]any) []byte {
	m := map[string]any{"required": required}
	for k, v := range extra {
		m[k] = v
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return []byte(`{"required":false}`)
	}
	return raw
}
