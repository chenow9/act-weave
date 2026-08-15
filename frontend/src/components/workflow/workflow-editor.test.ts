import { AxiosError } from "axios";
import { defineComponent } from "vue";
import { createPinia, setActivePinia } from "pinia";
import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createEmptyWorkflowGraphDraft } from "../../composables/workflow-generate-dock";
import WorkflowGenerateDock from "./WorkflowGenerateDock.vue";
import WorkflowGraphCanvas from "./WorkflowGraphCanvas.vue";
import WorkflowExecutionTracePanel from "./WorkflowExecutionTracePanel.vue";
import WorkflowInspector from "./WorkflowInspector.vue";
import WorkflowNodePalette from "./WorkflowNodePalette.vue";
import WorkflowTrialRunDialog from "./WorkflowTrialRunDialog.vue";
import { apiClient } from "../../services/api";
import { useWorkspaceStore } from "../../stores/workspaces";
import { useWorkflowStore } from "../../stores/workflow";
import { createTestI18n } from "../../test-utils/i18n";
import WorkflowView from "../../views/WorkflowView.vue";
import type { Workspace } from "../../types/domain";

function testI18nPlugins() {
  return [createTestI18n("zh-CN")];
}

vi.mock("../../services/api", () => ({
  apiClient: {
    delete: vi.fn(),
    get: vi.fn(),
    patch: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  },
  toAPIError: (error: { message?: string; response?: { status?: number } }) => ({
    message: error.message || "request failed",
    status: error.response?.status,
  }),
}));

function workflowFixture(id = "wf-order-cancel-draft", name = "订单取消编排") {
  return {
    id,
    workspaceId: "order",
    currentDraftId: `draft-${id}`,
    latestCompilationId: "comp-1",
    name,
    slug: id,
    description: "查询订单状态并决定后续动作。",
    status: "Draft" as const,
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-02T00:00:00Z",
    lockVersion: 1,
  };
}

function workflowSummaryFixture(id = "wf-order-cancel-draft", name = "订单取消编排") {
  return {
    id,
    workspaceId: "order",
    currentDraftId: `draft-${id}`,
    latestCompilationId: "comp-1",
    workspaceName: "订单中心",
    name,
    slug: id,
    description: "查询订单状态并决定后续动作。",
    status: "Draft" as const,
    nodeCount: 2,
    edgeCount: 1,
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-02T00:00:00Z",
    lockVersion: 1,
  };
}

function readinessFixture(overrides: Record<string, unknown> = {}) {
  return {
    stage: "TrialRequired",
    canCompile: true,
    canTrial: true,
    canValidate: true,
    canTrialRun: true,
    canPublish: false,
    hasDraft: true,
    compilationCurrent: true,
    compilationValid: true,
    trialCurrent: false,
    trialSuccessful: false,
    published: false,
    blockers: [
      {
        code: "trial_required",
        message: "Workflow must pass a trial run for the current compiled draft.",
        action: "Run a trial against the latest compiled draft.",
        severity: "error",
      },
    ],
    updatedAt: "2026-07-02T09:00:00Z",
    ...overrides,
  };
}

function workspaceFixture(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: "order",
    name: "order",
    displayName: "订单中心",
    owner: "Commerce Ops",
    mode: "PRODUCTION",
    status: "ACTIVE",
    defaultAgentId: "agent-order-default",
    modelConfigId: "model-default",
    healthScore: 99,
    toolCount: 12,
    workflowCount: 3,
    agentCount: 2,
    ...overrides,
  };
}

function draftFixture() {
  return {
    id: "draft-wf-order-cancel-draft",
    workflowId: "wf-order-cancel-draft",
    draftVersion: 2,
    schemaVersion: "workflow.graph.v1",
    graphHash: "sha256:graph-2",
    updatedBy: "user-1",
    updatedAt: "2026-06-27T09:00:00Z",
    lockVersion: 2,
    etag: '"draft-2-2"',
    graph: {
      schemaVersion: "workflow.graph.v1",
      nodes: [
        {
          id: "start",
          type: "Start" as const,
          label: "Start",
          position: { x: 120, y: 180 },
          ports: [{ key: "output", label: "Output", direction: "output" as const }],
          data: {
            inputSchema: {
              required: ["orderId"],
              properties: {
                orderId: { type: "string", description: "订单 ID" },
                dryRun: { type: "boolean", description: "仅校验" },
              },
            },
          },
          ui: {},
        },
        {
          id: "end",
          type: "End" as const,
          label: "End",
          position: { x: 420, y: 180 },
          ports: [{ key: "input", label: "Input", direction: "input" as const }],
          data: {},
          ui: {},
        },
      ],
      edges: [
        {
          id: "edge-start-end",
          sourceNodeId: "start",
          sourcePort: "output",
          targetNodeId: "end",
          targetPort: "input",
          data: {},
          ui: {},
        },
      ],
      viewport: { x: 0, y: 0, zoom: 1 },
      ui: {},
    },
  };
}

function draftFixtureFor(workflowId: string, startLabel: string) {
  return {
    ...draftFixture(),
    workflowId,
    graph: {
      ...draftFixture().graph,
      nodes: draftFixture().graph.nodes.map((node) => (node.id === "start" ? { ...node, label: startLabel } : node)),
    },
  };
}

function compactSchemaDraftFixture() {
  return {
    ...draftFixture(),
    graph: {
      ...draftFixture().graph,
      nodes: draftFixture().graph.nodes.map((node) =>
        node.id === "start"
          ? {
              ...node,
              data: {
                inputSchema: {
                  orderId: "string",
                  dryRun: "boolean",
                },
              },
            }
          : node,
      ),
    },
  };
}

function compilationFixture(overrides: Record<string, unknown> = {}) {
  return {
    id: "comp-1",
    workflowId: "wf-order-cancel-draft",
    draftId: "draft-wf-order-cancel-draft",
    draftVersion: 2,
    graphHash: "sha256:graph-2",
    compilerVersion: "workflow-compiler.v1",
    status: "INVALID" as const,
    spec: { workflowId: "wf-order-cancel-draft", nodes: [] },
    plan: { workflowId: "wf-order-cancel-draft", nodes: [] },
    issues: [
      {
        code: "missing-output",
        message: "End node is missing output mapping",
        severity: "error",
        sourceStage: "spec" as const,
        nodeId: "end",
      },
    ],
    planHash: "sha256:plan-1",
    compiledBy: "user-1",
    compiledAt: "2026-06-27T09:05:00Z",
    ...overrides,
  };
}

function toolDTO(id: string, name: string, status = "ACTIVE") {
  return {
    id,
    providerId: "provider-1",
    sourceAssetId: "asset-1",
    sourceEndpointId: "endpoint-1",
    defaultConnectionId: "conn-1",
    activeReleaseId: status === "ACTIVE" ? `release-${id}` : undefined,
    name,
    slug: id.replaceAll(".", "-"),
    description: name,
    status,
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-02T00:00:00Z",
    lockVersion: 1,
  };
}

function toolVersionDTO(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id: `version-${id}`,
    toolId: id,
    versionNo: 1,
    lifecycleStatus: "PUBLISHED",
    executorType: "HTTP",
    actionSchemaVersion: "http.v1",
    actionConfig: {},
    inputSchema: {},
    outputSchema: {},
    errorMappings: {},
    runtimePolicy: {},
    defaultConnectionId: "conn-1",
    createdBy: "user-1",
    updatedBy: "user-1",
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-02T00:00:00Z",
    lockVersion: 1,
    ...overrides,
  };
}

function executionFixture(overrides: Record<string, unknown> = {}) {
  return {
    id: "exec-trial",
    workflowId: "wf-order-cancel-draft",
    workflowVersion: "v0.1.0",
    workspaceId: "order",
    trigger: "Workflow Trial Run",
    userId: "user-chen-ops",
    traceId: "trace-exec-trial",
    status: "Failed" as const,
    durationMs: 36,
    inputSummary: '{"orderId":"A10293"}',
    outputSummary: "Workflow trial run failed",
    errorMessage: "tool timeout",
    rawPayloadObjectAddress: "s3://actweave-executions/exec-trial/payload.json",
    steps: [
      {
        id: "step-start",
        executionId: "exec-trial",
        name: "Start",
        nodeId: "start",
        nodeType: "Start",
        status: "passed",
        inputSummary: '{"orderId":"A10293"}',
        outputSummary: "input accepted",
        durationMs: 1,
      },
      {
        id: "step-query",
        executionId: "exec-trial",
        name: "Runtime Call",
        nodeId: "query",
        nodeType: "Tool",
        status: "failed",
        inputSummary: "orderId=A10293",
        outputSummary: "",
        errorMessage: "tool timeout",
        durationMs: 35,
      },
    ],
    ...overrides,
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function mockWorkflowAssets(
  workflows: Array<Record<string, unknown>>,
  executions: Array<Record<string, unknown>> = [],
) {
  void executions;
  const workflowReadiness = (workflows[0]?.readiness as Record<string, unknown> | undefined) || readinessFixture();
  const mock = vi.mocked(apiClient.get).mockResolvedValueOnce({ data: { items: workflows } });
  workflows.forEach((workflow) => mock.mockResolvedValueOnce({ data: workflow.readiness || readinessFixture() }));
  const queueResolved = mock.mockResolvedValueOnce.bind(mock);
  mock.mockResolvedValueOnce = ((response: { data?: Record<string, unknown> }) => {
    const data = response?.data;
    if (data?.draft) {
      if (data.latestCompilation) {
        useWorkflowStore().activeCompilation = data.latestCompilation as never;
      }
      queueResolved({ data: data.draft, headers: { etag: '"draft-2-2"' } });
      queueResolved({ data: workflowReadiness });
      return mock;
    }
    if (data?.workflow) {
      queueResolved({ data: data.workflow });
      queueResolved({ data: (data.workflow as Record<string, unknown>).readiness || readinessFixture() });
      return mock;
    }
    if (data?.revisions) {
      queueResolved({ data: { items: data.revisions } });
      return mock;
    }
    if (data?.approvals) return mock;
    queueResolved(response);
    return mock;
  }) as typeof mock.mockResolvedValueOnce;

  const putMock = vi.mocked(apiClient.put);
  const queuePut = putMock.mockResolvedValueOnce.bind(putMock);
  putMock.mockResolvedValueOnce = ((response: { data?: Record<string, unknown> }) => {
    const data = response?.data;
    if (data?.draft) {
      if (data.latestCompilation) {
        useWorkflowStore().activeCompilation = data.latestCompilation as never;
      }
      queuePut({ data: data.draft, headers: { etag: '"draft-3-3"' } });
      queueResolved({
        data: readinessFixture({ stage: "CompileRequired", canTrial: false, compilationCurrent: false }),
      });
      return putMock;
    }
    queuePut(response);
    return putMock;
  }) as typeof putMock.mockResolvedValueOnce;
  return mock;
}

const VueFlowStub = defineComponent({
  name: "VueFlow",
  props: {
    nodes: { type: Array, default: () => [] },
    edges: { type: Array, default: () => [] },
    fitViewOnInit: { type: Boolean, default: false },
  },
  emits: ["node-drag-stop", "move-start", "viewport-change-end", "connect", "edge-click", "edge-context-menu"],
  template: `
    <div class="vue-flow-stub" :data-fit-view-on-init="String(fitViewOnInit)">
      <button data-action="drag-stop" type="button" @click="$emit('node-drag-stop', { node: nodes[1] })">drag</button>
      <button
        data-action="connect-valid"
        type="button"
        @click="$emit('connect', { source: 'tool-1', sourceHandle: 'result', target: 'end', targetHandle: 'input' })"
      >
        connect valid
      </button>
      <button
        data-action="connect-invalid"
        type="button"
        @click="$emit('connect', { source: 'tool-1', sourceHandle: 'missing-source', target: 'end', targetHandle: 'missing-target' })"
      >
        connect invalid
      </button>
      <button
        data-action="viewport-change-end"
        type="button"
        @click="$emit('viewport-change-end', { x: 24, y: -12, zoom: 1.3 })"
      >
        viewport
      </button>
      <button data-action="move-start" type="button" @click="$emit('move-start')">move start</button>
      <button
        v-if="edges[0]"
        data-action="edge-click"
        type="button"
        @click="$emit('edge-click', { edge: edges[0] })"
      >
        edge click
      </button>
      <button
        v-if="edges[0]"
        data-action="edge-context-menu"
        type="button"
        @click="$emit('edge-context-menu', { edge: edges[0], event: { clientX: 320, clientY: 180 } })"
      >
        edge context menu
      </button>
      <span
        v-for="edge in edges"
        :key="edge.id"
        class="vue-flow-edge-label"
        :data-edge-id="edge.id"
      >
        {{ edge.label || '' }}
      </span>
      <template v-for="node in nodes" :key="node.id">
        <slot name="node-workflow" :id="node.id" :data="node.data" />
      </template>
    </div>
  `,
});

const HandleStub = defineComponent({
  name: "Handle",
  props: {
    id: { type: String, required: true },
    type: { type: String, required: true },
    position: { type: String, required: true },
  },
  template: `
    <span
      class="handle-stub"
      :data-handle-id="id"
      :data-handle-type="type"
      :data-handle-position="position"
    />
  `,
});

const AppSelectStub = defineComponent({
  name: "AppSelect",
  props: {
    modelValue: { type: [String, Number, Boolean], default: "" },
    options: { type: Array, default: () => [] },
  },
  emits: ["update:modelValue"],
  template: `
    <div class="app-select-stub" :data-model-value="String(modelValue)">
      <button
        v-for="option in options"
        :key="String(option.value)"
        class="app-select-option"
        type="button"
        :data-label="option.label"
        :data-value="String(option.value)"
        @click="$emit('update:modelValue', option.value)"
      >
        {{ option.label }}
      </button>
    </div>
  `,
});

describe("workflow graph canvas", () => {
  it("emits node position and viewport updates from vue-flow events", async () => {
    const wrapper = mount(WorkflowGraphCanvas, {
      props: {
        graph: draftFixture().graph,
        selectedNodeId: "start",
      },
      global: {
        stubs: {
          VueFlow: VueFlowStub,
          Handle: HandleStub,
        },
      },
    });

    await wrapper.get('[data-action="drag-stop"]').trigger("click");
    await wrapper.get('[data-action="viewport-change-end"]').trigger("click");

    expect(wrapper.emitted("update-node-position")?.[0]?.[0]).toEqual({
      nodeId: "end",
      position: { x: 420, y: 180 },
    });
    expect(wrapper.emitted("update-viewport")?.[0]?.[0]).toEqual({
      x: 24,
      y: -12,
      zoom: 1.3,
    });
    expect(wrapper.get(".vue-flow-stub").attributes("data-fit-view-on-init")).toBe("true");
  });

  it("renders a single shared exit handle so branches leave from one point", async () => {
    const wrapper = mount(WorkflowGraphCanvas, {
      props: {
        graph: {
          schemaVersion: "workflow.graph.v1",
          nodes: [
            {
              id: "tool-1",
              type: "Tool",
              label: "Query Order",
              position: { x: 120, y: 160 },
              // Legacy multi-exit graphs still render as one geometric output.
              ports: [
                { key: "input", label: "Input", direction: "input" },
                { key: "result", label: "Result", direction: "output" },
                { key: "fallback", label: "Fallback", direction: "output" },
              ],
              data: {},
              ui: {},
            },
            {
              id: "end",
              type: "End",
              label: "End",
              position: { x: 420, y: 160 },
              ports: [{ key: "input", label: "Input", direction: "input" }],
              data: {},
              ui: {},
            },
          ],
          edges: [
            {
              id: "edge-tool-end",
              sourceNodeId: "tool-1",
              sourcePort: "result",
              targetNodeId: "end",
              targetPort: "input",
              data: {},
              ui: {},
            },
          ],
          viewport: { x: 0, y: 0, zoom: 1 },
          ui: {},
        },
        selectedNodeId: "tool-1",
      },
      global: {
        stubs: {
          VueFlow: VueFlowStub,
          Handle: HandleStub,
        },
      },
    });

    // Only one source handle (first output / shared exit) — no second exit dot.
    expect(wrapper.find('[data-node-id="tool-1"] [data-handle-id="result"]').attributes("data-handle-type")).toBe(
      "source",
    );
    expect(wrapper.find('[data-node-id="tool-1"] [data-handle-id="fallback"]').exists()).toBe(false);
    expect(wrapper.find('[data-node-id="end"] [data-handle-id="input"]').attributes("data-handle-type")).toBe("target");
    expect(wrapper.find('[data-node-id="end"] [data-handle-id="output"]').exists()).toBe(false);

    await wrapper.get('[data-action="connect-valid"]').trigger("click");
    // Handle id is ignored; connection always uses the shared primary ports.
    await wrapper.get('[data-action="connect-invalid"]').trigger("click");

    expect(wrapper.emitted("connect-nodes")).toEqual([
      [
        {
          sourceNodeId: "tool-1",
          sourcePort: "result",
          targetNodeId: "end",
          targetPort: "input",
        },
      ],
      [
        {
          sourceNodeId: "tool-1",
          sourcePort: "result",
          targetNodeId: "end",
          targetPort: "input",
        },
      ],
    ]);
  });

  it("emits edge selection when a canvas edge is clicked", async () => {
    const wrapper = mount(WorkflowGraphCanvas, {
      props: {
        graph: draftFixture().graph,
        selectedNodeId: "start",
      },
      global: {
        stubs: {
          VueFlow: VueFlowStub,
          Handle: HandleStub,
        },
      },
    });

    await wrapper.get('[data-action="edge-click"]').trigger("click");

    expect(wrapper.emitted("select-edge")).toEqual([["edge-start-end"]]);
  });

  it("renders branch labels from workflow edge data", () => {
    const graph = {
      ...draftFixture().graph,
      edges: [
        {
          ...draftFixture().graph.edges[0],
          data: { branch: "true" },
        },
      ],
    };

    const wrapper = mount(WorkflowGraphCanvas, {
      props: {
        graph,
        selectedNodeId: "start",
      },
      global: {
        stubs: {
          VueFlow: VueFlowStub,
          Handle: HandleStub,
        },
      },
    });

    expect(wrapper.get('[data-edge-id="edge-start-end"]').text()).toBe("条件成立");
  });

  it("emits a node context menu event when a node is right-clicked", async () => {
    const wrapper = mount(WorkflowGraphCanvas, {
      props: {
        graph: draftFixture().graph,
        selectedNodeId: "start",
      },
      global: {
        stubs: {
          VueFlow: VueFlowStub,
          Handle: HandleStub,
        },
      },
    });

    await wrapper.get('[data-node-id="start"]').trigger("contextmenu", { clientX: 140, clientY: 160 });

    expect(wrapper.emitted("open-node-context-menu")?.[0]?.[0]).toEqual({
      nodeId: "start",
      position: { x: 140, y: 160 },
    });
  });

  it("emits an edge context menu event when a canvas edge is right-clicked", async () => {
    const wrapper = mount(WorkflowGraphCanvas, {
      props: {
        graph: draftFixture().graph,
        selectedNodeId: "start",
      },
      global: {
        stubs: {
          VueFlow: VueFlowStub,
          Handle: HandleStub,
        },
      },
    });

    await wrapper.get('[data-action="edge-context-menu"]').trigger("click");

    expect(wrapper.emitted("open-edge-context-menu")?.[0]?.[0]).toEqual({
      edgeId: "edge-start-end",
      position: { x: 320, y: 180 },
    });
  });

  it("shows empty-canvas copy when empty is true and the graph has no nodes", () => {
    const wrapper = mount(WorkflowGraphCanvas, {
      props: {
        graph: createEmptyWorkflowGraphDraft(),
        selectedNodeId: "",
        empty: true,
      },
      global: {
        stubs: {
          VueFlow: VueFlowStub,
          Handle: HandleStub,
        },
      },
    });

    expect(wrapper.find(".workflow-graph-empty").exists()).toBe(true);
    expect(wrapper.text()).toContain("描述流程后，草稿会出现在这里");
    expect(wrapper.find('[data-node-id="start"]').exists()).toBe(false);
  });

  it("hides empty-canvas copy when empty is false even if the graph has no nodes", () => {
    const wrapper = mount(WorkflowGraphCanvas, {
      props: {
        graph: createEmptyWorkflowGraphDraft(),
        selectedNodeId: "",
        empty: false,
      },
      global: {
        stubs: {
          VueFlow: VueFlowStub,
          Handle: HandleStub,
        },
      },
    });

    expect(wrapper.find(".workflow-graph-empty").exists()).toBe(false);
  });

  it("locks drag and connect while lockInteraction is true", async () => {
    const wrapper = mount(WorkflowGraphCanvas, {
      props: {
        graph: draftFixture().graph,
        selectedNodeId: "start",
        lockInteraction: true,
      },
      global: {
        stubs: {
          VueFlow: VueFlowStub,
          Handle: HandleStub,
        },
      },
    });

    await wrapper.get('[data-action="drag-stop"]').trigger("click");
    await wrapper.get('[data-action="connect-valid"]').trigger("click");
    await wrapper.get('[data-node-id="start"]').trigger("contextmenu", { clientX: 140, clientY: 160 });

    expect(wrapper.emitted("update-node-position")).toBeUndefined();
    expect(wrapper.emitted("connect-nodes")).toBeUndefined();
    expect(wrapper.emitted("open-node-context-menu")).toBeUndefined();
  });

  it("flashes a highlight when applyHighlightEpoch increments", async () => {
    vi.useFakeTimers();
    const wrapper = mount(WorkflowGraphCanvas, {
      props: {
        graph: draftFixture().graph,
        selectedNodeId: "start",
        applyHighlightEpoch: 0,
      },
      global: {
        stubs: {
          VueFlow: VueFlowStub,
          Handle: HandleStub,
        },
      },
    });

    expect(wrapper.get(".workflow-graph-canvas").classes()).not.toContain("is-apply-highlight");
    await wrapper.setProps({ applyHighlightEpoch: 1 });
    await wrapper.vm.$nextTick();
    expect(wrapper.get(".workflow-graph-canvas").classes()).toContain("is-apply-highlight");

    vi.advanceTimersByTime(180);
    await wrapper.vm.$nextTick();
    expect(wrapper.get(".workflow-graph-canvas").classes()).not.toContain("is-apply-highlight");
    vi.useRealTimers();
  });
});

describe("workflow inspector typed editors", () => {
  it("shows advanced workflow node entries in the palette", async () => {
    const wrapper = mount(WorkflowNodePalette, {
      props: {
        variableRefs: ["{{input.orderId}}", "{{nodeOutputs.lookup-1.status}}"],
      },
      global: {
        plugins: testI18nPlugins(),
      },
    });

    expect(wrapper.text()).toContain("子流程");
    expect(wrapper.text()).toContain("并行分支");
    // zh-CN ForEach palette title (workflow.nodeForEach).
    expect(wrapper.text()).toContain("循环");

    await wrapper.findAll(".workflow-node-library-item")[2].trigger("click");

    expect(wrapper.emitted("add-node")?.[0]?.[0]).toBe("Condition");
  });

  it("does not emit add-node when the palette is disabled", async () => {
    const wrapper = mount(WorkflowNodePalette, {
      props: {
        variableRefs: ["{{input.orderId}}"],
        disabled: true,
      },
      global: {
        plugins: testI18nPlugins(),
      },
    });

    await wrapper.findAll(".workflow-node-library-item")[2].trigger("click");

    expect(wrapper.emitted("add-node")).toBeUndefined();
    expect(wrapper.findAll(".workflow-node-library-item")[2].attributes("disabled")).toBeDefined();
  });

  it("disables generate submit without workspace context or a prompt", async () => {
    const wrapper = mount(WorkflowGenerateDock, {
      props: {
        hasWorkspaceContext: false,
      },
      global: {
        plugins: testI18nPlugins(),
      },
    });

    const textarea = wrapper.get("textarea");
    await textarea.setValue("供应商准入");
    expect(wrapper.get('[data-action="submit-generate"]').attributes("disabled")).toBeDefined();
  });

  it("fills the generate dock textarea from example chips without submitting", async () => {
    const wrapper = mount(WorkflowGenerateDock, {
      props: {
        hasWorkspaceContext: true,
      },
      global: {
        plugins: testI18nPlugins(),
      },
    });

    const textarea = wrapper.get("textarea");
    const submit = wrapper.get('[data-action="submit-generate"]');
    expect(submit.attributes("disabled")).toBeDefined();
    expect(wrapper.text()).toContain("试试");

    await wrapper.findAll(".workflow-generate-example")[0].trigger("click");
    expect((textarea.element as HTMLTextAreaElement).value).toContain("供应商准入");
    expect(submit.attributes("disabled")).toBeUndefined();
  });

  it("builds start input schema fields without requiring raw JSON and surfaces raw JSON parse errors", async () => {
    const wrapper = mount(WorkflowInspector, {
      props: {
        node: {
          id: "start",
          type: "Start",
          label: "Start",
          position: { x: 120, y: 180 },
          ports: [{ key: "output", label: "Output", direction: "output" }],
          data: {},
          ui: {},
        },
        variableRefs: [],
      },
      global: {
        plugins: testI18nPlugins(),
        stubs: {
          AppSelect: AppSelectStub,
        },
      },
    });

    expect(wrapper.find(".workflow-schema-builder").exists()).toBe(true);

    await wrapper.get('[data-action="add-schema-field"]').trigger("click");
    await wrapper.get('input[name="schema-field-key-0"]').setValue("orderId");
    await wrapper.get('.app-select-stub[name="schema-field-type-0"] [data-value="string"]').trigger("click");
    await wrapper.get('input[name="schema-field-description-0"]').setValue("订单 ID");
    await wrapper.get('input[name="schema-field-required-0"]').setValue(true);
    await wrapper.get('input[name="schema-field-enum-0"]').setValue("A10293,B20991");
    await wrapper.get('input[name="schema-field-example-0"]').setValue("A10293");

    expect(wrapper.emitted("update-node-data")?.at(-1)?.[0]).toEqual({
      key: "inputSchema",
      value: {
        type: "object",
        properties: {
          orderId: {
            type: "string",
            description: "订单 ID",
            enum: ["A10293", "B20991"],
            example: "A10293",
          },
        },
        required: ["orderId"],
      },
    });

    await wrapper.get('button[data-mode="raw-schema"]').trigger("click");
    await wrapper.get('textarea[name="workflow-schema-raw-json"]').setValue("{bad-json");

    expect(wrapper.text()).toContain("JSON 格式不正确");
  });

  it("uses the variable picker for end output references instead of free-typing paths", async () => {
    const wrapper = mount(WorkflowInspector, {
      props: {
        node: {
          id: "end",
          type: "End",
          label: "End",
          position: { x: 420, y: 180 },
          ports: [{ key: "input", label: "Input", direction: "input" }],
          data: {},
          ui: {},
        },
        variableRefs: ["{{input.orderId}}", "{{nodeOutputs.lookup-1.status}}"],
      },
      global: {
        plugins: testI18nPlugins(),
        stubs: {
          AppSelect: AppSelectStub,
        },
      },
    });

    expect(wrapper.find(".workflow-variable-picker").exists()).toBe(true);
    expect(wrapper.text()).toContain("input.orderId");
    expect(wrapper.text()).toContain("nodeOutputs.lookup-1.status");

    await wrapper.get('.workflow-variable-picker [data-value="input.orderId"]').trigger("click");

    expect(wrapper.emitted("update-node-data")?.at(-1)?.[0]).toEqual({
      key: "output",
      value: {
        kind: "ref",
        path: "input.orderId",
      },
    });
  });

  it("renders typed fields for HTTP nodes and emits merged payload for mapped input", async () => {
    const wrapper = mount(WorkflowInspector, {
      props: {
        node: {
          id: "http-1",
          type: "HTTP",
          label: "HTTP 1",
          position: { x: 220, y: 180 },
          ports: [
            { key: "input", label: "Input", direction: "input" },
            { key: "output", label: "Output", direction: "output" },
          ],
          data: {
            method: "POST",
            endpoint: "https://api.example.com/orders/cancel",
          },
          ui: {},
        },
        variableRefs: ["{{input.orderId}}", "{{nodeOutputs.lookup-1.status}}"],
      },
      global: {
        plugins: testI18nPlugins(),
        stubs: {
          AppSelect: AppSelectStub,
        },
      },
    });

    expect(wrapper.text()).toContain("请求方法");
    expect(wrapper.text()).toContain("请求地址");
    expect(wrapper.text()).toContain("输入绑定");
    expect(wrapper.text()).toContain("运行语义");
    expect(wrapper.text()).toContain("返回 request/status 摘要");
    expect(wrapper.find('input[name="node-http-endpoint"]').exists()).toBe(true);

    await wrapper.get('[data-action="http-input-mode-mapping"]').trigger("click");
    await wrapper.get('input[name="http-input-key-0"]').setValue("orderId");
    await wrapper.get('.workflow-advanced-input-select [data-value="input.orderId"]').trigger("click");

    expect(wrapper.emitted("update-node-data")?.at(-1)?.[0]).toEqual({
      key: "__merge",
      value: {
        inputMapping: {
          orderId: {
            kind: "ref",
            path: "input.orderId",
          },
        },
      },
    });
  });

  it("renders typed fields for SubWorkflow nodes", async () => {
    const wrapper = mount(WorkflowInspector, {
      props: {
        node: {
          id: "subworkflow-1",
          type: "SubWorkflow",
          label: "SubWorkflow 1",
          position: { x: 220, y: 180 },
          ports: [
            { key: "input", label: "Input", direction: "input" },
            { key: "output", label: "Output", direction: "output" },
          ],
          data: {
            workflowId: "wf-published-order-cancel",
            inputMapping: {
              orderId: {
                kind: "ref",
                path: "input.orderId",
              },
            },
          },
          ui: {},
        },
        variableRefs: ["{{input.orderId}}", "{{nodeOutputs.lookup-1.status}}"],
      },
      global: {
        plugins: testI18nPlugins(),
        stubs: {
          AppSelect: AppSelectStub,
        },
      },
    });

    expect(wrapper.text()).toContain("目标 Workflow ID");
    expect(wrapper.text()).toContain("输入绑定");
    expect(wrapper.text()).toContain("运行语义");
    expect(wrapper.text()).toContain("执行已发布 workflow revision");
    expect(wrapper.find('input[name="node-subworkflow-id"]').exists()).toBe(true);
  });

  it("renders typed fields for Parallel nodes", async () => {
    const wrapper = mount(WorkflowInspector, {
      props: {
        node: {
          id: "parallel-1",
          type: "Parallel",
          label: "Parallel 1",
          position: { x: 220, y: 180 },
          ports: [
            { key: "input", label: "Input", direction: "input" },
            { key: "output", label: "Output", direction: "output" },
          ],
          data: {
            branches: ["risk-check", "inventory-sync"],
          },
          ui: {},
        },
        variableRefs: ["{{input.orderId}}"],
      },
      global: {
        plugins: testI18nPlugins(),
      },
    });

    expect(wrapper.text()).toContain("分支列表");
    expect(wrapper.text()).toContain("运行语义");
    expect(wrapper.text()).toContain("按分支顺序写入 trace");
    expect(wrapper.text()).toContain("risk-check");
    expect(wrapper.text()).toContain("inventory-sync");
  });

  it("renders typed fields for ForEach nodes and supports foreach.item references", async () => {
    const wrapper = mount(WorkflowInspector, {
      props: {
        node: {
          id: "foreach-1",
          type: "ForEach",
          label: "ForEach 1",
          position: { x: 220, y: 180 },
          ports: [
            { key: "input", label: "Input", direction: "input" },
            { key: "output", label: "Output", direction: "output" },
          ],
          data: {
            collection: {
              kind: "ref",
              path: "input.orders",
            },
            itemAlias: "order",
            concurrency: 3,
            output: {
              items: {
                kind: "ref",
                path: "nodeOutputs.lookup-1.items",
              },
              count: {
                kind: "ref",
                path: "nodeOutputs.lookup-1.count",
              },
            },
          },
          ui: {},
        },
        variableRefs: ["{{input.orders}}", "{{nodeOutputs.lookup-1.items}}", "{{foreach.item.id}}"],
      },
      global: {
        plugins: testI18nPlugins(),
        stubs: {
          AppSelect: AppSelectStub,
        },
      },
    });

    expect(wrapper.text()).toContain("集合引用");
    expect(wrapper.text()).toContain("迭代别名");
    expect(wrapper.text()).toContain("并发度");
    expect(wrapper.text()).toContain("foreach.item");
    expect(wrapper.text()).toContain("运行语义");
    expect(wrapper.text()).toContain("变量选择器");
    expect(wrapper.text()).toContain("loop output mapping");
    expect(wrapper.find('input[name="node-foreach-item-alias"]').exists()).toBe(true);
  });
});

describe("workflow trial run dialog", () => {
  it("renders fields from the start-node input schema and emits typed input", async () => {
    const wrapper = mount(WorkflowTrialRunDialog, {
      props: {
        visible: true,
        inputSchema: [
          { key: "orderId", type: "string", required: true, description: "订单 ID" },
          { key: "dryRun", type: "boolean", required: false, description: "仅校验" },
        ],
      },
    });

    expect(wrapper.text()).toContain("订单 ID");
    expect(wrapper.get('input[name="orderId"]').attributes("required")).toBeDefined();

    await wrapper.get('input[name="orderId"]').setValue("A10293");
    await wrapper.get('input[name="dryRun"]').setValue(true);
    await wrapper.get('button[data-action="submit-trial-run"]').trigger("click");

    expect(wrapper.emitted("submit")?.[0]?.[0]).toEqual({
      input: { orderId: "A10293", dryRun: true },
      outboundCredentials: undefined,
    });
  });

  it("requires mandatory inputs and omits untouched optional fields", async () => {
    const wrapper = mount(WorkflowTrialRunDialog, {
      props: {
        visible: true,
        inputSchema: [
          { key: "orderId", type: "string", required: true, description: "订单 ID" },
          { key: "dryRun", type: "boolean", required: false, description: "仅校验" },
        ],
      },
    });

    await wrapper.get('button[data-action="submit-trial-run"]').trigger("click");
    expect(wrapper.emitted("submit")).toBeUndefined();

    await wrapper.get('input[name="orderId"]').setValue("A10293");
    await wrapper.get('button[data-action="submit-trial-run"]').trigger("click");

    expect(wrapper.emitted("submit")?.[0]?.[0]).toEqual({
      input: { orderId: "A10293" },
      outboundCredentials: undefined,
    });
  });

  it("allows required boolean fields to submit false without toggling", async () => {
    const wrapper = mount(WorkflowTrialRunDialog, {
      props: {
        visible: true,
        inputSchema: [{ key: "confirmOnly", type: "boolean", required: true, description: "仅确认" }],
      },
    });

    await wrapper.get('button[data-action="submit-trial-run"]').trigger("click");

    expect(wrapper.emitted("submit")?.[0]?.[0]).toEqual({
      input: { confirmOnly: false },
      outboundCredentials: undefined,
    });
  });

  it("submits raw JSON input and reports invalid JSON", async () => {
    const wrapper = mount(WorkflowTrialRunDialog, {
      props: {
        visible: true,
        inputSchema: [{ key: "orderId", type: "string", required: true, description: "订单 ID" }],
      },
    });

    await wrapper.get('button[data-mode="raw"]').trigger("click");
    await wrapper.get('textarea[name="raw-json-input"]').setValue('{"orderId":"A10293","retry":2}');
    await wrapper.get('button[data-action="submit-trial-run"]').trigger("click");

    expect(wrapper.emitted("submit")?.[0]?.[0]).toEqual({
      input: { orderId: "A10293", retry: 2 },
      outboundCredentials: undefined,
    });

    await wrapper.get('textarea[name="raw-json-input"]').setValue("{bad-json");
    await wrapper.get('button[data-action="submit-trial-run"]').trigger("click");

    expect(wrapper.emitted("submit")).toHaveLength(1);
    expect(wrapper.text()).toContain("JSON 格式不正确");
  });

  it("can reuse the last successful trial input when provided", async () => {
    const wrapper = mount(WorkflowTrialRunDialog, {
      props: {
        visible: true,
        inputSchema: [{ key: "orderId", type: "string", required: true, description: "订单 ID" }],
        lastSuccessfulInput: { orderId: "LAST-1", dryRun: true },
      },
    });

    await wrapper.get('button[data-mode="reuse"]').trigger("click");
    await wrapper.get('button[data-action="submit-trial-run"]').trigger("click");

    expect(wrapper.text()).toContain("LAST-1");
    expect(wrapper.emitted("submit")?.[0]?.[0]).toEqual({
      input: { orderId: "LAST-1", dryRun: true },
      outboundCredentials: undefined,
    });
  });

  it("keeps the dialog open and blocks duplicate submission while submitting", async () => {
    const wrapper = mount(WorkflowTrialRunDialog, {
      props: {
        visible: true,
        submitting: true,
        inputSchema: [{ key: "orderId", type: "string", required: true, description: "订单 ID" }],
      },
    });

    expect(wrapper.get('button[data-action="submit-trial-run"]').attributes("disabled")).toBeDefined();
    expect(wrapper.text()).toContain("正在模拟试运行");

    await wrapper.get('button[data-action="submit-trial-run"]').trigger("click");
    await wrapper.get(".workflow-trial-run-header .ghost-button").trigger("click");
    await wrapper.get(".workflow-trial-run-actions .ghost-button").trigger("click");
    await wrapper.get(".workflow-trial-run-backdrop").trigger("click");
    await wrapper.get(".workflow-trial-run-dialog").trigger("keydown", { key: "Escape" });

    expect(wrapper.emitted("submit")).toBeUndefined();
    expect(wrapper.emitted("close")).toBeUndefined();
  });

  it("keeps keyboard focus inside the trial-run dialog", async () => {
    const wrapper = mount(WorkflowTrialRunDialog, {
      attachTo: document.body,
      props: {
        visible: true,
        inputSchema: [{ key: "orderId", type: "string", required: true, description: "订单 ID" }],
      },
    });
    const dialog = wrapper.get(".workflow-trial-run-dialog").element as HTMLElement;
    const closeButton = wrapper.get(".workflow-trial-run-header .ghost-button").element as HTMLElement;
    const submitButton = wrapper.get('button[data-action="submit-trial-run"]').element as HTMLElement;
    for (const control of wrapper.findAll("button, input, textarea")) {
      Object.defineProperty(control.element, "offsetParent", { configurable: true, value: dialog });
    }

    submitButton.focus();
    await wrapper.get(".workflow-trial-run-dialog").trigger("keydown", { key: "Tab" });
    expect(document.activeElement).toBe(closeButton);

    await wrapper.get(".workflow-trial-run-dialog").trigger("keydown", { key: "Tab", shiftKey: true });
    expect(dialog.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).not.toBe(document.body);

    await wrapper.unmount();
  });
});

describe("workflow execution trace panel", () => {
  it("shows execution metadata and emits node selection from a failed step", async () => {
    const wrapper = mount(WorkflowExecutionTracePanel, {
      props: {
        execution: executionFixture(),
        selectedNodeId: "",
      },
    });

    expect(wrapper.text()).toContain("exec-trial");
    expect(wrapper.text()).toContain("Failed");
    expect(wrapper.text()).toContain("36 ms");
    expect(wrapper.text()).toContain("Runtime Call");
    expect(wrapper.text()).toContain("query");
    expect(wrapper.text()).toContain("Tool");
    expect(wrapper.text()).toContain("tool timeout");

    await wrapper.get('[data-step-node-id="query"]').trigger("click");

    expect(wrapper.emitted("select-node")?.[0]?.[0]).toBe("query");
  });
});

async function openWorkflowEditorFromMenu(
  wrapper: { findAll: (s: string) => any[]; vm: { $nextTick: () => Promise<void> } },
  rowIndex = 0,
) {
  const triggers = wrapper.findAll('button[aria-label="更多编排操作"]');
  const trigger = triggers[rowIndex];
  if (!trigger) {
    throw new Error(`No workflow row actions trigger at index ${rowIndex}`);
  }
  await trigger.trigger("click");
  await flushPromises();
  const menuItem =
    document.body.querySelector<HTMLButtonElement>('button[data-action-key="edit"]') ||
    wrapper.findAll('button[data-action-key="edit"]')[0]?.element;
  if (!menuItem) {
    throw new Error("Edit workflow menu item not found");
  }
  menuItem.click();
  await flushPromises();
  await wrapper.vm.$nextTick();
}

describe("workflow graph editor", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    setActivePinia(createPinia());
    vi.resetAllMocks();
    useWorkspaceStore().items = [workspaceFixture()];
    vi.stubGlobal(
      "ResizeObserver",
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    );
    if (!SVGElement.prototype.getBBox) {
      Object.defineProperty(SVGElement.prototype, "getBBox", {
        configurable: true,
        value: () => ({ x: 0, y: 0, width: 80, height: 24 }),
      });
    }
  });

  // Prefer plugins: testI18nPlugins() on mounts that render WorkflowView / inspector chrome.

  it("renders the workbench and updates node labels from the inspector", async () => {
    mockWorkflowAssets([workflowFixture()])
      .mockResolvedValueOnce({ data: { draft: draftFixture(), latestCompilation: compilationFixture() } })
      .mockResolvedValueOnce({ data: { draft: draftFixture(), latestCompilation: compilationFixture() } });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".workflow-editor-overlay-full-bleed").exists()).toBe(true);
    expect(wrapper.find(".workflow-workbench-full-bleed").exists()).toBe(true);
    expect(wrapper.find(".workflow-workbench-side-scrollable").exists()).toBe(true);
    expect(wrapper.find(".workflow-workbench").exists()).toBe(true);
    expect(wrapper.find(".workflow-node-palette").exists()).toBe(true);
    expect(wrapper.find(".workflow-graph-canvas-grid").exists()).toBe(true);
    expect(wrapper.find(".workflow-inspector").exists()).toBe(true);

    await wrapper.find('[data-node-id="start"]').trigger("click");
    const labelInput = wrapper.get('input[name="node-label"]');
    await labelInput.setValue("Order intake");

    expect(wrapper.get('[data-node-id="start"]').text()).toContain("Order intake");

    await wrapper.get(".workflow-issue-item").trigger("click");

    expect((wrapper.get('input[name="node-label"]').element as HTMLInputElement).value).toBe("End");
    expect(wrapper.get('[data-node-id="end"]').classes()).toContain("selected");

    wrapper.getComponent(WorkflowGraphCanvas).vm.$emit("update-node-position", {
      nodeId: "end",
      position: { x: 520, y: 260 },
    });
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find('input[name="node-position-x"]').exists()).toBe(false);
    expect(wrapper.find('input[name="node-position-y"]').exists()).toBe(false);
    expect(wrapper.get(".workflow-inspector-meta").text()).toContain("X 520");
    expect(wrapper.get(".workflow-inspector-meta").text()).toContain("Y 260");
  });

  it("tracks dirty state and supports undo/redo from keyboard shortcuts", async () => {
    mockWorkflowAssets([workflowFixture()])
      .mockResolvedValueOnce({
        data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
      })
      .mockResolvedValueOnce({
        data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
      });
    vi.mocked(apiClient.put).mockResolvedValueOnce({
      data: {
        draft: {
          ...draftFixture(),
          graph: {
            ...draftFixture().graph,
            nodes: draftFixture().graph.nodes.map((node) =>
              node.id === "start" ? { ...node, label: "Order intake" } : node,
            ),
          },
        },
        latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
      },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.get(".workflow-editor-shell").attributes("data-editor-dirty-state")).toBe("saved");

    await wrapper.find('[data-node-id="start"]').trigger("click");
    await wrapper.get('input[name="node-label"]').setValue("Order intake");

    expect(wrapper.get(".workflow-editor-shell").attributes("data-editor-dirty-state")).toBe("dirty");
    expect(wrapper.get('[data-node-id="start"]').text()).toContain("Order intake");

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "z", ctrlKey: true }));
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.get('[data-node-id="start"]').text()).toContain("Start");
    expect(wrapper.get(".workflow-editor-shell").attributes("data-editor-dirty-state")).toBe("saved");

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Z", ctrlKey: true, shiftKey: true }));
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.get('[data-node-id="start"]').text()).toContain("Order intake");
    expect(wrapper.get(".workflow-editor-shell").attributes("data-editor-dirty-state")).toBe("dirty");

    await wrapper.get('button[data-action="save-editor-draft"]').trigger("click");
    await flushPromises();

    expect(wrapper.get(".workflow-editor-shell").attributes("data-editor-dirty-state")).toBe("saved");
  });

  it("duplicates the selected node, deletes the selected edge with Delete, and auto-layouts the graph", async () => {
    mockWorkflowAssets([workflowFixture()])
      .mockResolvedValueOnce({
        data: {
          draft: {
            ...draftFixture(),
            graph: {
              ...draftFixture().graph,
              nodes: draftFixture().graph.nodes.map((node) =>
                node.id === "start"
                  ? { ...node, position: { x: 420, y: 260 } }
                  : node.id === "end"
                    ? { ...node, position: { x: 100, y: 120 } }
                    : node,
              ),
            },
          },
          latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
        },
      })
      .mockResolvedValueOnce({
        data: {
          draft: {
            ...draftFixture(),
            graph: {
              ...draftFixture().graph,
              nodes: draftFixture().graph.nodes.map((node) =>
                node.id === "start"
                  ? { ...node, position: { x: 420, y: 260 } }
                  : node.id === "end"
                    ? { ...node, position: { x: 100, y: 120 } }
                    : node,
              ),
            },
          },
          latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
        },
      });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.find('[data-node-id="start"]').trigger("click");
    await wrapper.get('button[data-action="duplicate-selected-node"]').trigger("click");

    expect(wrapper.findAll("[data-node-id]").length).toBe(3);
    expect(wrapper.text()).toContain("Start Copy");

    wrapper.getComponent(WorkflowGraphCanvas).vm.$emit("select-edge", "edge-start-end");
    await flushPromises();
    await wrapper.vm.$nextTick();

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Delete" }));
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find('[data-edge-id="edge-start-end"]').exists()).toBe(false);

    await wrapper.find('[data-node-id="end"]').trigger("click");
    expect(wrapper.get(".workflow-inspector-meta").text()).toContain("X 100 · Y 120");

    await wrapper.get('button[data-action="auto-layout-editor-graph"]').trigger("click");

    // Auto-layout repositions nodes onto the process spine; the original freehand coordinates must change.
    expect(wrapper.get(".workflow-inspector-meta").text()).not.toContain("X 100 · Y 120");
  });

  it("does not load the removed Workflow approval compatibility resource when the editor opens", async () => {
    mockWorkflowAssets([workflowSummaryFixture()]).mockResolvedValueOnce({
      data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(vi.mocked(apiClient.get).mock.calls.flat().join(" ")).not.toContain("/approvals");
    expect(wrapper.find(".workflow-approval-panel").exists()).toBe(false);
  });

  it("deletes the selected node together with attached edges from the editor", async () => {
    mockWorkflowAssets([workflowFixture()]).mockResolvedValueOnce({
      data: {
        draft: {
          ...draftFixture(),
          graph: {
            ...draftFixture().graph,
            nodes: [
              ...draftFixture().graph.nodes,
              {
                id: "tool-1",
                type: "Tool",
                label: "Query Order",
                position: { x: 260, y: 180 },
                ports: [
                  { key: "input", label: "Input", direction: "input" },
                  { key: "result", label: "Result", direction: "output" },
                ],
                data: { toolId: "order.status.query" },
                ui: {},
              },
            ],
            edges: [
              {
                id: "edge-start-tool",
                sourceNodeId: "start",
                sourcePort: "output",
                targetNodeId: "tool-1",
                targetPort: "input",
                data: {},
                ui: {},
              },
              {
                id: "edge-tool-end",
                sourceNodeId: "tool-1",
                sourcePort: "result",
                targetNodeId: "end",
                targetPort: "input",
                data: {},
                ui: {},
              },
            ],
          },
        },
        latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
      },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find('button[data-action="delete-selected-node"]').exists()).toBe(false);
    expect(wrapper.find('button[data-action="delete-selected-edge"]').exists()).toBe(false);

    await wrapper.find('[data-node-id="tool-1"]').trigger("contextmenu", { clientX: 260, clientY: 180 });
    expect(wrapper.get('button[data-action="delete-context-target"]').text()).toContain("删除节点");

    await wrapper.get('button[data-action="delete-context-target"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find('[data-node-id="tool-1"]').exists()).toBe(false);
    expect(wrapper.text()).toContain("已删除节点");
    expect(wrapper.text()).toContain("2 条关联连线");
  });

  it("prevents terminal start and end nodes from being deleted through the context menu", async () => {
    mockWorkflowAssets([workflowFixture()]).mockResolvedValueOnce({
      data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.find('[data-node-id="start"]').trigger("contextmenu", { clientX: 120, clientY: 180 });
    const startDelete = wrapper.get('button[data-action="delete-context-target"]');

    expect(startDelete.text()).toContain("起止节点不可删除");
    expect(startDelete.attributes("disabled")).toBeDefined();
    await startDelete.trigger("click");
    await flushPromises();

    expect(wrapper.find('[data-node-id="start"]').exists()).toBe(true);
    expect(wrapper.getComponent(WorkflowGraphCanvas).props("graph").edges).toHaveLength(1);

    await wrapper.find('[data-node-id="end"]').trigger("contextmenu", { clientX: 420, clientY: 180 });
    const endDelete = wrapper.get('button[data-action="delete-context-target"]');

    expect(endDelete.text()).toContain("起止节点不可删除");
    expect(endDelete.attributes("disabled")).toBeDefined();
    expect(wrapper.find('[data-node-id="end"]').exists()).toBe(true);
  });

  it("prevents browser navigation when Backspace targets protected terminal nodes", async () => {
    mockWorkflowAssets([workflowFixture()]).mockResolvedValueOnce({
      data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.find('[data-node-id="start"]').trigger("click");
    const event = new KeyboardEvent("keydown", { key: "Backspace", cancelable: true });
    window.dispatchEvent(event);
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(event.defaultPrevented).toBe(true);
    expect(wrapper.find('[data-node-id="start"]').exists()).toBe(true);
    expect(wrapper.text()).toContain("流程起止节点，不可删除");
  });

  it("deletes a duplicate Start node while preserving the canonical Start node", async () => {
    mockWorkflowAssets([workflowFixture()]).mockResolvedValueOnce({
      data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: { loading: () => undefined },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();
    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.find('[data-node-id="start"]').trigger("click");
    await wrapper.get('button[data-action="duplicate-selected-node"]').trigger("click");

    expect(wrapper.find('[data-node-id="start-2"]').exists()).toBe(true);

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Delete", cancelable: true }));
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find('[data-node-id="start"]').exists()).toBe(true);
    expect(wrapper.find('[data-node-id="start-2"]').exists()).toBe(false);
    expect(wrapper.text()).toContain("已删除节点 Start Copy");
  });

  it("deletes the selected edge without affecting nodes", async () => {
    mockWorkflowAssets([workflowFixture()]).mockResolvedValueOnce({
      data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    wrapper.getComponent(WorkflowGraphCanvas).vm.$emit("open-edge-context-menu", {
      edgeId: "edge-start-end",
      position: { x: 280, y: 180 },
    });
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.get('button[data-action="delete-context-target"]').text()).toContain("删除连线");

    await wrapper.get('button[data-action="delete-context-target"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find('[data-node-id="start"]').exists()).toBe(true);
    expect(wrapper.find('[data-node-id="end"]').exists()).toBe(true);
    expect(wrapper.text()).toContain("已删除连线");
  });

  it("shows an edge inspector, saves branch labels, and can clear them back to omitted data", async () => {
    mockWorkflowAssets([workflowFixture()]).mockResolvedValueOnce({
      data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
    });
    vi.mocked(apiClient.put).mockResolvedValueOnce({
      data: {
        draft: {
          ...draftFixture(),
          graph: {
            ...draftFixture().graph,
            edges: [
              {
                ...draftFixture().graph.edges[0],
                data: { branch: "true" },
              },
            ],
          },
        },
        latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
      },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: AppSelectStub,
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    wrapper.getComponent(WorkflowGraphCanvas).vm.$emit("select-edge", "edge-start-end");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".workflow-inspector").exists()).toBe(false);
    expect(wrapper.get(".workflow-edge-inspector").text()).toContain("分支标签");
    expect(wrapper.get(".workflow-edge-inspector").text()).toContain("无分支标签");
    expect(wrapper.get(".workflow-edge-inspector").text()).toContain("默认分支");
    expect(wrapper.get(".workflow-edge-inspector").text()).toContain("条件成立");

    await wrapper.get('.workflow-branch-select [data-value="true"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.get(".workflow-flow-edge-label").text()).toBe("条件成立");

    await wrapper.get('.workflow-branch-select [data-value=""]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".workflow-flow-edge-label").exists()).toBe(false);

    await wrapper.get('button[data-action="save-editor-draft"]').trigger("click");
    await flushPromises();

    expect(vi.mocked(apiClient.put)).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order-cancel-draft/draft",
      expect.objectContaining({
        graph: expect.objectContaining({
          edges: [
            expect.objectContaining({
              id: "edge-start-end",
              data: {},
            }),
          ],
        }),
      }),
      expect.objectContaining({ headers: expect.objectContaining({ "If-Match": expect.any(String) }) }),
    );
  });

  it("focuses branch compilation issues with both node and edge ids on the exact edge", async () => {
    const branchIssue = {
      code: "condition-branch-default-required",
      message: "Condition 节点存在多条出边时必须配置一条默认分支",
      severity: "error",
      sourceStage: "graph" as const,
      nodeId: "start",
      edgeId: "edge-start-end",
      fieldPath: "edges.data.branch",
    };
    mockWorkflowAssets([workflowFixture()]).mockResolvedValueOnce({
      data: {
        draft: draftFixture(),
        latestCompilation: compilationFixture({
          status: "Invalid",
          issues: [branchIssue],
        }),
      },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: AppSelectStub,
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".workflow-edge-inspector").exists()).toBe(false);

    await wrapper.get(".workflow-issue-item").trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".workflow-inspector").exists()).toBe(false);
    expect(wrapper.get(".workflow-edge-inspector").text()).toContain("edge-start-end");
    expect(wrapper.get(".vue-flow__edge.selected").exists()).toBe(true);
    expect(wrapper.get(".workflow-issue-item").classes()).toContain("active");
    expect(wrapper.find('[data-node-id="start"]').classes()).not.toContain("selected");
  });

  it("shows type-specific inspector fields for workflow nodes", async () => {
    mockWorkflowAssets([workflowFixture()]).mockResolvedValueOnce({
      data: {
        draft: {
          ...draftFixture(),
          graph: {
            ...draftFixture().graph,
            nodes: [
              {
                id: "condition-1",
                type: "Condition",
                label: "Condition 1",
                position: { x: 240, y: 120 },
                ports: [
                  { key: "input", label: "Input", direction: "input" },
                  { key: "output", label: "Output", direction: "output" },
                ],
                data: { expression: "nodeOutputs.tool.status == 'paid'" },
                ui: {},
              },
              {
                id: "transform-1",
                type: "Transform",
                label: "Transform 1",
                position: { x: 360, y: 220 },
                ports: [
                  { key: "input", label: "Input", direction: "input" },
                  { key: "output", label: "Output", direction: "output" },
                ],
                data: { template: "订单 {{input.orderId}}" },
                ui: {},
              },
              ...draftFixture().graph.nodes,
            ],
            edges: draftFixture().graph.edges,
          },
        },
        latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
      },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.find('[data-node-id="condition-1"]').trigger("click");
    expect(wrapper.text()).toContain("条件表达式");
    expect(wrapper.find('textarea[name="node-condition-expression"]').exists()).toBe(true);
    expect(wrapper.find('input[name="node-tool-id"]').exists()).toBe(false);

    await wrapper.find('[data-node-id="transform-1"]').trigger("click");
    expect(wrapper.text()).toContain("转换模板");
    expect(wrapper.find('textarea[name="node-transform-template"]').exists()).toBe(true);
    expect(wrapper.find('textarea[name="node-condition-expression"]').exists()).toBe(false);
  });

  it("renders workspace names instead of raw workspace ids in the workflow list", async () => {
    mockWorkflowAssets([
      {
        ...workflowSummaryFixture("wf-space-name", "退款编排"),
        workspaceId: "7391452109583233024",
        workspaceName: "订单中心",
      },
    ]);

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: AppSelectStub,
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    const row = wrapper.get(".data-table tbody tr");
    expect(row.text()).toContain("订单中心");
    expect(row.text()).not.toContain("7391452109583233024");
  });

  it("loads workspace-scoped published tool options without Agent ownership", async () => {
    mockWorkflowAssets([workflowSummaryFixture()])
      .mockResolvedValueOnce({
        data: {
          draft: {
            ...draftFixture(),
            graph: {
              ...draftFixture().graph,
              nodes: [
                {
                  id: "tool-1",
                  type: "Tool",
                  label: "Query Order",
                  position: { x: 260, y: 180 },
                  ports: [
                    { key: "input", label: "Input", direction: "input" },
                    { key: "result", label: "Result", direction: "output" },
                  ],
                  data: { toolId: "order.status.query" },
                  ui: {},
                },
                ...draftFixture().graph.nodes,
              ],
              edges: draftFixture().graph.edges,
            },
          },
          latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
        },
      })
      .mockResolvedValueOnce({ data: { approvals: [] } })
      .mockResolvedValueOnce({
        data: {
          items: [
            {
              ...toolDTO("order.status.query", "查询订单状态"),
              headVersion: toolVersionDTO("order.status.query"),
            },
            {
              // DRAFT head is not published → excluded from inspector options
              ...toolDTO("order.cancel.submit", "提交取消申请"),
              headVersion: toolVersionDTO("order.cancel.submit", { lifecycleStatus: "DRAFT" }),
            },
            {
              ...toolDTO("order.shared", "共享工具"),
              headVersion: toolVersionDTO("order.shared"),
            },
            {
              ...toolDTO("order.disabled", "已停用工具", "DISABLED"),
              headVersion: toolVersionDTO("order.disabled"),
            },
          ],
          pagination: { page: 1, pageSize: 50, total: 4 },
        },
      });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: AppSelectStub,
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.find('[data-node-id="tool-1"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(vi.mocked(apiClient.get)).toHaveBeenCalledWith("/workspaces/order/tools?page=1&pageSize=50");
    expect(wrapper.find('input[name="node-tool-id"]').exists()).toBe(false);
    const toolSelect = wrapper.get(".workflow-tool-select");
    expect(toolSelect.attributes("data-model-value")).toBe("order.status.query");
    expect(toolSelect.find('[data-value="order.status.query"]').text()).toContain("查询订单状态");
    expect(toolSelect.find('[data-value="order.cancel.submit"]').exists()).toBe(false);
    expect(toolSelect.find('[data-value="order.shared"]').text()).toContain("共享工具");
    expect(toolSelect.find('[data-value="order.disabled"]').exists()).toBe(false);

    expect(wrapper.get(".workflow-flow-node.selected").text()).toContain("Query Order");
  });

  it("saves tool input mapping and output schema preview from the tool editor", async () => {
    mockWorkflowAssets([{ ...workflowSummaryFixture(), agentId: "agent-1" }])
      .mockResolvedValueOnce({
        data: {
          draft: {
            ...draftFixture(),
            graph: {
              ...draftFixture().graph,
              nodes: [
                {
                  id: "lookup-1",
                  type: "Tool",
                  label: "Lookup Order",
                  position: { x: 180, y: 180 },
                  ports: [
                    { key: "input", label: "Input", direction: "input" },
                    { key: "result", label: "Result", direction: "output" },
                  ],
                  data: {
                    outputSchema: {
                      properties: {
                        status: { type: "string" },
                      },
                    },
                  },
                  ui: {},
                },
                {
                  id: "tool-1",
                  type: "Tool",
                  label: "Cancel Order",
                  position: { x: 260, y: 180 },
                  ports: [
                    { key: "input", label: "Input", direction: "input" },
                    { key: "result", label: "Result", direction: "output" },
                  ],
                  data: {
                    toolId: "order.cancel.submit",
                    outputSchema: {
                      properties: {
                        cancelId: { type: "string" },
                      },
                    },
                  },
                  ui: {},
                },
                {
                  id: "notify-1",
                  type: "Tool",
                  label: "Notify User",
                  position: { x: 520, y: 180 },
                  ports: [
                    { key: "input", label: "Input", direction: "input" },
                    { key: "result", label: "Result", direction: "output" },
                  ],
                  data: {
                    outputSchema: {
                      properties: {
                        notified: { type: "boolean" },
                      },
                    },
                  },
                  ui: {},
                },
                ...draftFixture().graph.nodes,
              ],
              edges: [
                {
                  id: "edge-lookup-tool",
                  sourceNodeId: "lookup-1",
                  sourcePort: "result",
                  targetNodeId: "tool-1",
                  targetPort: "input",
                  data: {},
                  ui: {},
                },
                {
                  id: "edge-tool-notify",
                  sourceNodeId: "tool-1",
                  sourcePort: "result",
                  targetNodeId: "notify-1",
                  targetPort: "input",
                  data: {},
                  ui: {},
                },
                ...draftFixture().graph.edges,
              ],
            },
          },
          latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
        },
      })
      .mockResolvedValueOnce({ data: { approvals: [] } })
      .mockResolvedValueOnce({
        data: {
          items: [
            {
              ...toolDTO("order.cancel.submit", "提交取消申请"),
              headVersion: toolVersionDTO("order.cancel.submit", {
                inputSchema: {
                  type: "object",
                  required: ["orderId", "reason"],
                  properties: {
                    orderId: { type: "string", description: "订单 ID", location: "Body" },
                    reason: { type: "string", description: "取消原因", location: "Body" },
                    pageSize: {
                      type: "integer",
                      description: "分页大小",
                      location: "Query",
                      valueSource: "SystemDefault",
                      default: 20,
                    },
                  },
                },
                outputSchema: {
                  type: "object",
                  properties: {
                    cancelId: { type: "string", description: "取消单 ID" },
                    status: { type: "string", description: "处理状态" },
                  },
                },
              }),
            },
          ],
          pagination: { page: 1, pageSize: 50, total: 1 },
        },
      });
    vi.mocked(apiClient.put).mockResolvedValueOnce({
      data: {
        draft: draftFixture(),
        latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
      },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: AppSelectStub,
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.find('[data-node-id="tool-1"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.get(".workflow-tool-required-params").text()).toContain("orderId");
    expect(wrapper.get(".workflow-tool-required-params").text()).toContain("reason");
    expect(wrapper.get(".workflow-tool-required-params").text()).not.toContain("pageSize");
    expect(wrapper.get(".workflow-tool-output-preview").text()).toContain("cancelId");
    expect(wrapper.get(".workflow-tool-output-preview").text()).toContain("status");
    expect(wrapper.get(".workflow-tool-variable-select").text()).toContain("input.orderId");
    expect(wrapper.get(".workflow-tool-variable-select").text()).toContain("nodeOutputs.lookup-1.status");
    expect(wrapper.get(".workflow-tool-variable-select").text()).not.toContain("nodeOutputs.tool-1.cancelId");
    expect(wrapper.get(".workflow-tool-variable-select").text()).not.toContain("nodeOutputs.notify-1.notified");

    await wrapper
      .get('[data-param-name="orderId"] .workflow-tool-variable-select [data-value="input.orderId"]')
      .trigger("click");
    await wrapper.get('[data-param-name="reason"] [data-action="mapping-kind-literal"]').trigger("click");
    await wrapper.get('input[name="tool-param-reason-literal"]').setValue("customer_requested");
    await wrapper.get('[data-param-name="reason"] [data-action="mapping-kind-ref"]').trigger("click");
    await wrapper.get('[data-param-name="reason"] [data-action="mapping-kind-literal"]').trigger("click");
    await wrapper.get('input[name="tool-param-reason-literal"]').setValue("customer_requested");
    await wrapper.get('button[data-action="save-editor-draft"]').trigger("click");
    await flushPromises();

    expect(vi.mocked(apiClient.put)).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order-cancel-draft/draft",
      expect.objectContaining({
        graph: expect.objectContaining({
          nodes: expect.arrayContaining([
            expect.objectContaining({
              id: "tool-1",
              data: expect.objectContaining({
                toolId: "order.cancel.submit",
                inputMapping: {
                  orderId: { kind: "ref", path: "input.orderId" },
                  reason: { kind: "literal", value: "customer_requested" },
                },
                outputSchema: {
                  type: "object",
                  properties: {
                    cancelId: { type: "string", description: "取消单 ID" },
                    status: { type: "string", description: "处理状态" },
                  },
                },
              }),
            }),
          ]),
        }),
      }),
      expect.objectContaining({ headers: expect.objectContaining({ "If-Match": expect.any(String) }) }),
    );
  });

  it("saves viewport changes together with the current draft graph", async () => {
    mockWorkflowAssets([workflowFixture()])
      .mockResolvedValueOnce({ data: { draft: draftFixture(), latestCompilation: compilationFixture() } })
      .mockResolvedValueOnce({ data: { draft: draftFixture(), latestCompilation: compilationFixture() } });
    vi.mocked(apiClient.put).mockResolvedValueOnce({
      data: {
        draft: {
          ...draftFixture(),
          graph: {
            ...draftFixture().graph,
            nodes: draftFixture().graph.nodes.map((node) =>
              node.id === "end" ? { ...node, position: { x: 520, y: 260 } } : node,
            ),
            viewport: { x: 24, y: -12, zoom: 1.3 },
          },
        },
        latestCompilation: compilationFixture(),
      },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    wrapper.getComponent(WorkflowGraphCanvas).vm.$emit("update-node-position", {
      nodeId: "end",
      position: { x: 520, y: 260 },
    });
    wrapper.getComponent(WorkflowGraphCanvas).vm.$emit("update-viewport", {
      x: 24,
      y: -12,
      zoom: 1.3,
    });
    await flushPromises();

    await wrapper.get('button[data-action="save-editor-draft"]').trigger("click");
    await flushPromises();

    expect(vi.mocked(apiClient.put)).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order-cancel-draft/draft",
      expect.objectContaining({
        graph: expect.objectContaining({
          viewport: { x: 24, y: -12, zoom: 1.3 },
          nodes: expect.arrayContaining([expect.objectContaining({ id: "end", position: { x: 520, y: 260 } })]),
        }),
      }),
      expect.objectContaining({ headers: expect.objectContaining({ "If-Match": expect.any(String) }) }),
    );
  });

  it("validates the current in-memory draft before requesting workflow validation", async () => {
    mockWorkflowAssets([workflowFixture()])
      .mockResolvedValueOnce({
        data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
      })
      .mockResolvedValueOnce({
        data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
      });
    vi.mocked(apiClient.put).mockResolvedValueOnce({
      data: {
        draft: {
          ...draftFixture(),
          graph: {
            ...draftFixture().graph,
            nodes: draftFixture().graph.nodes.map((node) =>
              node.id === "end" ? { ...node, position: { x: 520, y: 260 } } : node,
            ),
          },
        },
        latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
      },
    });
    vi.mocked(apiClient.post).mockResolvedValueOnce({
      data: compilationFixture({ status: "VALID", issues: [] }),
    });
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: readinessFixture() });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    wrapper.getComponent(WorkflowGraphCanvas).vm.$emit("update-node-position", {
      nodeId: "end",
      position: { x: 520, y: 260 },
    });
    await flushPromises();

    await wrapper.get('button[data-action="validate-editor-workflow"]').trigger("click");
    await flushPromises();

    expect(vi.mocked(apiClient.put)).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order-cancel-draft/draft",
      expect.objectContaining({
        graph: expect.objectContaining({
          nodes: expect.arrayContaining([expect.objectContaining({ id: "end", position: { x: 520, y: 260 } })]),
        }),
      }),
      expect.objectContaining({ headers: expect.objectContaining({ "If-Match": expect.any(String) }) }),
    );
    expect(vi.mocked(apiClient.post)).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order-cancel-draft/draft:compile",
    );
    expect(vi.mocked(apiClient.put).mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(apiClient.post).mock.invocationCallOrder[0],
    );
  });

  it("keeps compilation failures visible instead of calling legacy validation", async () => {
    mockWorkflowAssets([
      { ...workflowFixture(), readiness: readinessFixture({ stage: "PublishReady", canPublish: true }) },
    ])
      .mockResolvedValueOnce({
        data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
      })
      .mockResolvedValueOnce({ data: { approvals: [] } });
    vi.mocked(apiClient.put).mockResolvedValueOnce({
      data: {
        draft: draftFixture(),
        latestCompilation: compilationFixture(),
      },
    });
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: compilationFixture({ status: "INVALID" }) });
    vi.mocked(apiClient.get).mockResolvedValueOnce({
      data: readinessFixture({ stage: "CompileFailed", canTrial: false, compilationValid: false }),
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[data-action="validate-editor-workflow"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(vi.mocked(apiClient.put)).toHaveBeenCalled();
    expect(vi.mocked(apiClient.post)).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order-cancel-draft/draft:compile",
    );
    expect(wrapper.get(".action-toast").text()).toContain("编译问题");
  });

  it("opens a schema-driven trial-run dialog from the saved start node schema", async () => {
    mockWorkflowAssets([
      { ...workflowFixture(), readiness: readinessFixture({ stage: "PublishReady", canPublish: true }) },
    ])
      .mockResolvedValueOnce({
        data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
      })
      .mockResolvedValueOnce({ data: { approvals: [] } });
    vi.mocked(apiClient.put).mockResolvedValueOnce({
      data: {
        draft: draftFixture(),
        latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
      },
    });
    vi.mocked(apiClient.post)
      .mockResolvedValueOnce({ data: compilationFixture({ status: "VALID", issues: [] }) })
      .mockResolvedValueOnce({
        data: {
          id: "trial-1",
          compilationId: "comp-1",
          executionId: "exec-trial",
          status: "SUCCEEDED",
          inputHash: "sha256:input",
          startedBy: "user-1",
          startedAt: "2026-07-02T00:00:00Z",
          finishedAt: "2026-07-02T00:00:01Z",
        },
      });
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: readinessFixture({ stage: "TrialRequired", canTrial: true }) })
      .mockResolvedValueOnce({
        data: readinessFixture({ stage: "PublishReady", canPublish: true, trialCurrent: true, trialSuccessful: true }),
      });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[data-action="open-trial-run-dialog"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(vi.mocked(apiClient.put)).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order-cancel-draft/draft",
      expect.objectContaining({
        graph: expect.objectContaining({
          nodes: expect.arrayContaining([expect.objectContaining({ id: "start" })]),
        }),
      }),
      expect.objectContaining({ headers: expect.objectContaining({ "If-Match": expect.any(String) }) }),
    );
    expect(wrapper.findComponent(WorkflowTrialRunDialog).exists()).toBe(true);
    expect(wrapper.text()).toContain("订单 ID");

    await wrapper.get('input[name="orderId"]').setValue("A10293");
    await wrapper.get('input[name="dryRun"]').setValue(true);
    await wrapper.get('button[data-action="submit-trial-run"]').trigger("click");
    await flushPromises();

    expect(vi.mocked(apiClient.post)).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order-cancel-draft/compilations/comp-1:trial",
      {
        input: { orderId: "A10293", dryRun: true },
      },
    );
    expect(vi.mocked(apiClient.get)).not.toHaveBeenCalledWith("/executions/exec-trial");
    expect(wrapper.text()).toContain("试运行已生成 exec-trial");
    expect(wrapper.findComponent(WorkflowTrialRunDialog).exists()).toBe(false);
    expect(wrapper.findComponent(WorkflowExecutionTracePanel).exists()).toBe(true);
  });

  it("supports compact start-node input schema maps in the trial-run dialog", async () => {
    mockWorkflowAssets([workflowFixture()]).mockResolvedValueOnce({
      data: {
        draft: compactSchemaDraftFixture(),
        latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
      },
    });
    vi.mocked(apiClient.put).mockResolvedValueOnce({
      data: {
        draft: compactSchemaDraftFixture(),
        latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
      },
    });
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: compilationFixture({ status: "VALID", issues: [] }) });
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: readinessFixture({ canTrial: true }) });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[data-action="open-trial-run-dialog"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.findComponent(WorkflowTrialRunDialog).exists()).toBe(true);
    expect(wrapper.get('input[name="orderId"]').exists()).toBe(true);
    expect(wrapper.get('input[name="dryRun"]').attributes("type")).toBe("checkbox");
  });

  it("does not fall back to legacy trial-run inputs when draft loading fails for reasons other than not found", async () => {
    const draftLoadError = new AxiosError("draft load failed", "ERR_BAD_RESPONSE", undefined, undefined, {
      status: 500,
      statusText: "Internal Server Error",
      headers: {},
      config: { headers: {} } as never,
      data: { error: "draft load failed" },
    });

    mockWorkflowAssets([workflowFixture()]).mockRejectedValueOnce(draftLoadError);

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[aria-label="更多编排操作"]').trigger("click");
    await wrapper.vm.$nextTick();
    document.body.querySelector<HTMLButtonElement>('button[data-action-key="trial-run"]')?.click();
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.findComponent(WorkflowTrialRunDialog).exists()).toBe(false);
    expect(wrapper.text()).toContain("草稿加载失败");
  });

  it("does not fabricate trial-run inputs when the draft endpoint returns not found", async () => {
    const draftNotFoundError = new AxiosError("draft missing", "ERR_BAD_REQUEST", undefined, undefined, {
      status: 404,
      statusText: "Not Found",
      headers: {},
      config: { headers: {} } as never,
      data: { error: "workflow draft not found" },
    });
    mockWorkflowAssets([workflowFixture()]).mockRejectedValueOnce(draftNotFoundError);

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[aria-label="更多编排操作"]').trigger("click");
    await wrapper.vm.$nextTick();
    document.body.querySelector<HTMLButtonElement>('button[data-action-key="trial-run"]')?.click();
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.findComponent(WorkflowTrialRunDialog).exists()).toBe(false);
    expect(wrapper.text()).toContain("草稿加载失败");
    expect(wrapper.text()).not.toContain("Legacy input");
    expect(vi.mocked(apiClient.get)).not.toHaveBeenCalledWith("/workflows/wf-order-cancel-draft");
  });

  it("surfaces latest compilation when trial run returns an invalid draft error", async () => {
    const invalidTrialRunError = new AxiosError("trial run blocked", "ERR_BAD_REQUEST", undefined, undefined, {
      status: 400,
      statusText: "Bad Request",
      headers: {},
      config: { headers: {} } as never,
      data: {
        error: "workflow draft must compile successfully before trial run",
        workflow: workflowFixture(),
        validation: { valid: false, issues: [] },
        latestCompilation: compilationFixture(),
      },
    });

    mockWorkflowAssets([workflowFixture()]).mockResolvedValueOnce({
      data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
    });
    vi.mocked(apiClient.put).mockResolvedValueOnce({
      data: {
        draft: draftFixture(),
        latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
      },
    });
    vi.mocked(apiClient.post)
      .mockResolvedValueOnce({ data: compilationFixture({ status: "VALID", issues: [] }) })
      .mockRejectedValueOnce(invalidTrialRunError);
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: readinessFixture({ canTrial: true }) });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[data-action="open-trial-run-dialog"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('input[name="orderId"]').setValue("A10293");
    await wrapper.get('button[data-action="submit-trial-run"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.findComponent(WorkflowTrialRunDialog).exists()).toBe(false);
    expect(wrapper.get('[data-node-id="end"]').classes()).toContain("selected");
    expect(wrapper.get(".action-toast").text()).toContain("不可用于试运行");
  });

  it("treats stale compilation trial-run errors as requiring a fresh save and compile", async () => {
    const staleTrialRunError = new AxiosError("trial run blocked", "ERR_BAD_REQUEST", undefined, undefined, {
      status: 400,
      statusText: "Bad Request",
      headers: {},
      config: { headers: {} } as never,
      data: {
        error: "workflow draft compilation is stale",
        workflow: workflowFixture(),
        validation: { valid: false, issues: [] },
        latestCompilation: compilationFixture({
          status: "Valid",
          issues: [],
          draftVersion: "draft-v1",
          compiledAt: "2026-06-27T08:59:00Z",
        }),
      },
    });

    mockWorkflowAssets([workflowFixture()]).mockResolvedValueOnce({
      data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
    });
    vi.mocked(apiClient.put).mockResolvedValueOnce({
      data: {
        draft: draftFixture(),
        latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
      },
    });
    vi.mocked(apiClient.post)
      .mockResolvedValueOnce({ data: compilationFixture({ status: "VALID", issues: [] }) })
      .mockRejectedValueOnce(staleTrialRunError);
    vi.mocked(apiClient.get).mockResolvedValueOnce({ data: readinessFixture({ canTrial: true }) });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[data-action="open-trial-run-dialog"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('input[name="orderId"]').setValue("A10293");
    await wrapper.get('button[data-action="submit-trial-run"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.findComponent(WorkflowTrialRunDialog).exists()).toBe(false);
    expect(wrapper.get(".action-toast").text()).toContain("编译结果已过期");
  });

  it("keeps a dirty editor on the first compilation issue instead of publishing an invalid draft", async () => {
    mockWorkflowAssets([
      { ...workflowFixture(), readiness: readinessFixture({ stage: "PublishReady", canPublish: true }) },
    ]).mockResolvedValueOnce({
      data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
    });
    vi.mocked(apiClient.put).mockResolvedValueOnce({
      data: {
        draft: draftFixture(),
        latestCompilation: compilationFixture(),
      },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('input[name="node-label"]').setValue("Start with local edits");
    await wrapper.vm.$nextTick();

    await wrapper.get('button[data-action="publish-editor-workflow"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(vi.mocked(apiClient.put)).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order-cancel-draft/draft",
      expect.objectContaining({
        graph: expect.objectContaining({
          nodes: expect.arrayContaining([expect.objectContaining({ id: "end" })]),
        }),
      }),
      expect.objectContaining({ headers: expect.objectContaining({ "If-Match": expect.any(String) }) }),
    );
    expect(vi.mocked(apiClient.post)).not.toHaveBeenCalled();
    expect(wrapper.get(".action-toast").text()).toContain("试运行");
  });

  it("publishes an unchanged publish-ready editor draft without re-saving it", async () => {
    mockWorkflowAssets([
      { ...workflowFixture(), readiness: readinessFixture({ stage: "PublishReady", canPublish: true }) },
    ]).mockResolvedValueOnce({
      data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
    });
    vi.mocked(apiClient.put).mockResolvedValueOnce({
      data: {
        draft: draftFixture(),
        latestCompilation: compilationFixture({ status: "Valid", issues: [] }),
        workflow: {
          ...workflowFixture(),
          readiness: readinessFixture({ stage: "TrialRequired", canPublish: false }),
        },
      },
    });
    vi.mocked(apiClient.post).mockResolvedValueOnce({
      data: {
        revision: {
          id: "rev-1",
          revisionNo: 1,
          sourceCompilationId: "comp-1",
          draftSnapshot: draftFixture().graph,
          specSnapshot: { workflowId: "wf-order-cancel-draft", nodes: [] },
          planSnapshot: { workflowId: "wf-order-cancel-draft", nodes: [] },
          planHash: "sha256:plan-1",
          status: "PUBLISHED",
          publishNote: "verified",
          createdBy: "user-1",
          createdAt: "2026-07-02T00:00:00Z",
        },
        releaseId: "release-1",
        releaseNo: 1,
        trialId: "trial-1",
      },
    });
    vi.mocked(apiClient.get)
      .mockResolvedValueOnce({ data: { ...workflowFixture(), status: "ACTIVE", activeRevisionId: "rev-1" } })
      .mockResolvedValueOnce({
        data: readinessFixture({ stage: "Published", canPublish: false, published: true, activeRevisionId: "rev-1" }),
      });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.get('button[data-action="publish-editor-workflow"]').trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(vi.mocked(apiClient.put)).not.toHaveBeenCalled();
    expect(vi.mocked(apiClient.post)).toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order-cancel-draft/compilations/comp-1:publish",
      expect.objectContaining({ callableName: "wf_order_cancel_draft" }),
    );
    expect(wrapper.get(".action-toast").text()).toContain("已发布");
    expect(wrapper.get(".action-toast").text()).not.toContain("rev-1");
  });

  it("creates a new workflow as graph-first metadata and loads the backend default draft", async () => {
    mockWorkflowAssets([workflowFixture()]).mockResolvedValueOnce({
      data: {
        draft: {
          ...draftFixture(),
          workflowId: "wf-created",
          draftVersion: "draft-bootstrap",
        },
        latestCompilation: compilationFixture({ workflowId: "wf-created", draftVersion: "draft-bootstrap" }),
      },
    });
    vi.mocked(apiClient.post).mockResolvedValueOnce({
      data: {
        workflow: {
          ...workflowFixture("wf-created", "新建编排"),
        },
        draft: { ...draftFixture(), workflowId: "wf-created" },
      },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    const workspaceStoreModule = await import("../../stores/workspaces");
    const workspaceStore = workspaceStoreModule.useWorkspaceStore();
    const agentStoreModule = await import("../../stores/agents");
    const agentStore = agentStoreModule.useAgentStore();
    workspaceStore.items = [workspaceFixture()];
    workspaceStore.activeWorkspaceId = "order";
    agentStore.items = [
      {
        id: "agent.order-fulfillment",
        workspaceId: "order",
        name: "订单履约 Agent",
        roleDescription: "处理订单履约",
        modelConfigId: "model-default",
        systemPrompt: "order",
        isDefault: true,
        status: "ACTIVE",
        statusSource: "seed",
        toolsCount: 2,
        workflowsCount: 1,
      },
    ];

    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.find("button.primary-button").trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    const saveButton = wrapper.findAll("button").find((button) => button.text() === "保存编排");
    expect(saveButton).toBeTruthy();
    await wrapper.get(".workflow-metadata-body input").setValue("新建图编排");
    await saveButton?.trigger("click");
    await flushPromises();

    expect(vi.mocked(apiClient.post)).toHaveBeenCalledWith(
      "/workspaces/order/workflows",
      expect.objectContaining({
        graph: expect.objectContaining({ nodes: expect.any(Array), edges: expect.any(Array) }),
        schemaVersion: "workflow.graph.v1",
      }),
    );
    const createBody = vi.mocked(apiClient.post).mock.calls[0]?.[1] as Record<string, unknown>;
    expect(createBody).not.toHaveProperty("agentId");
    expect(createBody).not.toHaveProperty("dsl");
    expect(createBody).not.toHaveProperty("canvasGraph");
    expect(vi.mocked(apiClient.get)).toHaveBeenCalledWith("/workspaces/order/workflows/wf-created/draft");
    expect(vi.mocked(apiClient.put)).not.toHaveBeenCalled();
  });

  it("renders workflow metadata form with workspace select options and styled description field", async () => {
    mockWorkflowAssets([]);

    const appSelectStub = defineComponent({
      name: "AppSelect",
      props: {
        modelValue: { type: [String, Number, Boolean], default: "" },
        options: { type: Array, default: () => [] },
      },
      template: `
        <div class="app-select-stub" :data-model-value="String(modelValue)">
          <span
            v-for="option in options"
            :key="String(option.value)"
            class="app-select-option"
            :data-label="option.label"
            :data-value="String(option.value)"
          />
        </div>
      `,
    });

    const wrapper = mount(WorkflowView, {
      global: {
        directives: {
          loading: () => undefined,
        },
        plugins: [createPinia(), ...testI18nPlugins()],
        stubs: {
          AppSelect: appSelectStub,
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    const workspaceStoreModule = await import("../../stores/workspaces");
    const workspaceStore = workspaceStoreModule.useWorkspaceStore();
    const agentStoreModule = await import("../../stores/agents");
    const agentStore = agentStoreModule.useAgentStore();
    workspaceStore.items = [
      workspaceFixture(),
      workspaceFixture({ id: "finance", name: "finance", displayName: "财务沙箱" }),
    ];
    workspaceStore.activeWorkspaceId = "order";
    agentStore.items = [
      {
        id: "agent.order-fulfillment",
        workspaceId: "order",
        name: "订单履约 Agent",
        roleDescription: "处理订单履约",
        modelConfigId: "model-default",
        systemPrompt: "order",
        isDefault: true,
        status: "ACTIVE",
        statusSource: "seed",
        toolsCount: 2,
        workflowsCount: 1,
      },
      {
        id: "agent.finance-review",
        workspaceId: "finance",
        name: "财务复核 Agent",
        roleDescription: "处理财务复核",
        modelConfigId: "model-default",
        systemPrompt: "finance",
        isDefault: true,
        status: "ACTIVE",
        statusSource: "seed",
        toolsCount: 1,
        workflowsCount: 1,
      },
    ];

    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper
      .findAll("button")
      .find((button) => button.text() === "新建编排")
      ?.trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    const selectStubs = wrapper.get(".workflow-metadata-modal-card").findAll(".app-select-stub");
    expect(selectStubs).toHaveLength(2);

    const workspaceField = wrapper.findAll(".drawer-field").find((field) => field.text().includes("业务空间"));
    expect(workspaceField).toBeTruthy();
    const workspaceSelect = workspaceField!.get(".app-select-stub");
    expect(workspaceSelect.attributes("data-model-value")).toBe("order");
    expect(workspaceSelect.findAll(".app-select-option")).toHaveLength(2);
    expect(workspaceSelect.find('[data-value="order"]').attributes("data-label")).toBe("order (订单中心)");
    expect(workspaceSelect.find('[data-value="finance"]').attributes("data-label")).toBe("finance (财务沙箱)");

    expect(wrapper.text()).not.toContain("归属 Agent");
    expect(wrapper.find('input[placeholder="留空时按名称生成"]').exists()).toBe(true);

    expect(wrapper.find('input[aria-label="业务空间"]').exists()).toBe(false);
    const description = wrapper.get("textarea.workflow-description-input");
    expect(description.attributes("rows")).toBe("4");
  });

  it("does not expose legacy Agent ownership in workflow list rows", async () => {
    mockWorkflowAssets([
      {
        ...workflowSummaryFixture(),
        agentId: "agent.order-fulfillment",
        agentName: "订单履约 Agent",
      },
    ]);

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.text()).not.toContain("归属 Agent");
    expect(wrapper.text()).not.toContain("订单履约 Agent");
  });

  it("renders backend readiness next action in workflow rows and the detail drawer", async () => {
    const readiness = readinessFixture();
    mockWorkflowAssets([
      {
        ...workflowSummaryFixture(),
        readiness,
      },
    ]).mockResolvedValueOnce({ data: { workflow: { ...workflowFixture(), readiness } } });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    const row = wrapper.get(".data-table tbody tr");
    expect(row.text()).toContain("开发中草稿");
    expect(row.find(".workflow-status-badge.draft").exists()).toBe(true);
    await row.get('button[aria-label="更多编排操作"]').trigger("click");
    const editMenuItem = document.body.querySelector<HTMLButtonElement>('button[data-action-key="edit"]');
    expect(editMenuItem).toBeTruthy();
    expect(editMenuItem?.getAttribute("aria-label")).toBe("编辑流程图");
    expect(editMenuItem?.textContent || "").toContain("编辑流程图");
    // Close the overflow menu before opening the detail drawer.
    document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flushPromises();

    await row.trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.get(".workflow-readiness-panel").text()).toContain("运行当前已编译草稿的试运行。");
  });

  it("disables editor publish until backend readiness allows publishing", async () => {
    mockWorkflowAssets([
      {
        ...workflowSummaryFixture(),
        readiness: readinessFixture({ canPublish: false }),
      },
    ]).mockResolvedValueOnce({
      data: { draft: draftFixture(), latestCompilation: compilationFixture({ status: "Valid", issues: [] }) },
    });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    const publishButton = wrapper.get('button[data-action="publish-editor-workflow"]');
    expect(publishButton.attributes("disabled")).toBeDefined();
    const topbar = wrapper.get(".workflow-editor-topbar");
    expect(topbar.text()).not.toContain("Run a trial against the latest compiled draft.");
    expect(topbar.find(".workflow-readiness-panel.compact").exists()).toBe(false);
    expect(topbar.get(".workflow-editor-readiness-strip").text()).toContain("草稿");
    expect(topbar.get(".workflow-editor-readiness-strip").text()).toContain("编译");
    expect(topbar.get(".workflow-editor-readiness-strip").text()).toContain("试运行");
    expect(topbar.get(".workflow-editor-readiness-strip").text()).toContain("发布");
    expect(publishButton.attributes("title")).toContain("需先完成试运行");

    await publishButton.trigger("click");
    await flushPromises();

    expect(vi.mocked(apiClient.put)).not.toHaveBeenCalled();
    expect(vi.mocked(apiClient.post)).not.toHaveBeenCalledWith(
      "/workspaces/order/workflows/wf-order-cancel-draft/compilations/comp-1:publish",
    );
  });

  it("keeps the editor closed when the draft endpoint returns 404", async () => {
    const draftNotFoundError = new AxiosError("draft missing", "ERR_BAD_REQUEST", undefined, undefined, {
      status: 404,
      statusText: "Not Found",
      headers: {},
      config: { headers: {} } as never,
      data: { error: "workflow draft not found" },
    });

    mockWorkflowAssets([workflowSummaryFixture()]).mockRejectedValueOnce(draftNotFoundError);

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(vi.mocked(apiClient.get)).toHaveBeenCalledWith("/workspaces/order/workflows/wf-order-cancel-draft/draft");
    expect(vi.mocked(apiClient.get)).not.toHaveBeenCalledWith("/workflows/wf-order-cancel-draft");
    expect(wrapper.find(".workflow-editor-overlay").exists()).toBe(false);
    expect(wrapper.find(".workflow-workbench").exists()).toBe(false);
    expect(wrapper.text()).toContain("草稿加载失败");
  });

  it("ignores stale draft responses when opening another workflow editor", async () => {
    const firstDraft = deferred<{ data: ReturnType<typeof draftFixture>; headers: { etag: string } }>();
    const secondDraft = deferred<{ data: ReturnType<typeof draftFixture>; headers: { etag: string } }>();

    // loadWorkflowDraft issues draft GET + readiness GET in parallel; queue both per open.
    mockWorkflowAssets([workflowFixture(), workflowFixture("wf-second", "第二个流程")])
      .mockImplementationOnce(() => firstDraft.promise)
      .mockResolvedValueOnce({ data: readinessFixture() })
      .mockImplementationOnce(() => secondDraft.promise)
      .mockResolvedValueOnce({ data: readinessFixture() });

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper, 0);
    await openWorkflowEditorFromMenu(wrapper, 1);

    secondDraft.resolve({
      data: draftFixtureFor("wf-second", "Second Start"),
      headers: { etag: '"draft-2-2"' },
    });
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".workflow-editor-overlay").exists()).toBe(true);
    expect(wrapper.text()).toContain("Second Start");

    firstDraft.resolve({
      data: draftFixtureFor("wf-order-cancel-draft", "First Start"),
      headers: { etag: '"draft-2-2"' },
    });
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.text()).toContain("Second Start");
    expect(wrapper.text()).not.toContain("First Start");
  });

  it("does not reopen the editor when a pending draft load is abandoned for create mode", async () => {
    const firstDraft = deferred<{
      data: { draft: ReturnType<typeof draftFixture>; latestCompilation: ReturnType<typeof compilationFixture> };
    }>();

    mockWorkflowAssets([workflowFixture(), workflowFixture("wf-second", "第二个流程")]).mockImplementationOnce(
      () => firstDraft.promise,
    );

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await openWorkflowEditorFromMenu(wrapper);
    await wrapper.find("button.primary-button").trigger("click");

    firstDraft.resolve({
      data: {
        draft: draftFixtureFor("wf-order-cancel-draft", "First Start"),
        latestCompilation: compilationFixture(),
      },
    });
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".workflow-editor-overlay").exists()).toBe(false);
    expect(wrapper.text()).toContain("新建编排");
    expect(wrapper.text()).not.toContain("First Start");
  });

  it("does not open the editor after the detail drawer is closed during a pending draft load", async () => {
    const pendingDraft = deferred<{
      data: { draft: ReturnType<typeof draftFixture>; latestCompilation: ReturnType<typeof compilationFixture> };
    }>();

    mockWorkflowAssets([workflowSummaryFixture()])
      .mockResolvedValueOnce({ data: { workflow: workflowFixture() } })
      .mockResolvedValueOnce({ data: { revisions: [] } })
      .mockImplementationOnce(() => pendingDraft.promise);

    const wrapper = mount(WorkflowView, {
      global: {
        plugins: testI18nPlugins(),
        directives: {
          loading: () => undefined,
        },
        stubs: {
          AppSelect: { template: "<div class='app-select-stub' />" },
          ElDrawer: {
            props: ["modelValue"],
            template: "<div v-if='modelValue'><slot /><slot name='footer' /></div>",
          },
        },
      },
    });

    await flushPromises();
    await wrapper.vm.$nextTick();

    await wrapper.find(".data-table tbody tr").trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();
    expect(wrapper.find(".workflow-detail-modal-card").exists()).toBe(true);

    // ZKL-56 atomic handoff: detail stays open while Draft+Readiness load; editor mounts only on success.
    await openWorkflowEditorFromMenu(wrapper);
    expect(wrapper.find(".workflow-detail-modal-card").exists()).toBe(true);
    expect(wrapper.find(".workflow-editor-overlay").exists()).toBe(false);

    // Abandon the pending draft load by entering create mode.
    await wrapper.find("button.primary-button").trigger("click");
    await flushPromises();
    await wrapper.vm.$nextTick();

    pendingDraft.resolve({
      data: {
        draft: draftFixtureFor("wf-order-cancel-draft", "First Start"),
        latestCompilation: compilationFixture(),
      },
    });
    await flushPromises();
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".workflow-editor-overlay").exists()).toBe(false);
    expect(wrapper.text()).not.toContain("First Start");
  });
});
