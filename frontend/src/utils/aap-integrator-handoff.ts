import type { AgentAccessClient, AgentAccessCredential, AgentAccessGrant } from "../stores/agentAccess";

export const AAP_INTEGRATOR_HANDOFF_SPEC = "actweave.aap-integrator-handoff.v1" as const;
export const AAP_PROTOCOL_BASE_PATH = "/api/agent-access/v1";

export interface AAPIntegratorHandoffGrant {
  agentId: string;
  agentName: string;
  scopes: string[];
  status: AgentAccessGrant["status"];
  expiresAt?: string;
}

export interface AAPIntegratorHandoff {
  spec: typeof AAP_INTEGRATOR_HANDOFF_SPEC;
  generatedAt: string;
  aap: {
    baseUrl: string;
    tokenUrl: string;
    jwksUrl: string;
  };
  workspaceId: string;
  client: {
    name: string;
    clientId: string;
    status: AgentAccessClient["status"];
    authMethod: AgentAccessClient["authMethod"];
    tokenTtlSeconds: number;
    allowedCorsOrigins: string[];
    jwksUri?: string;
    trustedSubjectIssuer?: string;
    trustedSubjectJwksUri?: string;
  };
  selectedGrant: AAPIntegratorHandoffGrant | null;
  grants: AAPIntegratorHandoffGrant[];
  credentials: Array<{
    type: AgentAccessCredential["type"];
    publicHint: string;
    status: "ACTIVE" | "REVOKED";
    expiresAt?: string;
  }>;
  secrets: {
    clientSecretIncluded: false;
    note: string;
  };
  howTo: {
    runtimePath: string;
    topology: string;
    mintToken: string;
  };
}

export interface BuildAAPIntegratorHandoffInput {
  client: AgentAccessClient;
  grants: AgentAccessGrant[];
  credentials: AgentAccessCredential[];
  workspaceId: string;
  aapBaseUrl: string;
  selectedGrantId?: string;
  agentName: (agentId: string) => string;
  now?: Date;
}

export function defaultAAPBaseURL(origin: string): string {
  const base = origin.trim().replace(/\/+$/, "");
  if (!base) return AAP_PROTOCOL_BASE_PATH;
  return `${base}${AAP_PROTOCOL_BASE_PATH}`;
}

export function normalizeAAPBaseURL(raw: string): string {
  return raw.trim().replace(/\/+$/, "");
}

export function handoffFilename(clientId: string, format: "env" | "json"): string {
  const slug =
    clientId
      .trim()
      .replace(/[^A-Za-z0-9._-]+/g, "-")
      .replace(/^-+|-+$/g, "") || "client";
  return `actweave-aap-${slug}.${format}`;
}

export function buildAAPIntegratorHandoff(input: BuildAAPIntegratorHandoffInput): AAPIntegratorHandoff {
  const baseUrl = normalizeAAPBaseURL(input.aapBaseUrl) || AAP_PROTOCOL_BASE_PATH;
  const grants = input.grants.map((grant) => toHandoffGrant(grant, input.agentName));
  const selected = pickSelectedGrant(input.grants, input.selectedGrantId);
  return {
    spec: AAP_INTEGRATOR_HANDOFF_SPEC,
    generatedAt: (input.now ?? new Date()).toISOString(),
    aap: {
      baseUrl,
      tokenUrl: `${baseUrl}/oauth/token`,
      jwksUrl: `${baseUrl}/.well-known/jwks.json`,
    },
    workspaceId: input.workspaceId,
    client: {
      name: input.client.name,
      clientId: input.client.clientId,
      status: input.client.status,
      authMethod: input.client.authMethod,
      tokenTtlSeconds: input.client.tokenTtlSeconds,
      allowedCorsOrigins: [...input.client.allowedCorsOrigins],
      jwksUri: input.client.jwksUri || undefined,
      trustedSubjectIssuer: input.client.trustedSubjectIssuer || undefined,
      trustedSubjectJwksUri: input.client.trustedSubjectJwksUri || undefined,
    },
    selectedGrant: selected ? toHandoffGrant(selected, input.agentName) : null,
    grants,
    credentials: input.credentials.map((credential) => ({
      type: credential.type,
      publicHint: credential.publicHint,
      status: credential.revokedAt ? "REVOKED" : "ACTIVE",
      expiresAt: credential.expiresAt,
    })),
    secrets: {
      clientSecretIncluded: false,
      note: "Client Secret is shown once at creation or rotation and is never re-exported.",
    },
    howTo: {
      runtimePath: `${AAP_PROTOCOL_BASE_PATH} — Console /api/v1 session JWTs are rejected on AAP routes`,
      topology: "Keep Client Secret on a BFF; browsers must not persist secrets or access tokens",
      mintToken:
        input.client.authMethod === "private_key_jwt"
          ? "POST tokenUrl with private_key_jwt client_assertion, agent_id, and a scope subset of the Grant"
          : "POST tokenUrl with HTTP Basic client_id:client_secret, grant_type=client_credentials, agent_id, and a scope subset of the Grant",
    },
  };
}

export function renderAAPIntegratorHandoffJSON(packet: AAPIntegratorHandoff): string {
  return `${JSON.stringify(packet, null, 2)}\n`;
}

export function renderAAPIntegratorHandoffEnv(packet: AAPIntegratorHandoff): string {
  const grant = packet.selectedGrant;
  const cors =
    packet.client.allowedCorsOrigins.length > 0
      ? packet.client.allowedCorsOrigins.join(", ")
      : "server/BFF only (no browser-direct CORS)";
  const lines = [
    "# ActWeave AAP integrator handoff",
    `# spec: ${packet.spec}`,
    "# Do not use Console /api/v1 or a user session JWT on these endpoints.",
    "# Keep Client Secret on your BFF. Browsers must not store secrets or access tokens.",
    "# Client Secret is shown once at creation or rotation and is never included here.",
    "",
    `AAP_BASE_URL=${envValue(packet.aap.baseUrl)}`,
    `AAP_CLIENT_ID=${envValue(packet.client.clientId)}`,
  ];
  if (packet.client.authMethod === "private_key_jwt") {
    lines.push(
      "# Auth method: private_key_jwt. AAP_CLIENT_SECRET is unused; sign client_assertion with your registered key.",
    );
  } else {
    lines.push("# Paste the one-time Client Secret from creation or rotation. Do not commit it.");
  }
  lines.push("AAP_CLIENT_SECRET=", `AAP_WORKSPACE_ID=${envValue(packet.workspaceId)}`);
  if (grant) {
    lines.push(`AAP_AGENT_ID=${envValue(grant.agentId)}`);
  } else {
    lines.push(
      "# No Agent grant selected. Create a Grant in Console → Agent Access before minting a token.",
      "AAP_AGENT_ID=",
    );
  }
  lines.push(
    `AAP_SCOPES=${envValue(grant ? grant.scopes.join(" ") : "")}`,
    "",
    `# Token URL: ${packet.aap.tokenUrl}`,
    `# JWKS URL: ${packet.aap.jwksUrl}`,
    `# Auth method: ${packet.client.authMethod}`,
    `# Token TTL seconds: ${packet.client.tokenTtlSeconds}`,
    `# CORS: ${cors}`,
  );
  if (packet.client.trustedSubjectIssuer) {
    lines.push(`# Trusted subject issuer: ${packet.client.trustedSubjectIssuer}`);
  }
  if (grant) {
    lines.push(`# Agent: ${grant.agentName}`);
  }
  if (packet.client.authMethod === "client_secret_basic") {
    lines.push(
      "",
      "# Token request example:",
      '# curl -sS -X POST "$AAP_BASE_URL/oauth/token" \\',
      '#   -u "$AAP_CLIENT_ID:$AAP_CLIENT_SECRET" \\',
      '#   -H "Content-Type: application/x-www-form-urlencoded" \\',
      '#   -d "grant_type=client_credentials&agent_id=$AAP_AGENT_ID&scope=$AAP_SCOPES"',
    );
  }
  return `${lines.join("\n")}\n`;
}

function pickSelectedGrant(grants: AgentAccessGrant[], selectedGrantId?: string): AgentAccessGrant | undefined {
  const wanted = selectedGrantId?.trim();
  if (wanted) {
    const match = grants.find((grant) => grant.id === wanted);
    if (match) return match;
  }
  return grants.find((grant) => grant.status === "ACTIVE") ?? grants[0];
}

function toHandoffGrant(grant: AgentAccessGrant, agentName: (agentId: string) => string): AAPIntegratorHandoffGrant {
  return {
    agentId: grant.agentId,
    agentName: agentName(grant.agentId),
    scopes: [...grant.scopes],
    status: grant.status,
    expiresAt: grant.expiresAt,
  };
}

function envValue(value: string): string {
  return value.replace(/[\r\n]+/g, "");
}
