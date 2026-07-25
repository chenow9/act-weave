package serviceauth

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestResolveProviderOwnedOAuth2Contract(t *testing.T) {
	driver := json.RawMessage(`{
		"authentication": {
			"version":"service-auth.v1",
			"defaultSchemeKey":"oauth2-client",
			"schemes":[{
				"key":"oauth2-client","type":"OAUTH2_CLIENT","displayName":"Platform OAuth",
				"fields":[
					{"key":"tenantId","label":"Tenant","kind":"TEXT","required":true},
					{"key":"clientId","label":"Client ID","kind":"TEXT","required":true},
					{"key":"clientSecret","label":"Client Secret","kind":"SECRET","required":true},
					{"key":"audience","label":"Audience","kind":"TEXT","required":true},
					{"key":"scope","label":"Scope","kind":"TEXT"}
				],
				"oauth2":{
					"tokenUrlTemplate":"https://login.example/{{tenantId}}/oauth/token",
					"clientIdField":"clientId","credentialField":"clientSecret",
					"clientAuthMethod":"client_secret_post","scopeField":"scope",
					"tokenParameters":[{"name":"audience","field":"audience","required":true}],
					"response":{"accessTokenPath":"data.access_token","expiresInPath":"data.expires_in"},
					"injection":{"headerName":"X-Platform-Token","prefix":"Token"},
					"refreshStrategy":"CLIENT_CREDENTIALS"
				}
			}]
		}
	}`)
	connection := json.RawMessage(`{"schemeKey":"oauth2-client","values":{"tenantId":"tenant one","clientId":"client-1","audience":"orders","scope":"read write"}}`)
	resolved, err := Resolve(driver, connection, SchemeOAuth2Client, true)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.OAuth2 == nil || resolved.OAuth2.TokenURL != "https://login.example/tenant%20one/oauth/token" ||
		resolved.OAuth2.ClientID != "client-1" || resolved.OAuth2.TokenParameters["audience"] != "orders" ||
		resolved.OAuth2.Response.AccessTokenPath != "data.access_token" || resolved.OAuth2.Injection.HeaderName != "X-Platform-Token" {
		t.Fatalf("unexpected resolution: %+v", resolved)
	}
}

func TestContractRejectsConnectionControlledTokenHostAndSecretValue(t *testing.T) {
	contract := Contract{
		Version: ContractVersion, DefaultSchemeKey: "oauth",
		Schemes: []Scheme{{Key: "oauth", Type: SchemeOAuth2Client, DisplayName: "OAuth", Fields: []Field{
			{Key: "host", Label: "Host", Kind: FieldText, Required: true},
			{Key: "clientId", Label: "Client ID", Kind: FieldText, Required: true},
			{Key: "clientSecret", Label: "Secret", Kind: FieldSecret, Required: true},
		}, OAuth2: &OAuth2Config{
			TokenURLTemplate: "https://{{host}}/token", ClientIDField: "clientId", CredentialField: "clientSecret", ClientAuthMethod: ClientSecretBasic,
			Response: TokenResponse{AccessTokenPath: "access_token"}, Injection: TokenInjection{HeaderName: "Authorization"},
		}}},
	}
	if err := ValidateContract(contract); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("expected dynamic host rejection, got %v", err)
	}

	contract.Schemes[0].OAuth2.TokenURLTemplate = "https://auth.example/{{host}}/token"
	driver, _ := json.Marshal(DriverConfig{Authentication: &contract})
	connection := json.RawMessage(`{"schemeKey":"oauth","values":{"host":"tenant","clientId":"client","clientSecret":"raw-secret"}}`)
	if _, err := Resolve(driver, connection, SchemeOAuth2Client, true); !errors.Is(err, ErrInvalidConnection) {
		t.Fatalf("expected secret-in-values rejection, got %v", err)
	}
}

func TestOAuthContractRequiresCredentialsAndRejectsReservedTokenParameters(t *testing.T) {
	valid := Contract{
		Version: ContractVersion, DefaultSchemeKey: "oauth",
		Schemes: []Scheme{{Key: "oauth", Type: SchemeOAuth2Client, DisplayName: "OAuth", Fields: []Field{
			{Key: "clientId", Label: "Client ID", Kind: FieldText, Required: true},
			{Key: "clientSecret", Label: "Secret", Kind: FieldSecret, Required: true},
		}, OAuth2: &OAuth2Config{
			TokenURLTemplate: "https://auth.example/token", ClientIDField: "clientId", CredentialField: "clientSecret", ClientAuthMethod: ClientSecretBasic,
			Response: TokenResponse{AccessTokenPath: "access_token"}, Injection: TokenInjection{HeaderName: "Authorization"},
		}}},
	}
	if err := ValidateContract(valid); err != nil {
		t.Fatalf("valid contract: %v", err)
	}

	optionalClientID := valid
	optionalClientID.Schemes = append([]Scheme(nil), valid.Schemes...)
	optionalClientID.Schemes[0].Fields = append([]Field(nil), valid.Schemes[0].Fields...)
	optionalClientID.Schemes[0].Fields[0].Required = false
	if err := ValidateContract(optionalClientID); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("expected optional client ID rejection, got %v", err)
	}

	reservedParameter := valid
	reservedParameter.Schemes = append([]Scheme(nil), valid.Schemes...)
	oauth := *valid.Schemes[0].OAuth2
	oauth.TokenParameters = []TokenParameter{{Name: "grant_type", Value: "password"}}
	reservedParameter.Schemes[0].OAuth2 = &oauth
	if err := ValidateContract(reservedParameter); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("expected reserved OAuth parameter rejection, got %v", err)
	}
}

func TestLegacyOAuth2RemainsCompatible(t *testing.T) {
	resolved, err := Resolve(json.RawMessage(`{}`), json.RawMessage(`{"tokenUrl":"https://auth.example/token","clientId":"legacy","clientAuth":"client_secret_basic","scope":"read"}`), SchemeOAuth2Client, true)
	if err != nil || resolved.OAuth2 == nil || resolved.OAuth2.ClientID != "legacy" {
		t.Fatalf("legacy resolution: %+v err=%v", resolved, err)
	}
}

func TestNoAuthenticationRejectsCredentialReference(t *testing.T) {
	contract := Contract{
		Version: ContractVersion, DefaultSchemeKey: "none",
		Schemes: []Scheme{{Key: "none", Type: SchemeNone, DisplayName: "None"}},
	}
	driver, _ := json.Marshal(DriverConfig{Authentication: &contract})
	if _, err := Resolve(driver, json.RawMessage(`{"schemeKey":"none","values":{}}`), SchemeNone, true); !errors.Is(err, ErrInvalidConnection) {
		t.Fatalf("expected NONE scheme to reject a credential reference, got %v", err)
	}
}

func TestNestedTokenResponsePaths(t *testing.T) {
	payload := map[string]any{"data": map[string]any{"token": "abc", "ttl": "120"}}
	if StringAt(payload, "data.token") != "abc" || Int64At(payload, "data.ttl") != 120 {
		t.Fatalf("unexpected path result")
	}
}

func TestTokenURLTemplateEscapesPathAndQueryValuesInTheirOwnContext(t *testing.T) {
	resolved, err := resolveTemplate(
		"https://auth.example/{{tenant}}/token?audience={{audience}}",
		map[string]string{"tenant": "tenant one", "audience": "orders & admin=true"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "https://auth.example/tenant%20one/token?audience=orders+%26+admin%3Dtrue" {
		t.Fatalf("unexpected context-aware URL escaping: %s", resolved)
	}
}
