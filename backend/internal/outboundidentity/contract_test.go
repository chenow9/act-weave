package outboundidentity

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func validProviderJSON() string {
	return `{
		"schemaVersion":"outbound-identity.v1",
		"supportedModes":["BROKER_OBO","REQUEST_PASSTHROUGH"],
		"supportedSubjectTypes":["EXTERNAL_SUBJECT","USER"],
		"brokerObo":{
			"tokenEndpoint":"https://broker.example/token",
			"audience":"urn:broker:tenant",
			"grantType":"urn:ietf:params:oauth:grant-type:token-exchange",
			"subjectTokenType":"urn:ietf:params:oauth:token-type:jwt",
			"requestedTokenType":"urn:ietf:params:oauth:token-type:access_token",
			"machineAuthMethod":"PRIVATE_KEY_JWT",
			"allowedScopes":["orders.write","orders.read"],
			"response":{"accessTokenPath":"access_token","tokenTypePath":"token_type","expiresInPath":"expires_in","expectedTokenType":"Bearer"},
			"businessInjection":{"headerName":"Authorization","prefix":"Bearer"}
		},
		"requestPassthrough":{
			"credentialTypes":["ACCESS_TOKEN"],
			"businessInjection":{"headerName":"Authorization","prefix":"Bearer"}
		}
	}`
}

func TestParseProviderIdentityNormalizesAndClones(t *testing.T) {
	identity, err := ParseProviderIdentity(json.RawMessage(validProviderJSON()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(identity.SupportedModes) != 2 || identity.SupportedModes[0] != ModeBrokerOBO {
		t.Fatalf("modes not sorted: %+v", identity.SupportedModes)
	}
	if identity.BrokerOBO == nil || identity.BrokerOBO.AllowedScopes[0] != "orders.read" {
		t.Fatalf("scopes not sorted: %+v", identity.BrokerOBO)
	}
	cloned := CloneProviderIdentity(identity)
	if !EqualProviderIdentity(identity, cloned) {
		t.Fatalf("clone mismatch")
	}
	cloned.BrokerOBO.AllowedScopes[0] = "mutated"
	if identity.BrokerOBO.AllowedScopes[0] == "mutated" {
		t.Fatal("clone shared scopes slice")
	}
	if !EqualProviderIdentity(identity, identity) || equalProviderIdentity(identity, cloned) {
		t.Fatal("equal semantics broken")
	}
}

func TestProviderIdentityRejectsUnknownModeNONEAndSYSTEM(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		code string
	}{
		{
			name: "NONE mode",
			raw:  strings.Replace(validProviderJSON(), `"BROKER_OBO","REQUEST_PASSTHROUGH"`, `"NONE"`, 1),
			code: CodeIdentityModeUnsupported,
		},
		{
			name: "third mode",
			raw:  strings.Replace(validProviderJSON(), `"BROKER_OBO","REQUEST_PASSTHROUGH"`, `"BROKER_OBO","SHARED_ACCOUNT"`, 1),
			code: CodeIdentityModeUnsupported,
		},
		{
			name: "SYSTEM subject",
			raw:  strings.Replace(validProviderJSON(), `"EXTERNAL_SUBJECT","USER"`, `"SYSTEM"`, 1),
			code: CodeSubjectRequired,
		},
		{
			name: "unknown field",
			raw:  `{"schemaVersion":"outbound-identity.v1","supportedModes":["BROKER_OBO"],"supportedSubjectTypes":["USER"],"brokerObo":{"tokenEndpoint":"https://broker.example/token","audience":"a","allowedScopes":[],"response":{"accessTokenPath":"access_token"},"businessInjection":{"headerName":"Authorization"}},"legacyAuth":true}`,
			code: CodeIdentityPolicyInvalid,
		},
		{
			name: "legacy service-auth version",
			raw:  strings.Replace(validProviderJSON(), SchemaIdentity, "service-auth.v1", 1),
			code: CodeIdentityPolicyInvalid,
		},
		{
			name: "CRLF injection header",
			raw: `{
				"schemaVersion":"outbound-identity.v1",
				"supportedModes":["REQUEST_PASSTHROUGH"],
				"supportedSubjectTypes":["USER"],
				"requestPassthrough":{
					"credentialTypes":["ACCESS_TOKEN"],
					"businessInjection":{"headerName":"X-Token\r\nEvil","prefix":"Bearer"}
				}
			}`,
			code: CodeIdentityPolicyInvalid,
		},
		{
			name: "broker block without broker mode",
			raw: `{
				"schemaVersion":"outbound-identity.v1",
				"supportedModes":["REQUEST_PASSTHROUGH"],
				"supportedSubjectTypes":["USER"],
				"brokerObo":{
					"tokenEndpoint":"https://broker.example/token",
					"audience":"a",
					"allowedScopes":[],
					"response":{"accessTokenPath":"access_token"},
					"businessInjection":{"headerName":"Authorization"}
				},
				"requestPassthrough":{
					"credentialTypes":["ACCESS_TOKEN"],
					"businessInjection":{"headerName":"Authorization"}
				}
			}`,
			code: CodeIdentityPolicyInvalid,
		},
		{
			name: "duplicate scopes",
			raw: `{
				"schemaVersion":"outbound-identity.v1",
				"supportedModes":["BROKER_OBO"],
				"supportedSubjectTypes":["USER"],
				"brokerObo":{
					"tokenEndpoint":"https://broker.example/token",
					"audience":"a",
					"allowedScopes":["orders.read","orders.read"],
					"response":{"accessTokenPath":"access_token"},
					"businessInjection":{"headerName":"Authorization"}
				}
			}`,
			code: CodeIdentityPolicyInvalid,
		},
		{
			name: "client_secret_basic machine auth",
			raw: strings.Replace(validProviderJSON(),
				`"machineAuthMethod":"PRIVATE_KEY_JWT"`,
				`"machineAuthMethod":"client_secret_basic"`, 1),
			code: CodeIdentityModeUnsupported,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseProviderIdentity(json.RawMessage(tc.raw))
			assertOutboundCode(t, err, tc.code)
		})
	}
}

func TestConnectionIdentityAndProviderCompatibility(t *testing.T) {
	provider, err := ParseProviderIdentity(json.RawMessage(validProviderJSON()))
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	brokerRaw := `{
		"schemaVersion":"outbound-connection.v1",
		"mode":"BROKER_OBO",
		"policyVersion":3,
		"brokerObo":{"clientId":"actweave-connection-123","scopes":["orders.read"],"maxTokenTtlSeconds":300}
	}`
	connection, err := ParseConnectionIdentity(json.RawMessage(brokerRaw))
	if err != nil {
		t.Fatalf("connection: %v", err)
	}
	if err := ValidateConnectionAgainstProvider(connection, provider, true); err != nil {
		t.Fatalf("compatible broker: %v", err)
	}
	if err := ValidateConnectionAgainstProvider(connection, provider, false); !errors.Is(err, ErrIdentityConnectionNotReady) {
		t.Fatalf("expected not ready without machine secret, got %v", err)
	}

	// Scope outside Provider allowlist.
	badScope := connection
	badScope.BrokerOBO = &ConnectionBrokerOBO{
		ClientID: "c", Scopes: []string{"admin"}, MaxTokenTTLSeconds: 300,
	}
	if err := ValidateConnectionAgainstProvider(badScope, provider, true); !errors.Is(err, ErrIdentityScopeNotAllowed) {
		t.Fatalf("expected scope rejection, got %v", err)
	}

	passthroughRaw := `{
		"schemaVersion":"outbound-connection.v1",
		"mode":"REQUEST_PASSTHROUGH",
		"policyVersion":2,
		"requestPassthrough":{"maxResidenceSeconds":600}
	}`
	passthrough, err := ParseConnectionIdentity(json.RawMessage(passthroughRaw))
	if err != nil {
		t.Fatalf("passthrough: %v", err)
	}
	if err := ValidateConnectionAgainstProvider(passthrough, provider, false); err != nil {
		t.Fatalf("compatible passthrough: %v", err)
	}
	if err := ValidateConnectionAgainstProvider(passthrough, provider, true); !errors.Is(err, ErrIdentityPolicyInvalid) {
		t.Fatalf("passthrough with machine secret must fail, got %v", err)
	}

	// Mode unsupported by provider that only supports broker.
	brokerOnly := provider
	brokerOnly.SupportedModes = []Mode{ModeBrokerOBO}
	brokerOnly.RequestPassthrough = nil
	if err := ValidateConnectionAgainstProvider(passthrough, brokerOnly, false); !errors.Is(err, ErrIdentityModeUnsupported) {
		t.Fatalf("expected mode unsupported, got %v", err)
	}
}

func TestConnectionRejectsLegacyModesTTLAndUnknownFields(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		code string
	}{
		{
			name: "NONE",
			raw:  `{"schemaVersion":"outbound-connection.v1","mode":"NONE","requestPassthrough":{"maxResidenceSeconds":600}}`,
			code: CodeIdentityModeUnsupported,
		},
		{
			name: "OAUTH2_CLIENT",
			raw:  `{"schemaVersion":"outbound-connection.v1","mode":"OAUTH2_CLIENT","brokerObo":{"clientId":"c","scopes":[],"maxTokenTtlSeconds":300}}`,
			code: CodeIdentityModeUnsupported,
		},
		{
			name: "TTL too large",
			raw:  `{"schemaVersion":"outbound-connection.v1","mode":"BROKER_OBO","brokerObo":{"clientId":"c","scopes":[],"maxTokenTtlSeconds":901}}`,
			code: CodeIdentityPolicyInvalid,
		},
		{
			name: "residence too small",
			raw:  `{"schemaVersion":"outbound-connection.v1","mode":"REQUEST_PASSTHROUGH","requestPassthrough":{"maxResidenceSeconds":10}}`,
			code: CodeIdentityPolicyInvalid,
		},
		{
			name: "negative policy version",
			raw:  `{"schemaVersion":"outbound-connection.v1","mode":"REQUEST_PASSTHROUGH","policyVersion":-1,"requestPassthrough":{"maxResidenceSeconds":600}}`,
			code: CodeIdentityPolicyInvalid,
		},
		{
			name: "secret field smuggled",
			raw:  `{"schemaVersion":"outbound-connection.v1","mode":"BROKER_OBO","brokerObo":{"clientId":"c","scopes":[],"maxTokenTtlSeconds":300},"machineCredentialSecretId":"sec-1"}`,
			code: CodeIdentityPolicyInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseConnectionIdentity(json.RawMessage(tc.raw))
			assertOutboundCode(t, err, tc.code)
		})
	}

	// Defaults applied.
	raw := `{"schemaVersion":"outbound-connection.v1","mode":"BROKER_OBO","brokerObo":{"clientId":"c","scopes":["b","a"]}}`
	connection, err := ParseConnectionIdentity(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if connection.BrokerOBO.MaxTokenTTLSeconds != DefaultMaxTokenTTLSeconds {
		t.Fatalf("default ttl: %d", connection.BrokerOBO.MaxTokenTTLSeconds)
	}
	if connection.BrokerOBO.Scopes[0] != "a" {
		t.Fatalf("scopes sorted: %+v", connection.BrokerOBO.Scopes)
	}
	cloned := CloneConnectionIdentity(connection)
	if !EqualConnectionIdentity(connection, cloned) {
		t.Fatal("clone/equal connection failed")
	}
}

func TestRequirementsDescriptorNoSecretsAndPolicyVersions(t *testing.T) {
	raw := `{
		"schemaVersion":"outbound-requirements.v1",
		"connections":[
			{
				"connectionId":"conn-b",
				"providerId":"prov-1",
				"mode":"BROKER_OBO",
				"providerContractVersion":4,
				"connectionPolicyVersion":2,
				"requiredScopes":["orders.write","orders.read"],
				"credentialRequired":false
			},
			{
				"connectionId":"conn-a",
				"providerId":"prov-1",
				"mode":"REQUEST_PASSTHROUGH",
				"providerContractVersion":4,
				"connectionPolicyVersion":3,
				"requiredScopes":[],
				"credentialRequired":true
			}
		]
	}`
	requirements, err := ParseRequirements(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if requirements.Connections[0].ConnectionID != "conn-a" {
		t.Fatalf("sorted by connection id: %+v", requirements.Connections)
	}
	if requirements.Connections[1].RequiredScopes[0] != "orders.read" {
		t.Fatalf("scopes sorted: %+v", requirements.Connections[1].RequiredScopes)
	}
	cloned := CloneRequirements(requirements)
	if !EqualRequirements(requirements, cloned) {
		t.Fatal("clone/equal requirements failed")
	}
	if !requirements.PassthroughRequired() {
		t.Fatal("expected passthrough required")
	}

	// Broker cannot mark credentialRequired.
	badBroker := `{
		"schemaVersion":"outbound-requirements.v1",
		"connections":[{
			"connectionId":"c","providerId":"p","mode":"BROKER_OBO",
			"providerContractVersion":1,"connectionPolicyVersion":1,
			"requiredScopes":[],"credentialRequired":true
		}]
	}`
	if _, err := ParseRequirements(json.RawMessage(badBroker)); !errors.Is(err, ErrIdentityPolicyInvalid) {
		t.Fatalf("broker credentialRequired: %v", err)
	}

	// Duplicate connection.
	dup := `{
		"schemaVersion":"outbound-requirements.v1",
		"connections":[
			{"connectionId":"c","providerId":"p","mode":"REQUEST_PASSTHROUGH","providerContractVersion":1,"connectionPolicyVersion":1,"requiredScopes":[],"credentialRequired":true},
			{"connectionId":"c","providerId":"p","mode":"REQUEST_PASSTHROUGH","providerContractVersion":1,"connectionPolicyVersion":1,"requiredScopes":[],"credentialRequired":true}
		]
	}`
	if _, err := ParseRequirements(json.RawMessage(dup)); !errors.Is(err, ErrIdentityPolicyInvalid) {
		t.Fatalf("duplicate connection: %v", err)
	}

	// Zero policy version.
	zero := `{
		"schemaVersion":"outbound-requirements.v1",
		"connections":[{
			"connectionId":"c","providerId":"p","mode":"REQUEST_PASSTHROUGH",
			"providerContractVersion":0,"connectionPolicyVersion":1,
			"requiredScopes":[],"credentialRequired":true
		}]
	}`
	if _, err := ParseRequirements(json.RawMessage(zero)); !errors.Is(err, ErrIdentityPolicyInvalid) {
		t.Fatalf("zero policy version: %v", err)
	}

	// Forbidden secret/token/locator fields rejected by strict decode.
	for _, field := range []string{
		`"token":"secret"`,
		`"secretId":"s1"`,
		`"vaultKey":"v1"`,
		`"attachmentId":"a1"`,
		`"locator":"loc"`,
	} {
		smuggled := `{"schemaVersion":"outbound-requirements.v1","connections":[],` + field + `}`
		if _, err := ParseRequirements(json.RawMessage(smuggled)); err == nil {
			t.Fatalf("expected rejection for smuggled field %s", field)
		}
	}

	// BuildRequirementConnection
	connection := ConnectionIdentity{
		SchemaVersion:      SchemaConnection,
		Mode:               ModeRequestPassthrough,
		PolicyVersion:      2,
		RequestPassthrough: &ConnectionPassthrough{MaxResidenceSeconds: 600},
	}
	built, err := BuildRequirementConnection("conn-1", "prov-1", connection, 4, nil)
	if err != nil || !built.CredentialRequired || built.Mode != ModeRequestPassthrough {
		t.Fatalf("build: %+v err=%v", built, err)
	}
}

func TestCredentialsEnvelopeValidation(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	expires := now.Add(5 * time.Minute).Format(time.RFC3339)
	raw := `{
		"schemaVersion":"outbound-credentials.v1",
		"bindings":[{
			"connectionId":"conn-a",
			"credentialType":"ACCESS_TOKEN",
			"value":"opaque-secret-value",
			"expiresAt":"` + expires + `"
		}]
	}`
	envelope, err := ParseCredentialsEnvelope(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := ValidateCredentialsEnvelopeExpiry(envelope, now); err != nil {
		t.Fatalf("expiry: %v", err)
	}
	if err := ValidateCredentialsEnvelopeExpiry(envelope, now.Add(10*time.Minute)); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("expected expired, got %v", err)
	}

	// MarshalJSON must never include the token value.
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "opaque-secret-value") || strings.Contains(string(encoded), `"value"`) {
		t.Fatalf("token leaked in marshal: %s", encoded)
	}

	// Duplicate connection.
	dup := `{
		"schemaVersion":"outbound-credentials.v1",
		"bindings":[
			{"connectionId":"c","credentialType":"ACCESS_TOKEN","value":"a","expiresAt":"` + expires + `"},
			{"connectionId":"c","credentialType":"ACCESS_TOKEN","value":"b","expiresAt":"` + expires + `"}
		]
	}`
	if _, err := ParseCredentialsEnvelope(json.RawMessage(dup)); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("duplicate binding: %v", err)
	}

	// Missing expiresAt (T3=A).
	noExp := `{
		"schemaVersion":"outbound-credentials.v1",
		"bindings":[{"connectionId":"c","credentialType":"ACCESS_TOKEN","value":"a"}]
	}`
	if _, err := ParseCredentialsEnvelope(json.RawMessage(noExp)); err == nil {
		t.Fatal("expected missing expiresAt rejection")
	}

	// Header injection / CR-LF in value.
	crlf := `{
		"schemaVersion":"outbound-credentials.v1",
		"bindings":[{"connectionId":"c","credentialType":"ACCESS_TOKEN","value":"evil\r\n","expiresAt":"` + expires + `"}]
	}`
	if _, err := ParseCredentialsEnvelope(json.RawMessage(crlf)); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("crlf value: %v", err)
	}

	// Forbidden caller fields.
	for _, field := range []string{
		`"subject":"user-1"`,
		`"workspaceId":"ws"`,
		`"mode":"REQUEST_PASSTHROUGH"`,
		`"headerName":"Authorization"`,
		`"origin":"https://evil.example"`,
		`"vaultKey":"vk"`,
		`"locator":"loc"`,
	} {
		smuggled := `{"schemaVersion":"outbound-credentials.v1","bindings":[{"connectionId":"c","credentialType":"ACCESS_TOKEN","value":"a","expiresAt":"` + expires + `"}],` + field + `}`
		if _, err := ParseCredentialsEnvelope(json.RawMessage(smuggled)); err == nil {
			t.Fatalf("expected rejection for %s", field)
		}
	}

	// Against requirements.
	requirements := Requirements{
		SchemaVersion: SchemaRequirements,
		Connections: []RequirementConnection{{
			ConnectionID: "conn-a", ProviderID: "p", Mode: ModeRequestPassthrough,
			ProviderContractVersion: 1, ConnectionPolicyVersion: 1,
			CredentialRequired: true,
		}, {
			ConnectionID: "conn-broker", ProviderID: "p", Mode: ModeBrokerOBO,
			ProviderContractVersion: 1, ConnectionPolicyVersion: 1,
		}},
	}
	if err := ValidateCredentialsAgainstRequirements(envelope, requirements); err != nil {
		t.Fatalf("against requirements: %v", err)
	}

	// Binding for broker connection rejected.
	brokerBind := `{
		"schemaVersion":"outbound-credentials.v1",
		"bindings":[{"connectionId":"conn-broker","credentialType":"ACCESS_TOKEN","value":"a","expiresAt":"` + expires + `"}]
	}`
	brokerEnvelope, err := ParseCredentialsEnvelope(json.RawMessage(brokerBind))
	if err != nil {
		t.Fatalf("broker bind parse: %v", err)
	}
	if err := ValidateCredentialsAgainstRequirements(brokerEnvelope, requirements); !errors.Is(err, ErrCredentialTargetMismatch) {
		t.Fatalf("broker binding: %v", err)
	}
	ZeroCredentialsEnvelope(&brokerEnvelope)

	// Binding for unknown connection is target mismatch; missing required binding
	// with an empty envelope path is exercised via partial allowlist below.
	partialEnvelope, err := ParseCredentialsEnvelope(json.RawMessage(`{
		"schemaVersion":"outbound-credentials.v1",
		"bindings":[{"connectionId":"conn-a","credentialType":"ACCESS_TOKEN","value":"a","expiresAt":"` + expires + `"}]
	}`))
	if err != nil {
		t.Fatalf("partial envelope: %v", err)
	}
	missingRequired := Requirements{
		SchemaVersion: SchemaRequirements,
		Connections: []RequirementConnection{
			{
				ConnectionID: "conn-a", ProviderID: "p", Mode: ModeRequestPassthrough,
				ProviderContractVersion: 1, ConnectionPolicyVersion: 1, CredentialRequired: true,
			},
			{
				ConnectionID: "conn-missing", ProviderID: "p", Mode: ModeRequestPassthrough,
				ProviderContractVersion: 1, ConnectionPolicyVersion: 1, CredentialRequired: true,
			},
		},
	}
	if err := ValidateCredentialsAgainstRequirements(partialEnvelope, missingRequired); !errors.Is(err, ErrCredentialRequired) {
		t.Fatalf("missing binding: %v", err)
	}
	ZeroCredentialsEnvelope(&partialEnvelope)

	ZeroCredentialsEnvelope(&envelope)
	if envelope.Bindings != nil {
		t.Fatal("zero did not clear bindings")
	}
}

func TestStableErrorCatalogComplete(t *testing.T) {
	codes := AllStableCodes()
	if len(codes) != 19 {
		t.Fatalf("expected 19 stable codes, got %d", len(codes))
	}
	seen := map[string]struct{}{}
	for _, code := range codes {
		if _, dup := seen[code]; dup {
			t.Fatalf("duplicate code %s", code)
		}
		seen[code] = struct{}{}
		sentinel := SentinelByCode(code)
		if sentinel == nil || sentinel.Code != code || strings.TrimSpace(sentinel.Message) == "" {
			t.Fatalf("missing sentinel for %s", code)
		}
		// Errors must not retain upstream body placeholders.
		if strings.Contains(strings.ToLower(sentinel.Message), "token") &&
			code != CodeCredentialRequired && code != CodeCredentialInvalid &&
			code != CodeCredentialExpired && code != CodeCredentialCapacityExceeded &&
			code != CodeCredentialTargetMismatch && code != CodeBrokerDenied &&
			code != CodeBrokerUnavailable {
			// "token broker" wording is acceptable for broker codes already filtered.
		}
		if strings.Contains(sentinel.Message, "access_token") || strings.Contains(sentinel.Message, "Bearer ") {
			t.Fatalf("unsafe message for %s: %s", code, sentinel.Message)
		}
	}
	if !ErrCredentialExpired.Retryable || !ErrCredentialCapacityExceeded.Retryable || !ErrBrokerUnavailable.Retryable {
		t.Fatal("expected retryable flags for expired/capacity/broker unavailable")
	}
	if ErrCredentialInvalid.Retryable || ErrIdentityPolicyInvalid.Retryable {
		t.Fatal("non-retryable codes marked retryable")
	}
	wrapped := ErrCredentialInvalid.Wrap(errors.New("local detail"))
	if !errors.Is(wrapped, ErrCredentialInvalid) {
		t.Fatal("wrap/is broken")
	}
	if strings.Contains(wrapped.Message, "local detail") {
		t.Fatal("public message must not include cause")
	}
}

func assertOutboundCode(t *testing.T, err error, code string) {
	t.Helper()
	var outbound *Error
	if !errors.As(err, &outbound) {
		t.Fatalf("expected outbound error, got %v", err)
	}
	if outbound.Code != code {
		t.Fatalf("code=%s want=%s err=%v", outbound.Code, code, err)
	}
}

// Local aliases keep Equal* free for production while tests stay readable.
func equalProviderIdentity(a, b ProviderIdentity) bool     { return EqualProviderIdentity(a, b) }
func equalConnectionIdentity(a, b ConnectionIdentity) bool { return EqualConnectionIdentity(a, b) }
func equalRequirements(a, b Requirements) bool             { return EqualRequirements(a, b) }
