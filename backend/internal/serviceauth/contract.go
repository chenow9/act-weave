// Package serviceauth owns the versioned authentication contract shared by
// Providers, Connections and the HTTP execution plane.
package serviceauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	ContractVersion = "service-auth.v1"

	SchemeNone         = "NONE"
	SchemeOAuth2Client = "OAUTH2_CLIENT"

	FieldText   = "TEXT"
	FieldSecret = "SECRET"
	FieldSelect = "SELECT"

	ClientSecretBasic = "client_secret_basic"
	ClientSecretPost  = "client_secret_post"

	RefreshClientCredentials = "CLIENT_CREDENTIALS"
	RefreshToken             = "REFRESH_TOKEN"
)

var (
	ErrInvalidContract   = errors.New("invalid provider authentication contract")
	ErrInvalidConnection = errors.New("invalid connection authentication configuration")
	contractKeyPattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,63}$`)
	templatePattern      = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z0-9._-]{0,63})\s*\}\}`)
)

// DriverConfig is the non-secret Provider driver configuration. Other driver
// keys remain opaque; Authentication is the only cross-plane contract.
type DriverConfig struct {
	Authentication *Contract `json:"authentication,omitempty"`
}

type Contract struct {
	Version          string   `json:"version"`
	DefaultSchemeKey string   `json:"defaultSchemeKey"`
	Schemes          []Scheme `json:"schemes"`
}

type Scheme struct {
	Key         string        `json:"key"`
	Type        string        `json:"type"`
	DisplayName string        `json:"displayName"`
	Description string        `json:"description,omitempty"`
	Fields      []Field       `json:"fields,omitempty"`
	OAuth2      *OAuth2Config `json:"oauth2,omitempty"`
}

type Field struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Kind        string   `json:"kind"`
	Required    bool     `json:"required,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Help        string   `json:"help,omitempty"`
	Options     []Option `json:"options,omitempty"`
}

type Option struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type OAuth2Config struct {
	TokenURLTemplate string           `json:"tokenUrlTemplate"`
	ClientIDField    string           `json:"clientIdField"`
	CredentialField  string           `json:"credentialField"`
	ClientAuthMethod string           `json:"clientAuthMethod"`
	ScopeField       string           `json:"scopeField,omitempty"`
	TokenParameters  []TokenParameter `json:"tokenParameters,omitempty"`
	Response         TokenResponse    `json:"response"`
	Injection        TokenInjection   `json:"injection"`
	RefreshStrategy  string           `json:"refreshStrategy,omitempty"`
}

type TokenParameter struct {
	Name     string `json:"name"`
	Field    string `json:"field,omitempty"`
	Value    string `json:"value,omitempty"`
	Required bool   `json:"required,omitempty"`
}

type TokenResponse struct {
	AccessTokenPath  string `json:"accessTokenPath"`
	TokenTypePath    string `json:"tokenTypePath,omitempty"`
	ExpiresInPath    string `json:"expiresInPath,omitempty"`
	RenewalTokenPath string `json:"renewalTokenPath,omitempty"`
}

type TokenInjection struct {
	HeaderName string `json:"headerName"`
	Prefix     string `json:"prefix,omitempty"`
}

type ConnectionConfig struct {
	SchemeKey string            `json:"schemeKey"`
	Values    map[string]string `json:"values"`
}

// ResolvedOAuth2 is safe execution metadata. It never contains the credential
// value; CredentialField identifies the single Secret-backed field.
type ResolvedOAuth2 struct {
	SchemeKey        string
	TokenURL         string
	ClientID         string
	CredentialField  string
	ClientAuthMethod string
	Scope            string
	TokenParameters  map[string]string
	Response         TokenResponse
	Injection        TokenInjection
	RefreshStrategy  string
}

type Resolved struct {
	SchemeKey       string
	SchemeType      string
	DisplayName     string
	CredentialField string
	OAuth2          *ResolvedOAuth2
}

// ParseDriverConfig returns ok=false for legacy Providers that do not yet own
// an authentication contract.
func ParseDriverConfig(raw json.RawMessage) (contract Contract, ok bool, err error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return Contract{}, false, nil
	}
	var driver DriverConfig
	if json.Unmarshal(raw, &driver) != nil {
		return Contract{}, false, ErrInvalidContract
	}
	if driver.Authentication == nil {
		return Contract{}, false, nil
	}
	contract = *driver.Authentication
	if err := ValidateContract(contract); err != nil {
		return Contract{}, false, err
	}
	return contract, true, nil
}

func ValidateDriverConfig(raw json.RawMessage) error {
	_, _, err := ParseDriverConfig(raw)
	return err
}

func ValidateContract(contract Contract) error {
	if contract.Version != ContractVersion || !contractKeyPattern.MatchString(contract.DefaultSchemeKey) || len(contract.Schemes) == 0 || len(contract.Schemes) > 16 {
		return ErrInvalidContract
	}
	seenSchemes := make(map[string]struct{}, len(contract.Schemes))
	defaultFound := false
	for _, scheme := range contract.Schemes {
		if !contractKeyPattern.MatchString(scheme.Key) || strings.TrimSpace(scheme.DisplayName) == "" {
			return ErrInvalidContract
		}
		if _, duplicate := seenSchemes[scheme.Key]; duplicate {
			return ErrInvalidContract
		}
		seenSchemes[scheme.Key] = struct{}{}
		defaultFound = defaultFound || scheme.Key == contract.DefaultSchemeKey
		if err := validateScheme(scheme); err != nil {
			return err
		}
	}
	if !defaultFound {
		return ErrInvalidContract
	}
	return nil
}

func validateScheme(scheme Scheme) error {
	if scheme.Type != SchemeNone && scheme.Type != SchemeOAuth2Client {
		return ErrInvalidContract
	}
	fields := make(map[string]Field, len(scheme.Fields))
	secretCount := 0
	for _, field := range scheme.Fields {
		if !contractKeyPattern.MatchString(field.Key) || strings.TrimSpace(field.Label) == "" ||
			(field.Kind != FieldText && field.Kind != FieldSecret && field.Kind != FieldSelect) {
			return ErrInvalidContract
		}
		if _, duplicate := fields[field.Key]; duplicate {
			return ErrInvalidContract
		}
		if field.Kind == FieldSecret {
			secretCount++
		}
		if field.Kind == FieldSelect {
			if len(field.Options) == 0 || len(field.Options) > 32 {
				return ErrInvalidContract
			}
			seen := map[string]struct{}{}
			for _, option := range field.Options {
				if strings.TrimSpace(option.Label) == "" || strings.TrimSpace(option.Value) == "" {
					return ErrInvalidContract
				}
				if _, duplicate := seen[option.Value]; duplicate {
					return ErrInvalidContract
				}
				seen[option.Value] = struct{}{}
			}
		}
		fields[field.Key] = field
	}
	if secretCount > 1 {
		// service_connections currently exposes one versioned Secret reference.
		return ErrInvalidContract
	}
	if scheme.Type == SchemeNone {
		if scheme.Key != "none" || scheme.OAuth2 != nil || len(scheme.Fields) != 0 {
			return ErrInvalidContract
		}
		return nil
	}
	if scheme.OAuth2 == nil {
		return ErrInvalidContract
	}
	oauth := *scheme.OAuth2
	if oauth.ClientIDField != "clientId" || oauth.CredentialField != "clientSecret" ||
		(oauth.ScopeField != "" && oauth.ScopeField != "scope") ||
		!fieldHasKind(fields, oauth.ClientIDField, FieldText, FieldSelect) ||
		!fieldHasKind(fields, oauth.CredentialField, FieldSecret) ||
		!fields[oauth.ClientIDField].Required || !fields[oauth.CredentialField].Required ||
		(oauth.ScopeField != "" && !fieldHasKind(fields, oauth.ScopeField, FieldText, FieldSelect)) ||
		(oauth.ClientAuthMethod != ClientSecretBasic && oauth.ClientAuthMethod != ClientSecretPost) ||
		(oauth.RefreshStrategy != "" && oauth.RefreshStrategy != RefreshClientCredentials && oauth.RefreshStrategy != RefreshToken) {
		return ErrInvalidContract
	}
	if err := validateTokenURLTemplate(oauth.TokenURLTemplate, fields); err != nil {
		return err
	}
	seenParameters := map[string]struct{}{}
	for _, parameter := range oauth.TokenParameters {
		if !contractKeyPattern.MatchString(parameter.Name) || reservedOAuthParameter(parameter.Name) ||
			(parameter.Field == "") == (parameter.Value == "") {
			return ErrInvalidContract
		}
		if _, duplicate := seenParameters[parameter.Name]; duplicate {
			return ErrInvalidContract
		}
		seenParameters[parameter.Name] = struct{}{}
		if parameter.Field != "" && !fieldHasKind(fields, parameter.Field, FieldText, FieldSelect) {
			return ErrInvalidContract
		}
	}
	if !validJSONPath(oauth.Response.AccessTokenPath) || !validOptionalJSONPath(oauth.Response.TokenTypePath) ||
		!validOptionalJSONPath(oauth.Response.ExpiresInPath) || !validOptionalJSONPath(oauth.Response.RenewalTokenPath) ||
		!validHeaderName(oauth.Injection.HeaderName) || len(oauth.Injection.Prefix) > 64 ||
		strings.ContainsAny(oauth.Injection.Prefix, "\r\n\x00") {
		return ErrInvalidContract
	}
	return nil
}

func reservedOAuthParameter(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "grant_type", "client_id", "client_secret", "scope", "refresh_token":
		return true
	default:
		return false
	}
}

func validateTokenURLTemplate(value string, fields map[string]Field) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len(trimmed) > 2048 {
		return ErrInvalidContract
	}
	resolved := templatePattern.ReplaceAllStringFunc(trimmed, func(match string) string {
		parts := templatePattern.FindStringSubmatch(match)
		if len(parts) != 2 || !fieldHasKind(fields, parts[1], FieldText, FieldSelect) {
			return ""
		}
		return "template-value"
	})
	if strings.Contains(resolved, "{{") || strings.Contains(resolved, "}}") {
		return ErrInvalidContract
	}
	parsed, err := url.Parse(resolved)
	if err != nil || parsed == nil || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ErrInvalidContract
	}
	// Connection values may specialize a path or query, but never select the
	// token endpoint host.
	prefix := trimmed
	if index := strings.Index(prefix, "/"); index >= 0 {
		// Keep scheme://host together when checking the authority component.
		if authority := strings.Index(prefix, "://"); authority >= 0 {
			rest := prefix[authority+3:]
			if slash := strings.IndexAny(rest, "/?#"); slash >= 0 {
				prefix = prefix[:authority+3+slash]
			}
		}
	}
	if templatePattern.MatchString(prefix) {
		return ErrInvalidContract
	}
	return nil
}

func fieldHasKind(fields map[string]Field, key string, kinds ...string) bool {
	field, ok := fields[key]
	if !ok {
		return false
	}
	for _, kind := range kinds {
		if field.Kind == kind {
			return true
		}
	}
	return false
}

func Resolve(driverConfig, authConfig json.RawMessage, authMode string, credentialConfigured bool) (Resolved, error) {
	contract, ok, err := ParseDriverConfig(driverConfig)
	if err != nil {
		return Resolved{}, err
	}
	if !ok {
		return resolveLegacy(authConfig, authMode, credentialConfigured)
	}
	var connection ConnectionConfig
	if json.Unmarshal(authConfig, &connection) != nil {
		return Resolved{}, ErrInvalidConnection
	}
	if connection.SchemeKey == "" && connection.Values == nil {
		// Compatibility window for Connections created before the Provider-owned
		// contract existed. Transport callers preserve this shape verbatim.
		return resolveLegacy(authConfig, authMode, credentialConfigured)
	}
	if connection.Values == nil {
		return Resolved{}, ErrInvalidConnection
	}
	if connection.SchemeKey == "" {
		connection.SchemeKey = contract.DefaultSchemeKey
	}
	scheme, found := contractScheme(contract, connection.SchemeKey)
	if !found || strings.TrimSpace(authMode) != scheme.Type {
		return Resolved{}, ErrInvalidConnection
	}
	fields := make(map[string]Field, len(scheme.Fields))
	for _, field := range scheme.Fields {
		fields[field.Key] = field
	}
	for key := range connection.Values {
		field, exists := fields[key]
		if !exists || field.Kind == FieldSecret || len(connection.Values[key]) > 4096 ||
			strings.ContainsAny(connection.Values[key], "\r\n\x00") {
			return Resolved{}, ErrInvalidConnection
		}
		if field.Kind == FieldSelect && !containsOption(field.Options, connection.Values[key]) {
			return Resolved{}, ErrInvalidConnection
		}
	}
	for _, field := range scheme.Fields {
		if !field.Required {
			continue
		}
		if field.Kind == FieldSecret {
			if !credentialConfigured {
				return Resolved{}, ErrInvalidConnection
			}
			continue
		}
		if strings.TrimSpace(connection.Values[field.Key]) == "" {
			return Resolved{}, ErrInvalidConnection
		}
	}
	resolved := Resolved{SchemeKey: scheme.Key, SchemeType: scheme.Type, DisplayName: scheme.DisplayName}
	if scheme.Type == SchemeNone {
		if credentialConfigured {
			return Resolved{}, ErrInvalidConnection
		}
		return resolved, nil
	}
	oauth := *scheme.OAuth2
	tokenURL, err := resolveTemplate(oauth.TokenURLTemplate, connection.Values)
	if err != nil {
		return Resolved{}, err
	}
	parameters := make(map[string]string, len(oauth.TokenParameters))
	for _, parameter := range oauth.TokenParameters {
		value := parameter.Value
		if parameter.Field != "" {
			value = strings.TrimSpace(connection.Values[parameter.Field])
		}
		if parameter.Required && value == "" {
			return Resolved{}, ErrInvalidConnection
		}
		if value != "" {
			parameters[parameter.Name] = value
		}
	}
	refresh := oauth.RefreshStrategy
	if refresh == "" {
		refresh = RefreshClientCredentials
	}
	resolved.CredentialField = oauth.CredentialField
	resolved.OAuth2 = &ResolvedOAuth2{
		SchemeKey: scheme.Key, TokenURL: tokenURL,
		ClientID: strings.TrimSpace(connection.Values[oauth.ClientIDField]), CredentialField: oauth.CredentialField,
		ClientAuthMethod: oauth.ClientAuthMethod, Scope: strings.TrimSpace(connection.Values[oauth.ScopeField]),
		TokenParameters: parameters, Response: withResponseDefaults(oauth.Response),
		Injection: withInjectionDefaults(oauth.Injection), RefreshStrategy: refresh,
	}
	return resolved, nil
}

func resolveTemplate(template string, values map[string]string) (string, error) {
	trimmed := strings.TrimSpace(template)
	matches := templatePattern.FindAllStringSubmatchIndex(trimmed, -1)
	queryStart := strings.Index(trimmed, "?")
	var resolved strings.Builder
	last := 0
	failed := false
	for _, match := range matches {
		if len(match) != 4 {
			failed = true
			break
		}
		resolved.WriteString(trimmed[last:match[0]])
		key := strings.TrimSpace(trimmed[match[2]:match[3]])
		value := strings.TrimSpace(values[key])
		if value == "" {
			failed = true
			break
		}
		if queryStart >= 0 && match[0] > queryStart {
			resolved.WriteString(url.QueryEscape(value))
		} else {
			resolved.WriteString(url.PathEscape(value))
		}
		last = match[1]
	}
	resolved.WriteString(trimmed[last:])
	resolvedValue := resolved.String()
	parsed, err := url.Parse(resolvedValue)
	if failed || strings.Contains(resolvedValue, "{{") || strings.Contains(resolvedValue, "}}") ||
		err != nil || parsed == nil || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", ErrInvalidConnection
	}
	return resolvedValue, nil
}

func resolveLegacy(authConfig json.RawMessage, authMode string, credentialConfigured bool) (Resolved, error) {
	mode := strings.TrimSpace(authMode)
	if mode == SchemeNone {
		if credentialConfigured {
			return Resolved{}, ErrInvalidConnection
		}
		return Resolved{SchemeKey: "legacy-none", SchemeType: SchemeNone, DisplayName: "No authentication"}, nil
	}
	if mode != SchemeOAuth2Client {
		// API key and bearer legacy modes continue through the established header
		// injector and are intentionally not interpreted as schema contracts.
		return Resolved{SchemeKey: "legacy", SchemeType: mode}, nil
	}
	var legacy struct {
		TokenURL   string `json:"tokenUrl"`
		ClientID   string `json:"clientId"`
		ClientAuth string `json:"clientAuth"`
		Scope      string `json:"scope"`
	}
	if json.Unmarshal(authConfig, &legacy) != nil || strings.TrimSpace(legacy.TokenURL) == "" || strings.TrimSpace(legacy.ClientID) == "" || !credentialConfigured {
		return Resolved{}, ErrInvalidConnection
	}
	parsed, err := url.Parse(strings.TrimSpace(legacy.TokenURL))
	if err != nil || parsed == nil || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return Resolved{}, ErrInvalidConnection
	}
	method := strings.TrimSpace(legacy.ClientAuth)
	if method == "" {
		method = ClientSecretBasic
	}
	if method != ClientSecretBasic && method != ClientSecretPost {
		return Resolved{}, ErrInvalidConnection
	}
	return Resolved{
		SchemeKey: "legacy-oauth2-client", SchemeType: SchemeOAuth2Client, DisplayName: "OAuth2 Client Credentials", CredentialField: "clientSecret",
		OAuth2: &ResolvedOAuth2{SchemeKey: "legacy-oauth2-client", TokenURL: parsed.String(), ClientID: strings.TrimSpace(legacy.ClientID), CredentialField: "clientSecret", ClientAuthMethod: method, Scope: strings.TrimSpace(legacy.Scope), TokenParameters: map[string]string{}, Response: withResponseDefaults(TokenResponse{RenewalTokenPath: "refresh_token"}), Injection: withInjectionDefaults(TokenInjection{}), RefreshStrategy: RefreshToken},
	}, nil
}

func contractScheme(contract Contract, key string) (Scheme, bool) {
	for _, scheme := range contract.Schemes {
		if scheme.Key == key {
			return scheme, true
		}
	}
	return Scheme{}, false
}

func containsOption(options []Option, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}

func withResponseDefaults(value TokenResponse) TokenResponse {
	if value.AccessTokenPath == "" {
		value.AccessTokenPath = "access_token"
	}
	if value.ExpiresInPath == "" {
		value.ExpiresInPath = "expires_in"
	}
	return value
}

func withInjectionDefaults(value TokenInjection) TokenInjection {
	if value.HeaderName == "" {
		value.HeaderName = "Authorization"
	}
	if value.Prefix == "" {
		value.Prefix = "Bearer"
	}
	return value
}

func validOptionalJSONPath(value string) bool { return value == "" || validJSONPath(value) }
func validJSONPath(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) == 0 || len(parts) > 16 {
		return false
	}
	for _, part := range parts {
		if !contractKeyPattern.MatchString(part) {
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
		if !strings.ContainsRune("!#$%&'*+-.^_`|~", r) && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}

// ReadPath reads a dot-delimited path from an OAuth token response.
func ReadPath(document any, path string) (any, bool) {
	current := document
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func StringAt(document any, path string) string {
	if path == "" {
		return ""
	}
	value, ok := ReadPath(document, path)
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func Int64At(document any, path string) int64 {
	if path == "" {
		return 0
	}
	value, ok := ReadPath(document, path)
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

// CanonicalValues is useful for stable cache keys without retaining secrets.
func CanonicalValues(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(values[key])
		builder.WriteByte(0)
	}
	return builder.String()
}

func WrapInvalidConnection(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrInvalidConnection, err)
}
