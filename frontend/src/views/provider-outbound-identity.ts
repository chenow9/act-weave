export interface OutboundIdentityDraftInput {
  supportBrokerObo: boolean;
  supportRequestPassthrough: boolean;
  brokerTokenEndpoint: string;
  brokerAudience: string;
  brokerAllowedScopes: string;
  businessInjectionHeader: string;
  businessInjectionPrefix: string;
}

/**
 * Build outbound-identity.v1 payload from editor draft.
 * T3=A: zero modes throw — never silent REQUEST_PASSTHROUGH fallback.
 */
export function buildOutboundIdentityContract(draft: OutboundIdentityDraftInput): Record<string, unknown> {
  const supportedModes: string[] = [];
  if (draft.supportBrokerObo) supportedModes.push("BROKER_OBO");
  if (draft.supportRequestPassthrough) supportedModes.push("REQUEST_PASSTHROUGH");
  if (!supportedModes.length) {
    throw new Error(
      "outbound-identity.v1 requires at least one supportedMode (BROKER_OBO and/or REQUEST_PASSTHROUGH).",
    );
  }
  const injection = {
    headerName: (draft.businessInjectionHeader || "Authorization").trim() || "Authorization",
    prefix: (draft.businessInjectionPrefix || "Bearer").trim() || "Bearer",
  };
  const identity: Record<string, unknown> = {
    schemaVersion: "outbound-identity.v1",
    supportedModes,
    supportedSubjectTypes: ["USER"],
  };
  if (supportedModes.includes("BROKER_OBO")) {
    identity.brokerObo = {
      tokenEndpoint: draft.brokerTokenEndpoint.trim(),
      audience: draft.brokerAudience.trim(),
      grantType: "urn:ietf:params:oauth:grant-type:token-exchange",
      subjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
      requestedTokenType: "urn:ietf:params:oauth:token-type:access_token",
      machineAuthMethod: "private_key_jwt",
      allowedScopes: draft.brokerAllowedScopes
        .split(/[\s,]+/)
        .map((s) => s.trim())
        .filter(Boolean),
      response: {
        accessTokenPath: "access_token",
        tokenTypePath: "token_type",
        expiresInPath: "expires_in",
        expectedTokenType: "Bearer",
      },
      businessInjection: injection,
    };
  }
  if (supportedModes.includes("REQUEST_PASSTHROUGH")) {
    identity.requestPassthrough = {
      credentialTypes: ["ACCESS_TOKEN"],
      businessInjection: injection,
    };
  }
  return identity;
}
