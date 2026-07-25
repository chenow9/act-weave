package agentaccess

type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusDisabled Status = "DISABLED"
)

func KnownStatuses() []Status {
	return []Status{StatusActive, StatusDisabled}
}

func ParseStatus(value string) (Status, bool) {
	status := Status(value)
	for _, known := range KnownStatuses() {
		if status == known {
			return status, true
		}
	}
	return "", false
}

type ClientAuthMethod string

const (
	ClientAuthMethodSecretBasic ClientAuthMethod = "client_secret_basic"
	ClientAuthMethodPrivateKey  ClientAuthMethod = "private_key_jwt"
)

func KnownClientAuthMethods() []ClientAuthMethod {
	return []ClientAuthMethod{
		ClientAuthMethodSecretBasic,
		ClientAuthMethodPrivateKey,
	}
}

func ParseClientAuthMethod(value string) (ClientAuthMethod, bool) {
	method := ClientAuthMethod(value)
	for _, known := range KnownClientAuthMethods() {
		if method == known {
			return method, true
		}
	}
	return "", false
}

type CredentialType string

const (
	CredentialTypeClientSecret    CredentialType = "client_secret"
	CredentialTypeJWK             CredentialType = "jwk"
	CredentialTypeMTLSCertificate CredentialType = "mtls_certificate"
)

func KnownCredentialTypes() []CredentialType {
	return []CredentialType{
		CredentialTypeClientSecret,
		CredentialTypeJWK,
		CredentialTypeMTLSCertificate,
	}
}

func ParseCredentialType(value string) (CredentialType, bool) {
	credentialType := CredentialType(value)
	for _, known := range KnownCredentialTypes() {
		if credentialType == known {
			return credentialType, true
		}
	}
	return "", false
}

type GrantStatus string

const (
	GrantStatusActive  GrantStatus = "ACTIVE"
	GrantStatusRevoked GrantStatus = "REVOKED"
)

func KnownGrantStatuses() []GrantStatus {
	return []GrantStatus{GrantStatusActive, GrantStatusRevoked}
}
