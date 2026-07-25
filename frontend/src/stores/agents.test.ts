import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../services/api";
import type { Agent, AgentCapabilityBinding, Workspace } from "../types/domain";
import { useAgentStore } from "./agents";
import { useWorkspaceStore } from "./workspaces";

vi.mock("../services/api", async () => {
  const actual = await vi.importActual<typeof import("../services/api")>("../services/api");
  return { ...actual, apiClient: { delete: vi.fn(), get: vi.fn(), patch: vi.fn(), post: vi.fn(), put: vi.fn() } };
});

describe("v1 Agent, Capability, and Binding store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    useWorkspaceStore().items = [{ id: "workspace-1" }, { id: "workspace-2" }] as Workspace[];
    vi.resetAllMocks();
  });

  it("aggregates workspace-scoped Agent reads and locally pages derived fields", async () => {
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: { items: [agentDTO("agent-1", { name: "Orders" })] } })
      .mockResolvedValueOnce({ data: { items: [agentDTO("agent-2", { name: "Finance", status: "DISABLED" })] } });
    const store = useAgentStore();

    await store.loadAgentPage({ query: "finance", status: "DISABLED", page: 1, pageSize: 10 });

    expect(apiClient.get).toHaveBeenNthCalledWith(1, "/workspaces/workspace-1/agents");
    expect(apiClient.get).toHaveBeenNthCalledWith(2, "/workspaces/workspace-2/agents");
    expect(store.pageItems).toMatchObject([{ id: "agent-2", workspaceId: "workspace-2", toolsCount: 2, workflowsCount: 1 }]);
    expect(store.pageItems[0].systemPrompt).toBe("");
  });

  it("scopes Agent page reads to the requested Workspace", async () => {
    vi.mocked(apiClient.get).mockResolvedValueOnce({
      data: { items: [agentDTO("agent-2", { name: "Finance", status: "DISABLED" })] },
    });
    const store = useAgentStore();

    await store.loadAgentPage({ workspaceId: "workspace-2", page: 1, pageSize: 10 });

    expect(apiClient.get).toHaveBeenCalledTimes(1);
    expect(apiClient.get).toHaveBeenCalledWith("/workspaces/workspace-2/agents");
    expect(store.pageItems).toMatchObject([{ id: "agent-2", workspaceId: "workspace-2" }]);
  });

  it("uses exact create and update DTO allowlists without writing derived facts or prompt plaintext on PATCH", async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: agentDTO("agent-1") });
    vi.mocked(apiClient.patch).mockResolvedValueOnce({ data: agentDTO("agent-1", { name: "Updated", lockVersion: 2 }) });
    const store = useAgentStore();
    const draft = agentValue({ id: "", systemPrompt: "Initial prompt", toolsCount: 99, workflowsCount: 88 });

    const created = await store.createAgent(draft);
    expect(apiClient.post).toHaveBeenCalledWith("/workspaces/workspace-1/agents", {
      name: "Agent",
      roleDescription: "Assistant",
      modelConfigId: "model-1",
      isDefault: false,
      systemPrompt: "Initial prompt",
    });

    await store.updateAgent(created.id, { ...created, name: "Updated", systemPrompt: "must-not-patch", toolsCount: 77 });
    expect(apiClient.patch).toHaveBeenCalledWith("/workspaces/workspace-1/agents/agent-1", {
      name: "Updated",
      roleDescription: "Assistant",
      modelConfigId: "model-1",
      status: "ACTIVE",
      lockVersion: 1,
    });
  });

  it("previews and accepts Prompt revisions through the v1 command and deletes with lockVersion", async () => {
    vi.mocked(apiClient.post)
      .mockResolvedValueOnce({ data: promptResult(true) })
      .mockResolvedValueOnce({ data: promptResult(false) });
    vi.mocked(apiClient.delete).mockResolvedValueOnce({ data: undefined });
    const store = useAgentStore();
    const agent = agentValue();
    store.items = [agent];

    await store.enhanceAgentPrompt(agent, "Improve boundaries", { preview: true });
    await store.enhanceAgentPrompt(agent, "Improve boundaries", { preview: false, lockVersion: 1 });
    await store.deleteAgent(agent.id);

    expect(apiClient.post).toHaveBeenNthCalledWith(
      1,
      "/workspaces/workspace-1/agents/agent-1:enhance-prompt",
      { input: "Improve boundaries", preview: true },
      { timeout: 210_000 },
    );
    expect(apiClient.post).toHaveBeenNthCalledWith(
      2,
      "/workspaces/workspace-1/agents/agent-1:enhance-prompt",
      { input: "Improve boundaries", preview: false, lockVersion: 1 },
      { timeout: 210_000 },
    );
    expect(apiClient.delete).toHaveBeenCalledWith("/workspaces/workspace-1/agents/agent-1?lockVersion=1");
  });

  it("loads the unified catalog and manages follow, pin, Connection, and unbind DTOs", async () => {
    const binding = bindingValue();
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: { items: [capabilityDTO()] } })
      .mockResolvedValueOnce({ data: { items: [binding] } });
    vi.mocked(apiClient.put).mockResolvedValueOnce({ data: { ...binding, versionPolicy: "PINNED", pinnedReleaseId: "release-1", connectionId: "connection-1", lockVersion: 2 } });
    vi.mocked(apiClient.delete).mockResolvedValueOnce({ data: undefined });
    const store = useAgentStore();
    const agent = agentValue();

    await store.loadCapabilities(agent.workspaceId);
    await store.loadAgentCapabilities(agent);
    const saved = await store.bindCapability(agent, "capability-1", {
      ...binding,
      versionPolicy: "PINNED",
      pinnedReleaseId: "release-1",
      connectionId: "connection-1",
    });
    await store.unbindCapability(agent, saved);

    expect(apiClient.get).toHaveBeenNthCalledWith(1, "/workspaces/workspace-1/capabilities");
    expect(apiClient.get).toHaveBeenNthCalledWith(2, "/workspaces/workspace-1/agents/agent-1/capabilities");
    expect(apiClient.put).toHaveBeenCalledWith(
      "/workspaces/workspace-1/agents/agent-1/capabilities/capability-1",
      {
        versionPolicy: "PINNED",
        pinnedReleaseId: "release-1",
        connectionId: "connection-1",
        executionPolicyId: undefined,
        enabled: true,
        configOverrides: {},
        lockVersion: 1,
      },
    );
    expect(apiClient.delete).toHaveBeenCalledWith(
      "/workspaces/workspace-1/agents/agent-1/capabilities/capability-1?lockVersion=2",
    );
  });
});

function agentDTO(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id,
    name: "Agent",
    roleDescription: "Assistant",
    currentPromptRevisionId: "revision-1",
    modelConfigId: "model-1",
    isDefault: false,
    status: "ACTIVE",
    toolsCount: 2,
    workflowsCount: 1,
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-07-15T00:00:00Z",
    updatedAt: "2026-07-15T00:00:00Z",
    lockVersion: 1,
    ...overrides,
  };
}

function agentValue(overrides: Partial<Agent> = {}): Agent {
  return { ...agentDTO("agent-1"), workspaceId: "workspace-1", systemPrompt: "", ...overrides } as Agent;
}

function promptResult(preview: boolean) {
  return {
    runId: preview ? "run-preview" : "run-accepted",
    status: "SUCCEEDED",
    preview,
    output: "Improved prompt",
    inputObjectId: "input-object",
    outputObjectId: "output-object",
    ...(preview ? {} : { acceptedRevisionId: "revision-2", revisionNo: 2 }),
  };
}

function capabilityDTO() {
  return {
    id: "capability-1",
    kind: "TOOL",
    name: "Lookup order",
    slug: "lookup-order",
    description: "Lookup order",
    status: "ACTIVE",
    activeReleaseId: "release-1",
    boundAgentCount: 1,
    activeRelease: {
      capabilityId: "capability-1",
      releaseId: "release-1",
      kind: "TOOL",
      callableName: "lookup_order",
      callableDescription: "Lookup order",
      inputSchema: {},
      outputSchema: {},
      riskLevel: "LOW",
      sideEffectLevel: "READ",
      requiresConfirmation: false,
    },
    createdBy: "user-1",
    updatedBy: "user-1",
    lockVersion: 1,
  };
}

function bindingValue(): AgentCapabilityBinding {
  return {
    capabilityId: "capability-1",
    versionPolicy: "FOLLOW_ACTIVE",
    enabled: true,
    configOverrides: {},
    lockVersion: 1,
  };
}
