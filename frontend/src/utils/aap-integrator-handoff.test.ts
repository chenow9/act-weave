import { describe, expect, it } from "vitest";

import type { AgentAccessClient, AgentAccessCredential, AgentAccessGrant } from "../stores/agentAccess";
import {
  AAP_INTEGRATOR_HANDOFF_SPEC,
  buildAAPIntegratorHandoff,
  defaultAAPBaseURL,
  handoffFilename,
  renderAAPIntegratorHandoffEnv,
  renderAAPIntegratorHandoffJSON,
} from "./aap-integrator-handoff";

const client: AgentAccessClient = {
  id: "client-1",
  workspaceId: "workspace-1",
  servicePrincipalId: "principal-1",
  clientId: "awcl_public",
  name: "Business App",
  status: "ACTIVE",
  authMethod: "client_secret_basic",
  allowedCorsOrigins: ["https://app.example.com"],
  tokenTtlSeconds: 600,
  createdAt: "2026-07-20T00:00:00Z",
  updatedAt: "2026-07-20T00:00:00Z",
  lockVersion: 1,
};

const grant: AgentAccessGrant = {
  id: "grant-1",
  agentId: "agent-1",
  scopes: ["agent:read", "run:create", "run:read", "event:read"],
  policy: {},
  status: "ACTIVE",
  validFrom: "2026-07-20T00:00:00Z",
  createdAt: "2026-07-20T00:00:00Z",
  updatedAt: "2026-07-20T00:00:00Z",
  lockVersion: 1,
};

const credential: AgentAccessCredential = {
  id: "credential-1",
  type: "client_secret",
  publicHint: "…safe",
  validFrom: "2026-07-20T00:00:00Z",
  createdAt: "2026-07-20T00:00:00Z",
  lockVersion: 1,
};

describe("defaultAAPBaseURL", () => {
  it("appends the protocol path to the console origin", () => {
    expect(defaultAAPBaseURL("https://actweave.example.com")).toBe("https://actweave.example.com/api/agent-access/v1");
  });

  it("strips a trailing slash on the origin", () => {
    expect(defaultAAPBaseURL("https://actweave.example.com/")).toBe("https://actweave.example.com/api/agent-access/v1");
  });
});

describe("buildAAPIntegratorHandoff", () => {
  it("assembles public identifiers and never includes a client secret", () => {
    const packet = buildAAPIntegratorHandoff({
      client,
      grants: [grant],
      credentials: [credential],
      workspaceId: "workspace-1",
      aapBaseUrl: "https://actweave.example.com/api/agent-access/v1/",
      agentName: () => "Ops",
      now: new Date("2026-08-20T00:00:00.000Z"),
    });
    expect(packet.spec).toBe(AAP_INTEGRATOR_HANDOFF_SPEC);
    expect(packet.aap.baseUrl).toBe("https://actweave.example.com/api/agent-access/v1");
    expect(packet.aap.tokenUrl).toBe("https://actweave.example.com/api/agent-access/v1/oauth/token");
    expect(packet.client.clientId).toBe("awcl_public");
    expect(packet.selectedGrant).toEqual({
      agentId: "agent-1",
      agentName: "Ops",
      scopes: grant.scopes,
      status: "ACTIVE",
      expiresAt: undefined,
    });
    expect(packet.secrets.clientSecretIncluded).toBe(false);
    expect(JSON.stringify(packet)).not.toMatch(/awsk_|client_secret=/i);
  });

  it("prefers the requested grant when the client has several", () => {
    const second: AgentAccessGrant = {
      ...grant,
      id: "grant-2",
      agentId: "agent-2",
      scopes: ["agent:read"],
    };
    const packet = buildAAPIntegratorHandoff({
      client,
      grants: [grant, second],
      credentials: [],
      workspaceId: "workspace-1",
      aapBaseUrl: "https://host/api/agent-access/v1",
      selectedGrantId: "grant-2",
      agentName: (id) => (id === "agent-2" ? "Support" : "Ops"),
      now: new Date("2026-08-20T00:00:00.000Z"),
    });
    expect(packet.selectedGrant?.agentId).toBe("agent-2");
    expect(packet.grants).toHaveLength(2);
  });
});

describe("renderAAPIntegratorHandoffEnv", () => {
  it("emits demo-compatible env keys with an empty secret", () => {
    const packet = buildAAPIntegratorHandoff({
      client,
      grants: [grant],
      credentials: [credential],
      workspaceId: "workspace-1",
      aapBaseUrl: "https://actweave.example.com/api/agent-access/v1",
      agentName: () => "Ops",
      now: new Date("2026-08-20T00:00:00.000Z"),
    });
    const env = renderAAPIntegratorHandoffEnv(packet);
    expect(env).toContain("AAP_BASE_URL=https://actweave.example.com/api/agent-access/v1");
    expect(env).toContain("AAP_CLIENT_ID=awcl_public");
    expect(env).toContain("AAP_CLIENT_SECRET=");
    expect(env).toContain("AAP_WORKSPACE_ID=workspace-1");
    expect(env).toContain("AAP_AGENT_ID=agent-1");
    expect(env).toContain("AAP_SCOPES=agent:read run:create run:read event:read");
    expect(env).toContain("grant_type=client_credentials");
    expect(env).not.toMatch(/AAP_CLIENT_SECRET=.+/);
  });
});

describe("renderAAPIntegratorHandoffJSON", () => {
  it("round-trips the packet and omits secrets", () => {
    const packet = buildAAPIntegratorHandoff({
      client,
      grants: [grant],
      credentials: [credential],
      workspaceId: "workspace-1",
      aapBaseUrl: "https://actweave.example.com/api/agent-access/v1",
      agentName: () => "Ops",
      now: new Date("2026-08-20T00:00:00.000Z"),
    });
    const json = renderAAPIntegratorHandoffJSON(packet);
    expect(JSON.parse(json).secrets.clientSecretIncluded).toBe(false);
    expect(json).not.toMatch(/awsk_/);
  });
});

describe("handoffFilename", () => {
  it("uses the public client id", () => {
    expect(handoffFilename("awcl_public", "env")).toBe("actweave-aap-awcl_public.env");
    expect(handoffFilename("awcl_public", "json")).toBe("actweave-aap-awcl_public.json");
  });
});
