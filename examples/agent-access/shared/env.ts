/**
 * Environment loading for examples. Secrets have no hardcoded defaults.
 */

export interface ExampleEnv {
  aapBaseUrl: string;
  workspaceId: string;
  agentId: string;
  clientId: string;
  clientSecret: string;
  scope: string;
  bffHost: string;
  bffPort: number;
  mintHost: string;
  mintPort: number;
  subjectTokenIssuer: string;
  subjectTokenAudience: string;
}

export function loadExampleEnv(source: NodeJS.ProcessEnv = process.env): ExampleEnv {
  return {
    aapBaseUrl: required(source, "AAP_BASE_URL"),
    workspaceId: required(source, "AAP_WORKSPACE_ID"),
    agentId: required(source, "AAP_AGENT_ID"),
    clientId: required(source, "AAP_CLIENT_ID"),
    clientSecret: required(source, "AAP_CLIENT_SECRET"),
    scope:
      source.AAP_SCOPE?.trim() ||
      "agent:read conversation:create conversation:read run:create run:read run:cancel event:read interaction:decide",
    bffHost: source.BFF_LISTEN_HOST?.trim() || "127.0.0.1",
    bffPort: parsePort(source.BFF_LISTEN_PORT, 8787),
    mintHost: source.MINT_LISTEN_HOST?.trim() || "127.0.0.1",
    mintPort: parsePort(source.MINT_LISTEN_PORT, 8788),
    subjectTokenIssuer: source.SUBJECT_TOKEN_ISSUER?.trim() || "https://business.example.com/",
    subjectTokenAudience:
      source.SUBJECT_TOKEN_AUDIENCE?.trim() || "actweave-agent-access-subject",
  };
}

/** Partial env for unit tests that inject their own secrets. */
export function testEnv(overrides: Partial<ExampleEnv> & Pick<ExampleEnv, "aapBaseUrl" | "clientId" | "clientSecret">): ExampleEnv {
  return {
    workspaceId: "ws-test",
    agentId: "ag-test",
    scope: "run:read event:read",
    bffHost: "127.0.0.1",
    bffPort: 0,
    mintHost: "127.0.0.1",
    mintPort: 0,
    subjectTokenIssuer: "https://business.example.com/",
    subjectTokenAudience: "actweave-agent-access-subject",
    ...overrides,
  };
}

function required(source: NodeJS.ProcessEnv, key: string): string {
  const value = source[key]?.trim() ?? "";
  if (!value) {
    throw new Error(`Missing required environment variable ${key} (see .env.example)`);
  }
  return value;
}

function parsePort(raw: string | undefined, fallback: number): number {
  if (!raw?.trim()) {
    return fallback;
  }
  const n = Number.parseInt(raw, 10);
  if (!Number.isInteger(n) || n < 0 || n > 65535) {
    throw new Error(`Invalid port: ${raw}`);
  }
  return n;
}
