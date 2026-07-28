/** Provider + Connection pure mappers (ZKL-64 item 10). */
import type {
  CapabilityProvider,
  ProviderAsset,
  ServiceConnection,
  ServiceConnectionVerification,
} from "../../../types/domain";

export interface ConnectionDTO {
  id: string;
  providerId: string;
  name: string;
  alias: string;
  environment: string;
  externalAccountRef?: string;
  /** Dual-mode fields from hard-cutover DTO (legacy authMode not returned). */
  outboundIdentity?: Record<string, unknown> | null;
  outboundIdentityPolicyVersion?: number;
  migrationState?: string;
  machineCredentialConfigured?: boolean;
  /** Legacy fields may be absent after hard cutover. */
  authMode?: string;
  authConfig?: Record<string, unknown>;
  credentialConfigured?: boolean;
  credentialFingerprint?: string;
  grantedScopes: unknown[];
  policy: Record<string, unknown>;
  status: ServiceConnection["status"];
  lastVerifiedAt?: string;
  lastErrorCode?: string;
  createdBy: string;
  updatedBy: string;
  lockVersion: number;
}

export interface ConnectionVerificationDTO {
  ID: string;
  WorkspaceID: string;
  ConnectionID: string;
  Status: string;
  Diagnostics: Record<string, string>;
  LatencyMS?: number;
  TestedBy: string;
  TestedAt: string;
  RawObjectID?: string;
}

export interface ProviderSyncResult {
  id: string;
  status: string;
  discoveredCount: number;
  changedCount: number;
  errorSummary: Record<string, unknown>;
}

export interface ProviderMaterializationResult {
  asset: ProviderAsset;
  capabilityId: string;
  draftVersionId: string;
  lifecycleStatus: string;
}

export interface SecretReadDTO {
  id: string;
  workspaceId: string;
  name: string;
  kind: string;
  configured: boolean;
  fingerprint?: string;
  activeVersionNo?: number;
  createdBy: string;
  updatedBy: string;
  createdAt: string;
  updatedAt: string;
  lockVersion: number;
}

export function normalizeProvider(provider: CapabilityProvider): CapabilityProvider {
  return {
    ...provider,
    kind: "HTTP_OPENAPI",
    transport: "HTTP",
    endpointConfig: asRecord(provider.endpointConfig),
    driverConfig: asRecord(provider.driverConfig),
  };
}

export function providerWritePayload(provider: CapabilityProvider) {
  return {
    name: provider.name,
    kind: "HTTP_OPENAPI",
    driverKey: provider.driverKey,
    transport: "HTTP",
    endpointConfig: provider.endpointConfig || {},
    driverConfig: provider.driverConfig || {},
    discoveryMode: provider.discoveryMode,
  };
}

export function emptyAuthConfig(): ServiceConnection["authConfig"] {
  return {
    mode: "",
    label: "",
    schemeKey: "",
    values: {},
    tokenUrl: "",
    clientId: "",
    clientAuth: "",
    scope: "",
    refreshUrl: "",
    refreshMode: "",
    accessTokenPath: "",
    refreshTokenPath: "",
    expiresPath: "",
    injectionTemplate: "",
    retryOn401Policy: "",
    refreshFailurePolicy: "",
    credentialPlacement: "",
    apiKeyName: "",
    apiSecretName: "",
    tokenHeaderName: "",
    tokenPrefix: "",
  };
}

export function parseOutboundMode(
  identity: Record<string, unknown> | null | undefined,
): ServiceConnection["outboundMode"] {
  const mode = firstString(identity?.mode).toUpperCase();
  if (mode === "BROKER_OBO" || mode === "REQUEST_PASSTHROUGH") return mode;
  return undefined;
}

export function connectionFromDTO(
  value: ConnectionDTO,
  provider: CapabilityProvider,
  workspaceId?: string,
): ServiceConnection {
  const auth = asRecord(value.authConfig);
  const contractValues = stringRecord(auth.values);
  const endpoint = asRecord(provider.endpointConfig);
  const sourceAddress = firstString(endpoint.serviceBaseUrl, endpoint.baseUrl, endpoint.url, endpoint.endpoint);
  const parts = splitEndpoint(sourceAddress);
  const outboundIdentity =
    value.outboundIdentity && typeof value.outboundIdentity === "object"
      ? (value.outboundIdentity as Record<string, unknown>)
      : undefined;
  const outboundMode = parseOutboundMode(outboundIdentity);
  const uiMode = uiAuthMode(value.authMode || "");
  return {
    id: value.id,
    workspaceId,
    providerId: value.providerId,
    name: value.name,
    alias: value.alias,
    environment: value.environment,
    externalAccountRef: value.externalAccountRef,
    protocol: provider.transport,
    protocolConfig: {
      domain: sourceAddress,
      host: parts.host,
      port: parts.port,
      basePath: parts.path,
      verificationMethod: firstString(auth.verificationMethod) || "GET",
      verificationPath: firstString(auth.verificationPath),
      expectedStatus: firstString(auth.expectedStatus) || "200-299",
      expectedResponseContains: firstString(auth.expectedResponseContains),
      commonHeaders: stringRecord(auth.commonHeaders),
    },
    protocolSchema: "provider.http-openapi.v1",
    authMode: value.authMode || "",
    authConfig: {
      ...emptyAuthConfig(),
      mode: uiMode,
      label: firstString(auth.label),
      schemeKey: firstString(auth.schemeKey),
      values: contractValues,
      tokenUrl: firstString(auth.tokenUrl),
      clientId: firstString(contractValues.clientId, auth.clientId),
      clientAuth: firstString(auth.clientAuth) || "client_secret_basic",
      scope: firstString(contractValues.scope, auth.scope),
      refreshUrl: firstString(auth.refreshUrl),
      refreshMode: firstString(auth.refreshMode) || "none",
      accessTokenPath: firstString(auth.accessTokenPath),
      refreshTokenPath: firstString(auth.refreshTokenPath),
      expiresPath: firstString(auth.expiresPath),
      injectionTemplate: firstString(auth.injectionTemplate),
      retryOn401Policy: firstString(auth.retryOn401Policy),
      refreshFailurePolicy: firstString(auth.refreshFailurePolicy),
      credentialPlacement: firstString(auth.credentialPlacement, auth.placement) || "header",
      apiKeyName: firstString(auth.apiKeyName, auth.headerName),
      apiSecretName: firstString(auth.apiSecretName),
      tokenHeaderName: firstString(auth.tokenHeaderName, auth.headerName),
      tokenPrefix: firstString(auth.tokenPrefix),
    },
    outboundMode,
    outboundIdentity,
    outboundIdentityPolicyVersion: value.outboundIdentityPolicyVersion,
    migrationState:
      value.migrationState === "MIGRATION_REQUIRED"
        ? "MIGRATION_REQUIRED"
        : value.migrationState === "NONE"
          ? "NONE"
          : undefined,
    machineCredentialConfigured: Boolean(value.machineCredentialConfigured),
    credentialSecretId: undefined,
    credentialConfigured: Boolean(value.credentialConfigured || value.machineCredentialConfigured),
    credentialFingerprint: value.credentialFingerprint,
    grantedScopes: Array.isArray(value.grantedScopes) ? value.grantedScopes : [],
    policy: asRecord(value.policy),
    status: value.status,
    lastVerifiedAt: value.lastVerifiedAt,
    lastErrorCode: value.lastErrorCode,
    createdBy: value.createdBy,
    updatedBy: value.updatedBy,
    lockVersion: value.lockVersion,
  };
}

export function buildOutboundIdentityPayload(connection: ServiceConnection): Record<string, unknown> | null {
  const mode = connection.outboundMode;
  if (mode !== "BROKER_OBO" && mode !== "REQUEST_PASSTHROUGH") return null;
  if (mode === "BROKER_OBO") {
    const existing = (connection.outboundIdentity?.brokerObo || {}) as Record<string, unknown>;
    const clientId = firstString(existing.clientId, connection.authConfig.clientId).trim();
    const scopesRaw = existing.scopes;
    const scopes = Array.isArray(scopesRaw)
      ? scopesRaw.map(String)
      : String(connection.authConfig.scope || "")
          .split(/[\s,]+/)
          .map((s) => s.trim())
          .filter(Boolean);
    const maxTokenTtlSeconds = Number(existing.maxTokenTtlSeconds) || 300;
    return {
      schemaVersion: "outbound-connection.v1",
      mode: "BROKER_OBO",
      brokerObo: { clientId, scopes, maxTokenTtlSeconds },
    };
  }
  const existing = (connection.outboundIdentity?.requestPassthrough || {}) as Record<string, unknown>;
  const maxResidenceSeconds = Number(existing.maxResidenceSeconds) || 600;
  return {
    schemaVersion: "outbound-connection.v1",
    mode: "REQUEST_PASSTHROUGH",
    requestPassthrough: { maxResidenceSeconds },
  };
}

export function connectionWritePayload(
  connection: ServiceConnection,
  includeLock: boolean,
  credentialPlaintext = "",
  options: { impactConfirmationProof?: string; metadataOnly?: boolean; machineCredentialPlaintext?: string } = {},
) {
  const dual = buildOutboundIdentityPayload(connection);
  const payload: Record<string, unknown> = {
    name: connection.name,
    alias:
      connection.alias.trim() ||
      connection.name
        .trim()
        .toLocaleLowerCase()
        .replace(/[^a-z0-9]+/g, "-")
        .replace(/^-|-$/g, ""),
    environment: apiEnvironment(connection.environment),
    ...(connection.externalAccountRef?.trim() ? { externalAccountRef: connection.externalAccountRef.trim() } : {}),
    grantedScopes: connection.grantedScopes || [],
    policy: connection.policy || {},
  };
  if (dual) {
    payload.outboundIdentity = dual;
    if (options.machineCredentialPlaintext) {
      payload.machineCredential = { kind: "PRIVATE_KEY_PEM", plaintext: options.machineCredentialPlaintext };
    }
    if (options.impactConfirmationProof) {
      payload.impactConfirmationProof = options.impactConfirmationProof;
    }
    if (options.metadataOnly) payload.metadataOnly = true;
  } else {
    // Legacy path (non dual-mode) — still rejected by backend for HTTP targets after hard cut.
    const authConfig = sanitizeAuthConfig(connection.authConfig);
    payload.authMode = apiAuthMode(connection.authConfig.mode || connection.authMode);
    payload.authConfig = authConfig;
    if (connection.credentialSecretId?.trim()) payload.credentialSecretId = connection.credentialSecretId.trim();
    if (credentialPlaintext) payload.credential = { kind: "OAUTH2_CLIENT_SECRET", plaintext: credentialPlaintext };
  }
  if (includeLock) payload.lockVersion = connection.lockVersion;
  return payload;
}

export function sanitizeAuthConfig(value: ServiceConnection["authConfig"]) {
  if (value.schemeKey && value.values) {
    return {
      schemeKey: value.schemeKey,
      values: Object.fromEntries(
        Object.entries(value.values)
          .map(([key, item]) => [key, item.trim()])
          .filter(([, item]) => item !== ""),
      ),
    };
  }
  const blocked = new Set(["apiKeyValue", "apiSecretValue", "fixedToken", "password", "tokenValue", "secretValue"]);
  return Object.fromEntries(
    Object.entries(value).filter(([key, item]) => !blocked.has(key) && item !== "" && item !== undefined),
  );
}

export function verificationFromDTO(value: ConnectionVerificationDTO): ServiceConnectionVerification {
  return {
    id: value.ID,
    workspaceId: value.WorkspaceID,
    connectionId: value.ConnectionID,
    status: value.Status,
    diagnostics: asRecord(value.Diagnostics) as Record<string, string>,
    latencyMs: value.LatencyMS,
    testedBy: value.TestedBy,
    testedAt: value.TestedAt,
    rawObjectId: value.RawObjectID,
  };
}

export function filterConnections(items: ServiceConnection[], query: string, status?: ServiceConnection["status"]) {
  const needle = query.trim().toLocaleLowerCase();
  return items.filter((connection) => {
    if (status && connection.status !== status) return false;
    if (!needle) return true;
    return [
      connection.name,
      connection.alias,
      connection.environment,
      connection.authMode,
      connection.outboundMode || "",
      connection.migrationState || "",
      connection.protocolConfig.domain,
    ].some((value) => value.toLocaleLowerCase().includes(needle));
  });
}

export function sortConnections(items: ServiceConnection[], sortBy?: string, order?: "asc" | "desc") {
  if (!sortBy || !order) return items;
  const allowed = new Set(["name", "environment", "authMode", "status", "createdBy", "updatedBy"]);
  if (!allowed.has(sortBy)) return items;
  return [...items].sort((left, right) => {
    const comparison = String(left[sortBy as keyof ServiceConnection] || "").localeCompare(
      String(right[sortBy as keyof ServiceConnection] || ""),
      "zh-Hans",
    );
    return order === "asc" ? comparison : -comparison;
  });
}

export function apiEnvironment(value: string) {
  if (value === "测试" || value === "TEST") return "TEST";
  if (value === "STAGING" || value === "Staging") return "STAGING";
  if (value === "DEVELOPMENT" || value === "Development") return "DEVELOPMENT";
  return "PRODUCTION";
}

export function apiAuthMode(value: string) {
  const modes: Record<string, string> = {
    none: "NONE",
    "api-key-secret": "API_KEY",
    "fixed-token": "BEARER",
    "oauth2-client": "OAUTH2_CLIENT",
    "oauth2-mtls": "OAUTH2_MTLS",
    "custom-token-api": "CUSTOM_TOKEN",
  };
  return modes[value] || value || "NONE";
}

export function uiAuthMode(value: string) {
  const modes: Record<string, string> = {
    API_KEY: "api-key-secret",
    BEARER: "fixed-token",
    OAUTH2_CLIENT: "oauth2-client",
    OAUTH2_MTLS: "oauth2-mtls",
    CUSTOM_TOKEN: "custom-token-api",
    NONE: "",
  };
  return modes[value] ?? value;
}

export function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
}

export function stringRecord(value: unknown): Record<string, string> {
  return Object.fromEntries(
    Object.entries(asRecord(value)).filter((entry): entry is [string, string] => typeof entry[1] === "string"),
  );
}

export function firstString(...values: unknown[]) {
  return values.find((value): value is string => typeof value === "string") || "";
}

export function splitEndpoint(value: string) {
  try {
    const parsed = new URL(value);
    return { host: parsed.hostname, port: parsed.port, path: parsed.pathname === "/" ? "" : parsed.pathname };
  } catch {
    return { host: "", port: "", path: "" };
  }
}
