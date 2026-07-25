package outboundidentity

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// ProviderIdentity is the normalized Provider contract outbound-identity.v1.
// It never contains Secret, Token, Vault key, or locator fields.
type ProviderIdentity struct {
	SchemaVersion         string               `json:"schemaVersion"`
	SupportedModes        []Mode               `json:"supportedModes"`
	SupportedSubjectTypes []SubjectType        `json:"supportedSubjectTypes"`
	BrokerOBO             *ProviderBrokerOBO   `json:"brokerObo,omitempty"`
	RequestPassthrough    *ProviderPassthrough `json:"requestPassthrough,omitempty"`
}

// ProviderBrokerOBO is the non-secret Broker configuration owned by Provider.
type ProviderBrokerOBO struct {
	TokenEndpoint      string              `json:"tokenEndpoint"`
	Audience           string              `json:"audience"`
	GrantType          string              `json:"grantType"`
	SubjectTokenType   string              `json:"subjectTokenType"`
	RequestedTokenType string              `json:"requestedTokenType"`
	MachineAuthMethod  string              `json:"machineAuthMethod"`
	AllowedScopes      []string            `json:"allowedScopes"`
	Response           BrokerTokenResponse `json:"response"`
	BusinessInjection  BusinessInjection   `json:"businessInjection"`
}

// ProviderPassthrough is the non-secret request-passthrough configuration.
type ProviderPassthrough struct {
	CredentialTypes   []CredentialType  `json:"credentialTypes"`
	BusinessInjection BusinessInjection `json:"businessInjection"`
}

// BrokerTokenResponse describes where to read Broker token exchange fields.
type BrokerTokenResponse struct {
	AccessTokenPath   string `json:"accessTokenPath"`
	TokenTypePath     string `json:"tokenTypePath,omitempty"`
	ExpiresInPath     string `json:"expiresInPath,omitempty"`
	ExpectedTokenType string `json:"expectedTokenType,omitempty"`
}

// BusinessInjection is the fixed business Token injection header policy.
type BusinessInjection struct {
	HeaderName string `json:"headerName"`
	Prefix     string `json:"prefix,omitempty"`
}

var (
	scopePattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:._/-]{0,127}$`)
	jsonPathPartPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	audiencePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:._@/-]{0,255}$`)
)

const (
	defaultGrantType          = "urn:ietf:params:oauth:grant-type:token-exchange"
	defaultSubjectTokenType   = "urn:ietf:params:oauth:token-type:jwt"
	defaultRequestedTokenType = "urn:ietf:params:oauth:token-type:access_token"
	defaultExpectedTokenType  = "Bearer"
)

// ParseProviderIdentity decodes and validates outbound-identity.v1 from raw JSON.
func ParseProviderIdentity(raw json.RawMessage) (ProviderIdentity, error) {
	var identity ProviderIdentity
	if err := decodeStrictJSON(raw, &identity); err != nil {
		return ProviderIdentity{}, ErrIdentityPolicyInvalid.Wrap(err)
	}
	normalized, err := NormalizeProviderIdentity(identity)
	if err != nil {
		return ProviderIdentity{}, err
	}
	return normalized, nil
}

// NormalizeProviderIdentity validates, normalizes, and returns a deep clone.
func NormalizeProviderIdentity(identity ProviderIdentity) (ProviderIdentity, error) {
	if strings.TrimSpace(identity.SchemaVersion) != SchemaIdentity {
		return ProviderIdentity{}, ErrIdentityPolicyInvalid
	}
	if len(identity.SupportedModes) == 0 || len(identity.SupportedModes) > 2 {
		return ProviderIdentity{}, ErrIdentityModeUnsupported
	}
	modes := make([]Mode, 0, len(identity.SupportedModes))
	seenModes := map[Mode]struct{}{}
	for _, raw := range identity.SupportedModes {
		mode, ok := normalizeMode(string(raw))
		if !ok {
			return ProviderIdentity{}, ErrIdentityModeUnsupported
		}
		if _, dup := seenModes[mode]; dup {
			return ProviderIdentity{}, ErrIdentityPolicyInvalid
		}
		seenModes[mode] = struct{}{}
		modes = append(modes, mode)
	}
	sort.Slice(modes, func(i, j int) bool { return modes[i] < modes[j] })

	if len(identity.SupportedSubjectTypes) == 0 || len(identity.SupportedSubjectTypes) > 2 {
		return ProviderIdentity{}, ErrIdentityPolicyInvalid
	}
	subjects := make([]SubjectType, 0, len(identity.SupportedSubjectTypes))
	seenSubjects := map[SubjectType]struct{}{}
	for _, raw := range identity.SupportedSubjectTypes {
		subject, ok := normalizeSubjectType(string(raw))
		if !ok {
			// SYSTEM and any unknown subject type are hard-rejected.
			return ProviderIdentity{}, ErrSubjectRequired
		}
		if _, dup := seenSubjects[subject]; dup {
			return ProviderIdentity{}, ErrIdentityPolicyInvalid
		}
		seenSubjects[subject] = struct{}{}
		subjects = append(subjects, subject)
	}
	sort.Slice(subjects, func(i, j int) bool { return subjects[i] < subjects[j] })

	_, supportsBroker := seenModes[ModeBrokerOBO]
	_, supportsPassthrough := seenModes[ModeRequestPassthrough]

	var broker *ProviderBrokerOBO
	if supportsBroker {
		if identity.BrokerOBO == nil {
			return ProviderIdentity{}, ErrIdentityPolicyInvalid
		}
		normalized, err := normalizeProviderBroker(*identity.BrokerOBO)
		if err != nil {
			return ProviderIdentity{}, err
		}
		broker = &normalized
	} else if identity.BrokerOBO != nil {
		return ProviderIdentity{}, ErrIdentityPolicyInvalid
	}

	var passthrough *ProviderPassthrough
	if supportsPassthrough {
		if identity.RequestPassthrough == nil {
			return ProviderIdentity{}, ErrIdentityPolicyInvalid
		}
		normalized, err := normalizeProviderPassthrough(*identity.RequestPassthrough)
		if err != nil {
			return ProviderIdentity{}, err
		}
		passthrough = &normalized
	} else if identity.RequestPassthrough != nil {
		return ProviderIdentity{}, ErrIdentityPolicyInvalid
	}

	return ProviderIdentity{
		SchemaVersion:         SchemaIdentity,
		SupportedModes:        modes,
		SupportedSubjectTypes: subjects,
		BrokerOBO:             broker,
		RequestPassthrough:    passthrough,
	}, nil
}

// CloneProviderIdentity returns a deep copy without shared slices/pointers.
func CloneProviderIdentity(identity ProviderIdentity) ProviderIdentity {
	cloned := ProviderIdentity{
		SchemaVersion: identity.SchemaVersion,
	}
	if len(identity.SupportedModes) > 0 {
		cloned.SupportedModes = append([]Mode(nil), identity.SupportedModes...)
	}
	if len(identity.SupportedSubjectTypes) > 0 {
		cloned.SupportedSubjectTypes = append([]SubjectType(nil), identity.SupportedSubjectTypes...)
	}
	if identity.BrokerOBO != nil {
		broker := *identity.BrokerOBO
		broker.AllowedScopes = append([]string(nil), identity.BrokerOBO.AllowedScopes...)
		cloned.BrokerOBO = &broker
	}
	if identity.RequestPassthrough != nil {
		pt := *identity.RequestPassthrough
		pt.CredentialTypes = append([]CredentialType(nil), identity.RequestPassthrough.CredentialTypes...)
		cloned.RequestPassthrough = &pt
	}
	return cloned
}

// EqualProviderIdentity compares two normalized contracts.
func EqualProviderIdentity(a, b ProviderIdentity) bool {
	if a.SchemaVersion != b.SchemaVersion {
		return false
	}
	if !equalModes(a.SupportedModes, b.SupportedModes) || !equalSubjects(a.SupportedSubjectTypes, b.SupportedSubjectTypes) {
		return false
	}
	if (a.BrokerOBO == nil) != (b.BrokerOBO == nil) {
		return false
	}
	if a.BrokerOBO != nil && !equalBroker(*a.BrokerOBO, *b.BrokerOBO) {
		return false
	}
	if (a.RequestPassthrough == nil) != (b.RequestPassthrough == nil) {
		return false
	}
	if a.RequestPassthrough != nil && !equalPassthrough(*a.RequestPassthrough, *b.RequestPassthrough) {
		return false
	}
	return true
}

// SupportsMode reports whether the Provider contract admits the mode.
func (p ProviderIdentity) SupportsMode(mode Mode) bool {
	for _, supported := range p.SupportedModes {
		if supported == mode {
			return true
		}
	}
	return false
}

// SupportsSubject reports whether the Provider contract admits the subject type.
func (p ProviderIdentity) SupportsSubject(subject SubjectType) bool {
	for _, supported := range p.SupportedSubjectTypes {
		if supported == subject {
			return true
		}
	}
	return false
}

func normalizeProviderBroker(cfg ProviderBrokerOBO) (ProviderBrokerOBO, error) {
	endpoint := strings.TrimSpace(cfg.TokenEndpoint)
	if err := validateHTTPSURL(endpoint); err != nil {
		return ProviderBrokerOBO{}, ErrIdentityPolicyInvalid.Wrap(err)
	}
	audience := strings.TrimSpace(cfg.Audience)
	if audience == "" || !audiencePattern.MatchString(audience) {
		return ProviderBrokerOBO{}, ErrIdentityPolicyInvalid
	}
	grantType := strings.TrimSpace(cfg.GrantType)
	if grantType == "" {
		grantType = defaultGrantType
	}
	subjectTokenType := strings.TrimSpace(cfg.SubjectTokenType)
	if subjectTokenType == "" {
		subjectTokenType = defaultSubjectTokenType
	}
	requestedTokenType := strings.TrimSpace(cfg.RequestedTokenType)
	if requestedTokenType == "" {
		requestedTokenType = defaultRequestedTokenType
	}
	machineAuth := strings.TrimSpace(cfg.MachineAuthMethod)
	if machineAuth == "" {
		machineAuth = MachineAuthPrivateKeyJWT
	}
	if machineAuth != MachineAuthPrivateKeyJWT {
		return ProviderBrokerOBO{}, ErrIdentityModeUnsupported
	}
	scopes, err := normalizeScopes(cfg.AllowedScopes, true)
	if err != nil {
		return ProviderBrokerOBO{}, err
	}
	response, err := normalizeBrokerResponse(cfg.Response)
	if err != nil {
		return ProviderBrokerOBO{}, err
	}
	injection, err := normalizeBusinessInjection(cfg.BusinessInjection)
	if err != nil {
		return ProviderBrokerOBO{}, err
	}
	return ProviderBrokerOBO{
		TokenEndpoint:      endpoint,
		Audience:           audience,
		GrantType:          grantType,
		SubjectTokenType:   subjectTokenType,
		RequestedTokenType: requestedTokenType,
		MachineAuthMethod:  machineAuth,
		AllowedScopes:      scopes,
		Response:           response,
		BusinessInjection:  injection,
	}, nil
}

func normalizeProviderPassthrough(cfg ProviderPassthrough) (ProviderPassthrough, error) {
	if len(cfg.CredentialTypes) == 0 || len(cfg.CredentialTypes) > 8 {
		return ProviderPassthrough{}, ErrIdentityPolicyInvalid
	}
	types := make([]CredentialType, 0, len(cfg.CredentialTypes))
	seen := map[CredentialType]struct{}{}
	for _, raw := range cfg.CredentialTypes {
		credentialType := CredentialType(strings.TrimSpace(string(raw)))
		if !credentialType.Valid() {
			return ProviderPassthrough{}, ErrIdentityModeUnsupported
		}
		if _, dup := seen[credentialType]; dup {
			return ProviderPassthrough{}, ErrIdentityPolicyInvalid
		}
		seen[credentialType] = struct{}{}
		types = append(types, credentialType)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	injection, err := normalizeBusinessInjection(cfg.BusinessInjection)
	if err != nil {
		return ProviderPassthrough{}, err
	}
	return ProviderPassthrough{
		CredentialTypes:   types,
		BusinessInjection: injection,
	}, nil
}

func normalizeBrokerResponse(response BrokerTokenResponse) (BrokerTokenResponse, error) {
	accessPath := strings.TrimSpace(response.AccessTokenPath)
	if accessPath == "" {
		accessPath = "access_token"
	}
	if !validJSONPath(accessPath) {
		return BrokerTokenResponse{}, ErrIdentityPolicyInvalid
	}
	tokenTypePath := strings.TrimSpace(response.TokenTypePath)
	if tokenTypePath != "" && !validJSONPath(tokenTypePath) {
		return BrokerTokenResponse{}, ErrIdentityPolicyInvalid
	}
	expiresInPath := strings.TrimSpace(response.ExpiresInPath)
	if expiresInPath == "" {
		expiresInPath = "expires_in"
	}
	if !validJSONPath(expiresInPath) {
		return BrokerTokenResponse{}, ErrIdentityPolicyInvalid
	}
	expected := strings.TrimSpace(response.ExpectedTokenType)
	if expected == "" {
		expected = defaultExpectedTokenType
	}
	if len(expected) > 64 || strings.ContainsAny(expected, "\r\n\x00") {
		return BrokerTokenResponse{}, ErrIdentityPolicyInvalid
	}
	return BrokerTokenResponse{
		AccessTokenPath:   accessPath,
		TokenTypePath:     tokenTypePath,
		ExpiresInPath:     expiresInPath,
		ExpectedTokenType: expected,
	}, nil
}

func normalizeBusinessInjection(injection BusinessInjection) (BusinessInjection, error) {
	header := strings.TrimSpace(injection.HeaderName)
	if header == "" {
		header = "Authorization"
	}
	if !validHeaderName(header) {
		return BusinessInjection{}, ErrIdentityPolicyInvalid
	}
	prefix := injection.Prefix
	if prefix == "" {
		prefix = "Bearer"
	}
	if len(prefix) > 64 || strings.ContainsAny(prefix, "\r\n\x00") {
		return BusinessInjection{}, ErrIdentityPolicyInvalid
	}
	return BusinessInjection{HeaderName: header, Prefix: prefix}, nil
}

func normalizeScopes(scopes []string, allowEmpty bool) ([]string, error) {
	if len(scopes) == 0 {
		if allowEmpty {
			return []string{}, nil
		}
		return nil, ErrIdentityScopeNotAllowed
	}
	if len(scopes) > 64 {
		return nil, ErrIdentityPolicyInvalid
	}
	normalized := make([]string, 0, len(scopes))
	seen := map[string]struct{}{}
	for _, raw := range scopes {
		scope := strings.TrimSpace(raw)
		if scope == "" || !scopePattern.MatchString(scope) {
			return nil, ErrIdentityScopeNotAllowed
		}
		if _, dup := seen[scope]; dup {
			return nil, ErrIdentityPolicyInvalid
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func validateHTTPSURL(raw string) error {
	if raw == "" || len(raw) > 2048 {
		return ErrIdentityPolicyInvalid
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.User != nil || parsed.Host == "" ||
		parsed.Fragment != "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ErrIdentityPolicyInvalid
	}
	return nil
}

func validJSONPath(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) == 0 || len(parts) > 16 {
		return false
	}
	for _, part := range parts {
		if !jsonPathPartPattern.MatchString(part) {
			return false
		}
	}
	return true
}

func validHeaderName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("!#$%&'*+-.^_`|~", r) &&
			(r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}

func equalModes(a, b []Mode) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalSubjects(a, b []SubjectType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalBroker(a, b ProviderBrokerOBO) bool {
	if a.TokenEndpoint != b.TokenEndpoint || a.Audience != b.Audience ||
		a.GrantType != b.GrantType || a.SubjectTokenType != b.SubjectTokenType ||
		a.RequestedTokenType != b.RequestedTokenType || a.MachineAuthMethod != b.MachineAuthMethod ||
		a.Response != b.Response || a.BusinessInjection != b.BusinessInjection {
		return false
	}
	if len(a.AllowedScopes) != len(b.AllowedScopes) {
		return false
	}
	for i := range a.AllowedScopes {
		if a.AllowedScopes[i] != b.AllowedScopes[i] {
			return false
		}
	}
	return true
}

func equalPassthrough(a, b ProviderPassthrough) bool {
	if a.BusinessInjection != b.BusinessInjection || len(a.CredentialTypes) != len(b.CredentialTypes) {
		return false
	}
	for i := range a.CredentialTypes {
		if a.CredentialTypes[i] != b.CredentialTypes[i] {
			return false
		}
	}
	return true
}
