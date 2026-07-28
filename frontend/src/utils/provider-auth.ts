import type {
  CapabilityProvider,
  ProviderAuthContract,
  ProviderAuthField,
  ProviderAuthScheme,
  ServiceConnection,
} from "../types/domain";

export const PROVIDER_AUTH_VERSION = "service-auth.v1" as const;

export function defaultOAuthContract(tokenUrlTemplate = ""): ProviderAuthContract {
  return {
    version: PROVIDER_AUTH_VERSION,
    defaultSchemeKey: "oauth2-client",
    schemes: [
      {
        key: "oauth2-client",
        type: "OAUTH2_CLIENT",
        displayName: "OAuth2 Client Credentials",
        description: "Provider 负责 Token 协议，Connection 只填写当前环境的账号和 Secret。",
        fields: [
          { key: "clientId", label: "Client ID", kind: "TEXT", required: true, placeholder: "客户端标识" },
          {
            key: "clientSecret",
            label: "Client Secret",
            kind: "SECRET",
            required: true,
            help: "明文只用于创建或替换 Secret。",
          },
          { key: "scope", label: "Scope", kind: "TEXT", placeholder: "例如：read write" },
        ],
        oauth2: {
          tokenUrlTemplate,
          clientIdField: "clientId",
          credentialField: "clientSecret",
          clientAuthMethod: "client_secret_basic",
          scopeField: "scope",
          response: {
            accessTokenPath: "access_token",
            tokenTypePath: "token_type",
            expiresInPath: "expires_in",
          },
          injection: { headerName: "Authorization", prefix: "Bearer" },
          refreshStrategy: "CLIENT_CREDENTIALS",
        },
      },
    ],
  };
}

export function noAuthenticationContract(): ProviderAuthContract {
  return {
    version: PROVIDER_AUTH_VERSION,
    defaultSchemeKey: "none",
    schemes: [{ key: "none", type: "NONE", displayName: "无需认证", fields: [] }],
  };
}

export function providerAuthContract(provider?: CapabilityProvider): ProviderAuthContract | null {
  const candidate = provider?.driverConfig?.authentication;
  if (
    !candidate ||
    candidate.version !== PROVIDER_AUTH_VERSION ||
    !Array.isArray(candidate.schemes) ||
    !candidate.schemes.length
  )
    return null;
  if (!candidate.schemes.some((scheme) => scheme.key === candidate.defaultSchemeKey)) return null;
  return candidate;
}

export function providerAuthSchemes(provider?: CapabilityProvider): ProviderAuthScheme[] {
  return providerAuthContract(provider)?.schemes || [];
}

export function providerAuthScheme(
  provider: CapabilityProvider | undefined,
  schemeKey?: string,
): ProviderAuthScheme | null {
  const contract = providerAuthContract(provider);
  if (!contract) return null;
  return (
    contract.schemes.find((scheme) => scheme.key === schemeKey) ||
    contract.schemes.find((scheme) => scheme.key === contract.defaultSchemeKey) ||
    null
  );
}

export function connectionProviderAuthScheme(
  provider: CapabilityProvider | undefined,
  connection: ServiceConnection,
): ProviderAuthScheme | null {
  const schemeKey = connection.authConfig.schemeKey?.trim();
  if (!schemeKey) return null;
  return providerAuthSchemes(provider).find((scheme) => scheme.key === schemeKey) || null;
}

export function authSchemeSummary(provider: CapabilityProvider) {
  const identity = (provider.driverConfig as Record<string, unknown> | undefined)?.outboundIdentity;
  if (identity && typeof identity === "object") {
    const modes = Array.isArray((identity as { supportedModes?: unknown }).supportedModes)
      ? (identity as { supportedModes: unknown[] }).supportedModes.map((mode) => String(mode).toUpperCase())
      : [];
    const labels = modes
      .map((mode) => {
        if (mode === "BROKER_OBO") return "Broker / OBO";
        if (mode === "REQUEST_PASSTHROUGH") return "请求透传";
        return "";
      })
      .filter(Boolean);
    if (labels.length) return labels.join("、");
  }
  const contract = providerAuthContract(provider);
  if (!contract) return "未配置认证契约";
  return contract.schemes.map((scheme) => scheme.displayName).join("、");
}

export function connectionAuthValues(authConfig: ServiceConnection["authConfig"]): Record<string, string> {
  if (authConfig.values && typeof authConfig.values === "object") return { ...authConfig.values };
  return {};
}

export function authModeForScheme(scheme: ProviderAuthScheme) {
  return scheme.type;
}

export function uiModeForScheme(scheme: ProviderAuthScheme) {
  if (scheme.type === "OAUTH2_CLIENT") return "oauth2-client";
  return "none";
}

export function publicAuthFields(scheme: ProviderAuthScheme | null): ProviderAuthField[] {
  return scheme?.fields.filter((field) => field.kind !== "SECRET") || [];
}

export function credentialField(scheme: ProviderAuthScheme | null): ProviderAuthField | null {
  return scheme?.fields.find((field) => field.kind === "SECRET") || null;
}

export function providerHasOutboundIdentity(provider?: CapabilityProvider): boolean {
  if (!provider) return false;
  const identity = (provider.driverConfig as Record<string, unknown> | undefined)?.outboundIdentity;
  if (!identity || typeof identity !== "object") return false;
  const modes = (identity as { supportedModes?: unknown }).supportedModes;
  return (
    Array.isArray(modes) &&
    modes.some((m) => {
      const mode = String(m).toUpperCase();
      return mode === "BROKER_OBO" || mode === "REQUEST_PASSTHROUGH";
    })
  );
}

export function isProviderReadyForConnections(provider: CapabilityProvider) {
  const endpoint = provider.endpointConfig || {};
  const hasEndpoint =
    provider.status === "ACTIVE" &&
    Number(endpoint.schemaVersion) === 2 &&
    typeof endpoint.serviceBaseUrl === "string" &&
    Boolean(endpoint.serviceBaseUrl.trim());
  // Dual-mode hard-cutover: outbound-identity.v1 is preferred; legacy auth contract still counts for migration UIs.
  return hasEndpoint && (providerHasOutboundIdentity(provider) || Boolean(providerAuthContract(provider)));
}

export function legacySchemeForConnection(connection: ServiceConnection): ProviderAuthScheme {
  const label = connection.authConfig.label || connection.authMode || "Legacy authentication";
  return {
    key: "legacy-readonly",
    type: connection.authMode === "NONE" ? "NONE" : "OAUTH2_CLIENT",
    displayName: label,
    fields: [],
  };
}
