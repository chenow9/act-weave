package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"actweave/backend/internal/serviceauth"
)

func TestSSRFRejectsUnsafeProtocolsAddressesDNSAndPorts(t *testing.T) {
	resolver := staticHostResolver{addresses: map[string][]net.IPAddr{
		"public.example":  {{IP: net.ParseIP("93.184.216.34")}},
		"private.example": {{IP: net.ParseIP("10.0.0.7")}},
		"mixed.example": {
			{IP: net.ParseIP("93.184.216.34")},
			{IP: net.ParseIP("169.254.169.254")},
		},
		"shared.example": {{IP: net.ParseIP("100.64.0.7")}},
	}}
	guard, err := NewHTTPNetworkGuard(EgressPolicy{
		AllowedHosts: []string{"public.example", "private.example", "mixed.example", "shared.example", "127.0.0.1"},
		AllowedPorts: []int{443},
	}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	unsafe := []string{
		"ftp://public.example/resource",
		"https://user:password@public.example/resource",
		"https://unlisted.example/resource",
		"https://public.example:8443/resource",
		"https://private.example/resource",
		"https://mixed.example/resource",
		"https://shared.example/resource",
		"https://127.0.0.1/resource",
	}
	for _, rawURL := range unsafe {
		target, parseErr := url.Parse(rawURL)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if err := guard.ValidateURL(context.Background(), target); ErrorCode(err) != ErrorCodeEgressDenied {
			t.Fatalf("expected SSRF rejection for %s, got %v", rawURL, err)
		}
	}
	allowed, _ := url.Parse("https://public.example/resource")
	if err := guard.ValidateURL(context.Background(), allowed); err != nil {
		t.Fatalf("expected allowlisted public endpoint, got %v", err)
	}
}

func TestSSRFAllowsOnlyExplicitPrivateCIDR(t *testing.T) {
	resolver := staticHostResolver{addresses: map[string][]net.IPAddr{
		"service.internal": {{IP: net.ParseIP("10.42.0.9")}},
	}}
	target, _ := url.Parse("https://service.internal:8443/v1")
	guard, err := NewHTTPNetworkGuard(EgressPolicy{
		AllowedHosts: []string{"service.internal"}, AllowedPorts: []int{8443},
		AllowedCIDRs: []string{"10.42.0.0/16"},
	}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.ValidateURL(context.Background(), target); err != nil {
		t.Fatalf("explicit private egress CIDR should be allowed: %v", err)
	}
}

func TestSSRFRevalidatesRedirectAndStripsSensitiveHeaders(t *testing.T) {
	var redirectedCredential string
	var serverPort string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/redirect":
			target := *request.URL
			target.Scheme = "http"
			target.Host = "localhost:" + serverPort
			target.Path = "/target"
			http.Redirect(writer, request, target.String(), http.StatusFound)
		case "/target":
			redirectedCredential = request.Header.Get("X-API-Key")
			writer.WriteHeader(http.StatusNoContent)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	parsedServer, _ := url.Parse(server.URL)
	serverPort = parsedServer.Port()
	port, _ := strconv.Atoi(parsedServer.Port())
	resolver := staticHostResolver{addresses: map[string][]net.IPAddr{
		"localhost": {{IP: net.ParseIP("127.0.0.1")}},
	}}
	guard, err := NewHTTPNetworkGuard(EgressPolicy{
		AllowedHosts: []string{"127.0.0.1", "localhost"}, AllowedPorts: []int{port},
		AllowedCIDRs: []string{"127.0.0.0/8"},
	}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	client, err := guard.ProtectClient(server.Client(), []string{"X-API-Key"})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/redirect", nil)
	request.Header.Set("X-API-Key", "redirect-secret")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("follow allowlisted redirect: %v", err)
	}
	response.Body.Close()
	if redirectedCredential != "" {
		t.Fatalf("credential leaked across redirect origin: %q", redirectedCredential)
	}

	blockedGuard, err := NewHTTPNetworkGuard(EgressPolicy{
		AllowedHosts: []string{"127.0.0.1"}, AllowedPorts: []int{port},
		AllowedCIDRs: []string{"127.0.0.0/8"},
	}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	blockedClient, _ := blockedGuard.ProtectClient(server.Client(), nil)
	if _, err := blockedClient.Get(server.URL + "/redirect"); ErrorCode(err) != ErrorCodeEgressDenied {
		t.Fatalf("expected redirect host revalidation failure, got %v", err)
	}
}

func TestSecretInjectionUsesHeaderAllowlistAndDoesNotSerializeCredential(t *testing.T) {
	source := &staticActiveSecretSource{value: []byte("top-secret-token")}
	injector, err := NewHTTPSecretInjector(source)
	if err != nil {
		t.Fatal(err)
	}
	connection := ConnectionSnapshot{
		ID: "connection-one", WorkspaceID: "workspace-one", ProviderID: "provider-one",
		Headers: map[string]string{"X-Tenant": "tenant-one"},
	}
	err = injector.WithInjectedConnection(context.Background(), connection, CredentialReference{
		WorkspaceID: "workspace-one", SecretID: "secret-one", AuthMode: "API_KEY",
		AuthConfig:               json.RawMessage(`{"headerName":"X-Vendor-Key","placement":"HEADER"}`),
		AllowedCredentialHeaders: []string{"X-Vendor-Key"},
	}, func(injected ConnectionSnapshot) error {
		if injected.Headers["X-Vendor-Key"] != "top-secret-token" ||
			!containsFold(injected.SensitiveHeaderNames, "X-Vendor-Key") {
			t.Fatalf("credential was not minimally injected: %+v", injected.SensitiveHeaderNames)
		}
		encoded, marshalErr := json.Marshal(injected)
		if marshalErr != nil {
			return marshalErr
		}
		if strings.Contains(string(encoded), "top-secret-token") {
			t.Fatalf("connection snapshot serialization leaked credential: %s", encoded)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inject API key: %v", err)
	}
	if source.calls != 1 || connection.Headers["X-Vendor-Key"] != "" {
		t.Fatalf("injector mutated source snapshot or skipped resolver: calls=%d headers=%v", source.calls, connection.Headers)
	}
}

func TestOAuthClientCredentialsFetchesCachesAndInjectsBearerToken(t *testing.T) {
	var calls int
	var gotGrant, gotScope, gotClientID, gotClientSecret string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse token request: %v", err)
		}
		gotGrant, gotScope = request.Form.Get("grant_type"), request.Form.Get("scope")
		gotClientID, gotClientSecret, _ = request.BasicAuth()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"access_token":"issued-token","expires_in":120}`))
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(parsed.Port())
	source := &staticActiveSecretSource{value: []byte("client-secret")}
	injector, err := NewHTTPSecretInjector(source)
	if err != nil {
		t.Fatal(err)
	}
	connection := ConnectionSnapshot{
		ID: "connection-one", WorkspaceID: "workspace-one", Headers: map[string]string{},
		EgressPolicy: EgressPolicy{AllowedCIDRs: []string{"127.0.0.0/8"}, AllowedPorts: []int{port}},
	}
	reference := CredentialReference{
		WorkspaceID: "workspace-one", SecretID: "secret-one", AuthMode: "OAUTH2_CLIENT",
		AuthConfig: json.RawMessage(`{"clientId":"client-one","tokenUrl":"` + server.URL + `","scope":"read"}`),
	}
	for range 2 {
		err = injector.WithInjectedConnection(context.Background(), connection, reference, func(injected ConnectionSnapshot) error {
			if got := injected.Headers["Authorization"]; got != "Bearer issued-token" {
				t.Fatalf("unexpected injected authorization: %q", got)
			}
			if !containsFold(injected.SensitiveHeaderNames, "Authorization") {
				t.Fatal("authorization header was not marked sensitive")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("inject oauth credential: %v", err)
		}
	}
	if calls != 1 || source.calls != 1 || gotGrant != "client_credentials" || gotScope != "read" || gotClientID != "client-one" || gotClientSecret != "client-secret" {
		t.Fatalf("unexpected OAuth token exchange: calls=%d secretCalls=%d grant=%q scope=%q clientID=%q", calls, source.calls, gotGrant, gotScope, gotClientID)
	}
}

func TestOAuthClientCredentialsSupportsClientSecretPost(t *testing.T) {
	var gotClientID, gotClientSecret string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse token request: %v", err)
		}
		gotClientID, gotClientSecret = request.Form.Get("client_id"), request.Form.Get("client_secret")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"access_token":"issued-token"}`))
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(parsed.Port())
	injector, _ := NewHTTPSecretInjector(&staticActiveSecretSource{value: []byte("client-secret")})
	err := injector.WithInjectedConnection(context.Background(), ConnectionSnapshot{
		ID: "connection-one", WorkspaceID: "workspace-one", Headers: map[string]string{},
		EgressPolicy: EgressPolicy{AllowedCIDRs: []string{"127.0.0.0/8"}, AllowedPorts: []int{port}},
	}, CredentialReference{
		WorkspaceID: "workspace-one", SecretID: "secret-one", AuthMode: "OAUTH2_CLIENT",
		AuthConfig: json.RawMessage(`{"clientId":"client-one","clientAuth":"client_secret_post","tokenUrl":"` + server.URL + `"}`),
	}, func(ConnectionSnapshot) error { return nil })
	if err != nil || gotClientID != "client-one" || gotClientSecret != "client-secret" {
		t.Fatalf("client_secret_post exchange failed: err=%v clientID=%q", err, gotClientID)
	}
}

func TestOAuthClientCredentialsUsesProviderDefinedAudienceResponseAndInjection(t *testing.T) {
	var calls int
	var gotGrant, gotAudience, gotClientID, gotClientSecret string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse token request: %v", err)
		}
		gotGrant, gotAudience = request.Form.Get("grant_type"), request.Form.Get("audience")
		gotClientID, gotClientSecret, _ = request.BasicAuth()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"payload":{"credential":"provider-issued-token","ttl":"120","kind":"ignored-by-explicit-prefix"}}`))
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(parsed.Port())
	contract := serviceauth.Contract{
		Version: serviceauth.ContractVersion, DefaultSchemeKey: "orders-oauth",
		Schemes: []serviceauth.Scheme{{
			Key: "orders-oauth", Type: serviceauth.SchemeOAuth2Client, DisplayName: "Orders OAuth2",
			Fields: []serviceauth.Field{
				{Key: "clientId", Label: "Client ID", Kind: serviceauth.FieldText, Required: true},
				{Key: "clientSecret", Label: "Client Secret", Kind: serviceauth.FieldSecret, Required: true},
				{Key: "audience", Label: "Audience", Kind: serviceauth.FieldText, Required: true},
			},
			OAuth2: &serviceauth.OAuth2Config{
				TokenURLTemplate: server.URL, ClientIDField: "clientId", CredentialField: "clientSecret",
				ClientAuthMethod: serviceauth.ClientSecretBasic,
				TokenParameters:  []serviceauth.TokenParameter{{Name: "audience", Field: "audience", Required: true}},
				Response: serviceauth.TokenResponse{
					AccessTokenPath: "payload.credential", TokenTypePath: "payload.kind", ExpiresInPath: "payload.ttl",
				},
				Injection:       serviceauth.TokenInjection{HeaderName: "X-Orders-Token", Prefix: "Platform"},
				RefreshStrategy: serviceauth.RefreshClientCredentials,
			},
		}},
	}
	driverConfig, err := json.Marshal(serviceauth.DriverConfig{Authentication: &contract})
	if err != nil {
		t.Fatal(err)
	}
	authConfig, err := json.Marshal(serviceauth.ConnectionConfig{SchemeKey: "orders-oauth", Values: map[string]string{
		"clientId": "orders-client", "audience": "orders-api",
	}})
	if err != nil {
		t.Fatal(err)
	}
	source := &staticActiveSecretSource{value: []byte("orders-client-secret")}
	injector, err := NewHTTPSecretInjector(source)
	if err != nil {
		t.Fatal(err)
	}
	err = injector.WithInjectedConnection(context.Background(), ConnectionSnapshot{
		ID: "connection-provider-contract", WorkspaceID: "workspace-one", Headers: map[string]string{},
		EgressPolicy: EgressPolicy{AllowedCIDRs: []string{"127.0.0.0/8"}, AllowedPorts: []int{port}},
	}, CredentialReference{
		WorkspaceID: "workspace-one", SecretID: "secret-provider-contract", SecretFingerprint: "fingerprint-v1",
		AuthMode: serviceauth.SchemeOAuth2Client, AuthConfig: authConfig, ProviderDriverConfig: driverConfig,
	}, func(injected ConnectionSnapshot) error {
		if got := injected.Headers["X-Orders-Token"]; got != "Platform provider-issued-token" {
			t.Fatalf("unexpected Provider-defined credential injection: %q", got)
		}
		if injected.Headers["Authorization"] != "" || !containsFold(injected.SensitiveHeaderNames, "X-Orders-Token") {
			t.Fatalf("credential was not confined to Provider-defined header: headers=%v sensitive=%v", injected.Headers, injected.SensitiveHeaderNames)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inject Provider-defined OAuth credential: %v", err)
	}
	if calls != 1 || source.calls != 1 || gotGrant != "client_credentials" || gotAudience != "orders-api" ||
		gotClientID != "orders-client" || gotClientSecret != "orders-client-secret" {
		t.Fatalf("unexpected Provider-defined token exchange: calls=%d secretCalls=%d grant=%q audience=%q clientID=%q", calls, source.calls, gotGrant, gotAudience, gotClientID)
	}
}

func TestOAuthClientCredentialsRefreshesWithReturnedRefreshToken(t *testing.T) {
	var grants []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse token request: %v", err)
		}
		grants = append(grants, request.Form.Get("grant_type"))
		writer.Header().Set("Content-Type", "application/json")
		if request.Form.Get("grant_type") == "client_credentials" {
			_, _ = writer.Write([]byte(`{"access_token":"first-token","refresh_token":"refresh-one","expires_in":1}`))
			return
		}
		if request.Form.Get("refresh_token") != "refresh-one" {
			t.Fatalf("unexpected refresh token: %q", request.Form.Get("refresh_token"))
		}
		_, _ = writer.Write([]byte(`{"access_token":"refreshed-token","expires_in":120}`))
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(parsed.Port())
	injector, _ := NewHTTPSecretInjector(&staticActiveSecretSource{value: []byte("client-secret")})
	connection := ConnectionSnapshot{ID: "connection-one", WorkspaceID: "workspace-one", Headers: map[string]string{}, EgressPolicy: EgressPolicy{AllowedCIDRs: []string{"127.0.0.0/8"}, AllowedPorts: []int{port}}}
	reference := CredentialReference{WorkspaceID: "workspace-one", SecretID: "secret-one", AuthMode: "OAUTH2_CLIENT", AuthConfig: json.RawMessage(`{"clientId":"client-one","clientAuth":"client_secret_post","tokenUrl":"` + server.URL + `"}`)}
	for _, expected := range []string{"first-token", "refreshed-token"} {
		if err := injector.WithInjectedConnection(context.Background(), connection, reference, func(injected ConnectionSnapshot) error {
			if injected.Headers["Authorization"] != "Bearer "+expected {
				t.Fatalf("expected %s, got %q", expected, injected.Headers["Authorization"])
			}
			return nil
		}); err != nil {
			t.Fatalf("inject oauth token: %v", err)
		}
	}
	if strings.Join(grants, ",") != "client_credentials,refresh_token" {
		t.Fatalf("unexpected grant sequence: %v", grants)
	}
}

func TestSecretInjectionRejectsUnsafeModesHeadersAndValues(t *testing.T) {
	tests := []struct {
		name      string
		reference CredentialReference
		value     string
	}{
		{name: "query placement", value: "secret", reference: CredentialReference{
			WorkspaceID: "workspace-one", SecretID: "secret-one", AuthMode: "API_KEY",
			AuthConfig: json.RawMessage(`{"headerName":"X-API-Key","placement":"QUERY"}`),
		}},
		{name: "forbidden host header", value: "secret", reference: CredentialReference{
			WorkspaceID: "workspace-one", SecretID: "secret-one", AuthMode: "API_KEY",
			AuthConfig: json.RawMessage(`{"headerName":"Host"}`), AllowedCredentialHeaders: []string{"Host"},
		}},
		{name: "unlisted custom header", value: "secret", reference: CredentialReference{
			WorkspaceID: "workspace-one", SecretID: "secret-one", AuthMode: "API_KEY",
			AuthConfig: json.RawMessage(`{"headerName":"X-Unlisted-Key"}`),
		}},
		{name: "header injection", value: "secret\r\nX-Evil: yes", reference: CredentialReference{
			WorkspaceID: "workspace-one", SecretID: "secret-one", AuthMode: "BEARER_TOKEN",
		}},
		{name: "unknown auth", value: "secret", reference: CredentialReference{
			WorkspaceID: "workspace-one", SecretID: "secret-one", AuthMode: "OAUTH_MAGIC",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &staticActiveSecretSource{value: []byte(test.value)}
			injector, _ := NewHTTPSecretInjector(source)
			err := injector.WithInjectedConnection(context.Background(), ConnectionSnapshot{
				ID: "connection-one", WorkspaceID: "workspace-one", Headers: map[string]string{},
			}, test.reference, func(ConnectionSnapshot) error {
				return errors.New("callback must not execute")
			})
			if ErrorCode(err) != ErrorCodeCredential || strings.Contains(err.Error(), test.value) {
				t.Fatalf("expected stable credential rejection without value leak, got %v", err)
			}
		})
	}

	source := &staticActiveSecretSource{err: errors.New("vault failure included-secret")}
	injector, _ := NewHTTPSecretInjector(source)
	err := injector.WithInjectedConnection(context.Background(), ConnectionSnapshot{
		ID: "connection-one", WorkspaceID: "workspace-one", Headers: map[string]string{},
	}, CredentialReference{
		WorkspaceID: "workspace-one", SecretID: "secret-one", AuthMode: "API_KEY",
	}, func(ConnectionSnapshot) error { return nil })
	if ErrorCode(err) != ErrorCodeCredential || strings.Contains(err.Error(), "included-secret") {
		t.Fatalf("secret source failure leaked detail: %v", err)
	}
}

type staticHostResolver struct {
	addresses map[string][]net.IPAddr
}

func (resolver staticHostResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	addresses, exists := resolver.addresses[strings.ToLower(host)]
	if !exists {
		return nil, errors.New("host not found")
	}
	return append([]net.IPAddr(nil), addresses...), nil
}

type staticActiveSecretSource struct {
	value []byte
	err   error
	calls int
}

func (source *staticActiveSecretSource) WithActiveSecret(
	_ context.Context,
	_, _ string,
	use func([]byte) error,
) error {
	source.calls++
	if source.err != nil {
		return source.err
	}
	return use(source.value)
}

func containsFold(values []string, candidate string) bool {
	for _, value := range values {
		if strings.EqualFold(value, candidate) {
			return true
		}
	}
	return false
}
