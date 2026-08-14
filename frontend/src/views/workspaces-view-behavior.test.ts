import { flushPromises, mount } from "@vue/test-utils";
import { nextTick, reactive } from "vue";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Workspace } from "../types/domain";
import WorkspacesView from "./WorkspacesView.vue";

const fixtures = vi.hoisted(() => ({
  workspaceStore: null as any,
  agentStore: { items: [], loadAgents: vi.fn(async () => []) },
  modelStore: {
    items: [
      {
        id: "model-1",
        name: "Gateway",
        provider: "OpenAI",
        apiKeyMasked: "***",
        apiBase: "https://example.com",
        modelName: "gpt-test",
        owner: "AI",
        lastLatencyMs: 1,
        status: "Connected",
      },
      {
        id: "model-2",
        name: "Fallback",
        provider: "OpenAI",
        apiKeyMasked: "***",
        apiBase: "https://example.com",
        modelName: "gpt-fallback",
        owner: "AI",
        lastLatencyMs: 1,
        status: "Connected",
      },
    ],
    loadModelConfigs: vi.fn(async () => []),
  },
  authStore: { user: { id: "user-owner" }, logout: vi.fn(), clearSession: vi.fn() },
  router: { push: vi.fn() },
}));

function workspace(id: string, status: "ACTIVE" | "DISABLED" = "ACTIVE"): Workspace {
  return {
    id,
    name: `Workspace-${id}`,
    displayName: `业务空间 ${id}`,
    owner: "Ops Platform",
    mode: "PRODUCTION",
    status,
    defaultAgentId: `agent-${id}`,
    modelConfigId: "model-1",
    healthScore: 98,
    toolCount: 2,
    workflowCount: 3,
    agentCount: 1,
    createdBy: "user-owner",
    createdByUsername: "workspace.creator",
    updatedBy: "user-owner",
    updatedByUsername: "workspace.editor",
    lockVersion: 1,
  };
}

const pageWorkspace = workspace("page");
const secondPageWorkspace = workspace("second", "Disabled");
const offPageWorkspace = workspace("off-page");

function createWorkspaceStore() {
  const store = reactive({
    items: [pageWorkspace, secondPageWorkspace, offPageWorkspace],
    pageItems: [pageWorkspace, secondPageWorkspace],
    pagination: { page: 1, pageSize: 10, total: 3, pageSizeOptions: [10, 20, 50] },
    listQuery: {
      query: "",
      page: 1,
      pageSize: 10,
      sortBy: undefined as string | undefined,
      sortOrder: undefined as "asc" | "desc" | undefined,
    },
    pageLoading: false,
    pageError: null as string | null,
    pageHasLoaded: true,
    activeWorkspaceId: "",
    summary: { total: 3, active: 2, production: 3, boundAgents: 3 },
    membersByWorkspace: {
      [pageWorkspace.id]: [{ userId: "user-owner", role: "OWNER", joinedAt: "2026-07-15T03:00:00Z" }],
    } as Record<string, any[]>,
    load: vi.fn(async () => store.items),
    loadWorkspacePage: vi.fn(async () => store.pageItems),
    loadMembers: vi.fn(async (workspaceId: string) => store.membersByWorkspace[workspaceId] || []),
    searchMemberCandidates: vi.fn(async () => [
      {
        userId: "user-new",
        username: "new.member",
        displayName: "新成员",
        platformRole: "USER",
      },
    ]),
    addMember: vi.fn(),
    changeMemberRole: vi.fn(),
    removeMember: vi.fn(),
    can: vi.fn(() => true),
    createWorkspace: vi.fn(),
    updateWorkspace: vi.fn(),
    enableWorkspace: vi.fn(async (id: string) => workspace(id, "ACTIVE")),
    disableWorkspace: vi.fn(async (id: string) => workspace(id, "DISABLED")),
    deleteWorkspace: vi.fn(),
    selectWorkspace: vi.fn(),
  });
  return store;
}

vi.mock("../stores/workspaces", () => ({ useWorkspaceStore: () => fixtures.workspaceStore }));
vi.mock("../stores/agents", () => ({ useAgentStore: () => fixtures.agentStore }));
vi.mock("../stores/modelConfigs", () => ({ useModelConfigStore: () => fixtures.modelStore }));
vi.mock("../stores/auth", () => ({ useAuthStore: () => fixtures.authStore }));
vi.mock("vue-router", () => ({ useRouter: () => fixtures.router }));

const ManagementListStub = {
  props: {
    rows: { type: Array, default: () => [] },
    expandedRowKey: { type: [String, Number], default: "" },
    error: { type: String, default: null },
    hasLoaded: { type: Boolean, default: false },
    pagination: { type: Object, default: () => ({ page: 1, pageSize: 10, total: 0 }) },
    checkedRowKeys: { type: Array, default: () => [] },
  },
  emits: ["select-row", "update:search", "update:checked-row-keys", "reset", "page-change", "sort-change"],
  template: `
    <div data-test="management-list" :data-expanded-row="expandedRowKey" :data-page="pagination.page" :data-total="pagination.total">
      <div v-if="checkedRowKeys.length" class="management-list-batch-bar" role="status">
        <span>已选 {{ checkedRowKeys.length }} 项</span>
        <slot name="batch-actions" :checked-row-keys="checkedRowKeys" />
        <button type="button" aria-label="取消选择" @click="$emit('update:checked-row-keys', [])">取消选择</button>
      </div>
      <button v-else data-test="search" @click="$emit('update:search', 'order')">search</button>
      <button data-test="page" @click="$emit('page-change', { page: 2, pageSize: 20 })">page</button>
      <button data-test="sort" @click="$emit('sort-change', { sortBy: 'healthScore', sortOrder: 'desc' })">sort</button>
      <button data-test="clear-sort" @click="$emit('sort-change', {})">clear sort</button>
      <button v-if="rows[0]" data-test="row" @click="$emit('select-row', rows[0])">row</button>
      <slot name="filters" />
      <template v-for="row in rows" :key="row.id">
        <slot name="cell-selection" :row="row" />
      </template>
      <slot v-if="rows[0]" name="cell-createdBy" :row="rows[0]" />
      <slot v-if="rows[0]" name="cell-updatedBy" :row="rows[0]" />
      <slot v-if="rows[0]" name="cell-actions" :row="rows[0]" />
      <slot v-if="expandedRowKey" name="row-detail" :row="rows.find((row) => row.id === expandedRowKey) || rows[0]" />
      <div v-if="error" data-test="error-slot"><slot name="error" /></div>
      <div v-else-if="hasLoaded && !rows.length" data-test="empty-slot"><slot name="empty" /></div>
    </div>
  `,
};

const ManagementRowActionsStub = {
  props: {
    primaryActions: { type: Array, default: () => [] },
    menuActions: { type: Array, default: () => [] },
  },
  emits: ["action"],
  template: `
    <div data-test="row-actions">
      <button v-for="action in [...primaryActions, ...menuActions]" :key="action.key" :data-action="action.key" @click="$emit('action', action.key)">{{ action.label }}</button>
    </div>
  `,
};

const ManagementSegmentedFilterStub = {
  props: ["ariaLabel"],
  emits: ["update:model-value"],
  template: `<button class="filter-stub" :data-label="ariaLabel" @click="$emit('update:model-value', ariaLabel.includes('环境') ? 'PRODUCTION' : 'ACTIVE')">filter</button>`,
};

const AppSelectStub = {
  props: ["modelValue", "options", "ariaLabel"],
  emits: ["update:model-value"],
  template: `
    <div class="app-select-stub" :aria-label="ariaLabel" :data-model-value="modelValue">
      <button
        v-for="option in options"
        :key="option.value"
        type="button"
        class="app-select-option"
        :data-value="option.value"
        @click="$emit('update:model-value', option.value)"
      >{{ option.label }}</button>
    </div>
  `,
};

function mountView() {
  return mount(WorkspacesView, {
    global: {
      directives: { loading: () => undefined },
      stubs: {
        ManagementList: ManagementListStub,
        ManagementRowActions: ManagementRowActionsStub,
        ManagementSegmentedFilter: ManagementSegmentedFilterStub,
        AppSelect: AppSelectStub,
        Transition: false,
      },
    },
  });
}

function putWorkspaceOnLastPage(
  item: Workspace,
  filters: { query?: string; status?: "ACTIVE" | "DISABLED"; sortBy?: string; sortOrder?: "asc" | "desc" } = {},
) {
  fixtures.workspaceStore.pageItems = [item];
  fixtures.workspaceStore.pagination = { page: 2, pageSize: 10, total: 11, pageSizeOptions: [10, 20, 50] };
  fixtures.workspaceStore.listQuery = {
    query: filters.query || "",
    status: filters.status,
    page: 2,
    pageSize: 10,
    sortBy: filters.sortBy,
    sortOrder: filters.sortOrder,
  };
}

function mockLastPageCollapse() {
  const requests: Array<Record<string, unknown>> = [];
  vi.mocked(fixtures.workspaceStore.loadWorkspacePage).mockClear();
  vi.mocked(fixtures.workspaceStore.loadWorkspacePage).mockImplementation(async (request: Record<string, unknown>) => {
    requests.push(request);
    const page = Number(request.page || fixtures.workspaceStore.listQuery.page);
    fixtures.workspaceStore.pagination = { page, pageSize: 10, total: 10, pageSizeOptions: [10, 20, 50] };
    fixtures.workspaceStore.pageItems = page === 1 ? [offPageWorkspace] : [];
    fixtures.workspaceStore.listQuery = {
      ...fixtures.workspaceStore.listQuery,
      ...request,
      page,
      pageSize: 10,
    };
    return fixtures.workspaceStore.pageItems;
  });
  return requests;
}

describe("WorkspacesView management behavior", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(pageWorkspace, workspace("page"));
    Object.assign(secondPageWorkspace, workspace("second", "Disabled"));
    Object.assign(offPageWorkspace, workspace("off-page"));
    fixtures.workspaceStore = createWorkspaceStore();
  });

  it("sends search, filters, pagination, and sorting to the Workspace page loader", async () => {
    const wrapper = mountView();
    await flushPromises();
    vi.mocked(fixtures.workspaceStore.loadWorkspacePage).mockClear();

    await wrapper.get('[data-test="search"]').trigger("click");
    expect(fixtures.workspaceStore.loadWorkspacePage).toHaveBeenLastCalledWith(
      expect.objectContaining({ query: "order", page: 1 }),
    );

    await wrapper.get('[data-label="业务空间环境筛选"]').trigger("click");
    expect(fixtures.workspaceStore.loadWorkspacePage).toHaveBeenLastCalledWith(
      expect.objectContaining({ mode: "PRODUCTION", page: 1 }),
    );

    await wrapper.get('[data-label="业务空间状态筛选"]').trigger("click");
    expect(fixtures.workspaceStore.loadWorkspacePage).toHaveBeenLastCalledWith(
      expect.objectContaining({ status: "ACTIVE", page: 1 }),
    );

    await wrapper.get('[data-test="sort"]').trigger("click");
    expect(fixtures.workspaceStore.loadWorkspacePage).toHaveBeenLastCalledWith(
      expect.objectContaining({ sortBy: "healthScore", sortOrder: "desc", page: 1 }),
    );

    await wrapper.get('[data-test="page"]').trigger("click");
    expect(fixtures.workspaceStore.loadWorkspacePage).toHaveBeenLastCalledWith(
      expect.objectContaining({ page: 2, pageSize: 20 }),
    );
  });

  it("preserves the empty sort sentinel emitted by the third sort click so the store clears sorting", async () => {
    fixtures.workspaceStore.listQuery = {
      query: "",
      page: 2,
      pageSize: 10,
      sortBy: "owner",
      sortOrder: "asc",
    };
    const wrapper = mountView();
    await flushPromises();
    vi.mocked(fixtures.workspaceStore.loadWorkspacePage).mockClear();
    vi.mocked(fixtures.workspaceStore.loadWorkspacePage).mockImplementationOnce(
      async (request: Record<string, unknown>) => {
        const sortBy =
          request.sortBy !== undefined ? String(request.sortBy) || undefined : fixtures.workspaceStore.listQuery.sortBy;
        fixtures.workspaceStore.listQuery = {
          ...fixtures.workspaceStore.listQuery,
          ...request,
          sortBy,
          sortOrder: sortBy ? request.sortOrder : undefined,
        };
        return fixtures.workspaceStore.pageItems;
      },
    );

    await wrapper.get('[data-test="clear-sort"]').trigger("click");
    await flushPromises();

    expect(fixtures.workspaceStore.loadWorkspacePage).toHaveBeenLastCalledWith(
      expect.objectContaining({ page: 1, sortBy: "", sortOrder: undefined }),
    );
    expect(fixtures.workspaceStore.listQuery).toMatchObject({ sortBy: undefined, sortOrder: undefined });
  });

  it("shows backend creation and modification actors instead of derived owner facts", async () => {
    pageWorkspace.createdBy = "user-creator";
    pageWorkspace.createdByUsername = "creator.user";
    pageWorkspace.updatedBy = "user-editor";
    pageWorkspace.updatedByUsername = "editor.user";
    const wrapper = mountView();
    await flushPromises();

    expect(wrapper.text()).toContain("@creator.user");
    expect(wrapper.text()).toContain("@editor.user");
    expect(wrapper.get('[title="@creator.user · 用户 ID：user-creator"]').exists()).toBe(true);
    expect(wrapper.get('[title="@editor.user · 用户 ID：user-editor"]').exists()).toBe(true);
  });

  it("falls back to persisted actor IDs when a username is unavailable", async () => {
    pageWorkspace.createdBy = "legacy-creator-id";
    pageWorkspace.createdByUsername = undefined;
    pageWorkspace.updatedBy = "legacy-editor-id";
    pageWorkspace.updatedByUsername = undefined;
    const wrapper = mountView();
    await flushPromises();

    expect(wrapper.text()).toContain("legacy-creator-id");
    expect(wrapper.text()).toContain("legacy-editor-id");
    expect(wrapper.get('[title="legacy-creator-id"]').exists()).toBe(true);
    expect(wrapper.get('[title="legacy-editor-id"]').exists()).toBe(true);
  });

  it("clamps a filtered single-status mutation from an empty second page back to page one", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('[data-label="业务空间状态筛选"]').trigger("click");
    putWorkspaceOnLastPage(pageWorkspace, { status: "ACTIVE", sortBy: "owner", sortOrder: "desc" });
    await nextTick();
    const requests = mockLastPageCollapse();

    await wrapper.get('[data-action="toggle"]').trigger("click");
    await flushPromises();
    await wrapper.get(".workspace-confirm-card .primary-button").trigger("click");
    await flushPromises();

    expect(requests).toEqual([
      expect.objectContaining({ status: "ACTIVE", page: 2, pageSize: 10, sortBy: "owner", sortOrder: "desc" }),
      expect.objectContaining({ status: "ACTIVE", page: 1, pageSize: 10, sortBy: "owner", sortOrder: "desc" }),
    ]);
    expect(wrapper.get('[data-test="management-list"]').attributes()).toMatchObject({
      "data-page": "1",
      "data-total": "10",
    });
  });

  it("clamps a bulk-status mutation from an empty second page back to page one", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('[data-label="业务空间状态筛选"]').trigger("click");
    putWorkspaceOnLastPage(pageWorkspace, { status: "ACTIVE", sortBy: "healthScore", sortOrder: "asc" });
    await nextTick();
    const requests = mockLastPageCollapse();

    await wrapper.get(`input[aria-label="选择${pageWorkspace.name}"]`).setValue(true);
    await wrapper.get('[data-action="bulk-disable"]').trigger("click");
    await flushPromises();

    expect(fixtures.workspaceStore.disableWorkspace).toHaveBeenCalledWith(pageWorkspace.id);
    expect(requests).toEqual([
      expect.objectContaining({ status: "ACTIVE", page: 2, pageSize: 10, sortBy: "healthScore", sortOrder: "asc" }),
      expect.objectContaining({ status: "ACTIVE", page: 1, pageSize: 10, sortBy: "healthScore", sortOrder: "asc" }),
    ]);
    expect(wrapper.get('[data-test="management-list"]').attributes()).toMatchObject({
      "data-page": "1",
      "data-total": "10",
    });
  });

  it("clamps an edit that removes the last matching row from page two back to page one", async () => {
    const editableWorkspace = { ...pageWorkspace, name: "order-space" };
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('[data-test="search"]').trigger("click");
    putWorkspaceOnLastPage(editableWorkspace, { query: "order", sortBy: "name", sortOrder: "asc" });
    await nextTick();
    vi.mocked(fixtures.workspaceStore.updateWorkspace).mockResolvedValue({ ...editableWorkspace, name: "other-space" });
    const requests = mockLastPageCollapse();

    await wrapper.get('[data-action="edit"]').trigger("click");
    await wrapper.get('input[placeholder="例如: customer-service"]').setValue("other-space");
    await wrapper.get(".workspace-modal-actions .primary-button").trigger("click");
    await flushPromises();

    expect(requests).toEqual([
      expect.objectContaining({ query: "order", page: 2, pageSize: 10, sortBy: "name", sortOrder: "asc" }),
      expect.objectContaining({ query: "order", page: 1, pageSize: 10, sortBy: "name", sortOrder: "asc" }),
    ]);
    expect(wrapper.get('[data-test="management-list"]').attributes()).toMatchObject({
      "data-page": "1",
      "data-total": "10",
    });
  });

  it("bulk-enables only explicitly selected current-page rows", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get(`input[aria-label="选择${secondPageWorkspace.name}"]`).setValue(true);
    await wrapper.get('[data-action="bulk-enable"]').trigger("click");
    await flushPromises();

    expect(fixtures.workspaceStore.enableWorkspace).toHaveBeenCalledWith(secondPageWorkspace.id);
    expect(fixtures.workspaceStore.enableWorkspace).not.toHaveBeenCalledWith(offPageWorkspace.id);
  });

  it("loads detail members and dispatches member management through the v1 store", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('[data-test="row"]').trigger("click");
    await flushPromises();

    expect(fixtures.workspaceStore.loadMembers).toHaveBeenCalledWith(pageWorkspace.id);
    expect(fixtures.workspaceStore.searchMemberCandidates).toHaveBeenCalledWith(pageWorkspace.id, "", 20);
    expect(wrapper.find('input[aria-label="成员用户 ID"]').exists()).toBe(false);
    expect(wrapper.find(".workspace-detail-page").exists()).toBe(true);
    expect(wrapper.find('[data-test="management-list"]').exists()).toBe(false);
    await wrapper.get("#workspace-detail-tab-members").trigger("click");
    expect(wrapper.text()).toContain("新成员 (@new.member) · USER");
    await wrapper.get('.app-select-stub[aria-label="选择成员用户"] [data-value="user-new"]').trigger("click");
    await wrapper.get(".workspace-member-add-button").trigger("click");
    await flushPromises();

    expect(fixtures.workspaceStore.addMember).toHaveBeenCalledWith(pageWorkspace.id, "user-new", "VIEWER");
    expect(fixtures.workspaceStore.searchMemberCandidates).toHaveBeenCalledTimes(2);
  });

  it("does not reopen a created or edited detail when the mutation result is absent from the refreshed page", async () => {
    const created = workspace("created");
    vi.mocked(fixtures.workspaceStore.createWorkspace).mockResolvedValue(created);
    const wrapper = mountView();
    await flushPromises();
    vi.mocked(fixtures.workspaceStore.loadWorkspacePage).mockImplementation(async () => {
      fixtures.workspaceStore.pageItems = [secondPageWorkspace];
      return fixtures.workspaceStore.pageItems;
    });

    await wrapper.get(`input[aria-label="选择${pageWorkspace.name}"]`).setValue(true);
    await wrapper.get(".page-actions .primary-button").trigger("click");
    await wrapper.get('input[placeholder="例如: customer-service"]').setValue(created.name);
    await wrapper.get('input[placeholder="例如: 客户服务业务空间"]').setValue(created.displayName);
    await wrapper.get(".workspace-modal-actions .primary-button").trigger("click");
    await flushPromises();
    expect(wrapper.find(".workspace-detail-page").exists()).toBe(false);
    expect(wrapper.find('[data-test="management-list"]').exists()).toBe(true);
    expect(wrapper.find(".management-list-batch-bar").exists()).toBe(false);
    expect(fixtures.workspaceStore.selectWorkspace).not.toHaveBeenCalledWith(created.id);

    fixtures.workspaceStore.pageItems = [pageWorkspace];
    await flushPromises();
    vi.mocked(fixtures.workspaceStore.updateWorkspace).mockResolvedValue(pageWorkspace);
    await wrapper.get(`input[aria-label="选择${pageWorkspace.name}"]`).setValue(true);
    await wrapper.get('[data-action="edit"]').trigger("click");
    await wrapper.get(".workspace-modal-actions .primary-button").trigger("click");
    await flushPromises();
    expect(wrapper.find(".workspace-detail-page").exists()).toBe(false);
    expect(wrapper.find('[data-test="management-list"]').exists()).toBe(true);
    expect(wrapper.find(".management-list-batch-bar").exists()).toBe(false);
    expect(fixtures.workspaceStore.selectWorkspace).not.toHaveBeenCalledWith(pageWorkspace.id);
  });

  it("keeps the v1 create form open on backdrop clicks and only submits mutable choices", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get(".page-actions .primary-button").trigger("click");
    const modal = wrapper.get('[role="dialog"][aria-label="新建业务空间"]');
    expect(modal.get('input[placeholder="例如: customer-service"]').element).toBeTruthy();
    expect(modal.get('input[placeholder="例如: 客户服务业务空间"]').element).toBeTruthy();
    expect(modal.find('input[placeholder="例如: Platform Team"]').exists()).toBe(false);
    expect(modal.find('[role="radiogroup"][aria-label="状态"]').exists()).toBe(false);

    const modeGroup = modal.get('[role="radiogroup"][aria-label="环境模式"]');
    const production = modeGroup.get("button.tone-production");
    const sandbox = modeGroup.get("button.tone-sandbox");
    expect(production.classes()).not.toContain("selected");
    expect(sandbox.classes()).toContain("selected");
    expect(sandbox.attributes("aria-checked")).toBe("true");
    await production.trigger("click");
    expect(production.classes()).toContain("selected");
    expect(sandbox.classes()).not.toContain("selected");

    await wrapper.get('[data-testid="workspace-modal-backdrop"]').trigger("click");
    expect(wrapper.get('[role="dialog"][aria-label="新建业务空间"]').exists()).toBe(true);
  });

  it("dispatches view, edit, toggle, and delete row actions", async () => {
    const wrapper = mountView();
    await flushPromises();

    await wrapper.get('[data-action="view"]').trigger("click");
    expect(wrapper.find(".workspace-detail-page").exists()).toBe(true);
    expect(wrapper.find('[data-test="management-list"]').exists()).toBe(false);
    await wrapper.get(".workspace-detail-back").trigger("click");
    expect(wrapper.find('[data-test="management-list"]').exists()).toBe(true);

    await wrapper.get('[data-action="edit"]').trigger("click");
    expect(wrapper.get('[role="dialog"][aria-label="修改业务空间"]').exists()).toBe(true);
    await wrapper.get('button[aria-label="关闭业务空间表单"]').trigger("click");

    await wrapper.get('[data-action="toggle"]').trigger("click");
    await flushPromises();
    expect(wrapper.get('[role="dialog"][aria-label="业务空间状态变更确认"]').exists()).toBe(true);
    await wrapper.get('button[aria-label="关闭状态变更确认"]').trigger("click");

    await wrapper.get('[data-action="delete"]').trigger("click");
    await flushPromises();
    expect(wrapper.get('[role="dialog"][aria-label="删除业务空间确认"]').exists()).toBe(true);
  });

  it("hides edit, lifecycle, delete, and member management actions from viewers", async () => {
    vi.mocked(fixtures.workspaceStore.can).mockImplementation(
      (_workspaceId: string, _userId: string, action: string) => action === "VIEW",
    );
    const wrapper = mountView();
    await flushPromises();

    expect(wrapper.find('[data-action="edit"]').exists()).toBe(false);
    expect(wrapper.find('[data-action="toggle"]').exists()).toBe(false);
    expect(wrapper.find('[data-action="delete"]').exists()).toBe(false);
    expect(wrapper.find(`input[aria-label="选择${pageWorkspace.name}"]`).exists()).toBe(false);
    await wrapper.get('[data-action="view"]').trigger("click");
    await flushPromises();
    await wrapper.get("#workspace-detail-tab-members").trigger("click");
    expect(wrapper.find(".workspace-member-add").exists()).toBe(false);
    expect(wrapper.find(".workspace-detail-edit").exists()).toBe(false);
  });

  it("reconciles detail and selection after status and delete mutation reloads", async () => {
    const wrapper = mountView();
    await flushPromises();
    vi.mocked(fixtures.workspaceStore.loadWorkspacePage).mockImplementation(async () => {
      fixtures.workspaceStore.pageItems = [secondPageWorkspace];
      return fixtures.workspaceStore.pageItems;
    });

    await wrapper.get(`input[aria-label="选择${pageWorkspace.name}"]`).setValue(true);
    await wrapper.get('[data-test="row"]').trigger("click");
    await wrapper.get('[data-action="toggle"]').trigger("click");
    await flushPromises();
    await wrapper.get(".workspace-confirm-card .primary-button").trigger("click");
    await flushPromises();
    expect(wrapper.find(".workspace-detail-page").exists()).toBe(false);
    expect(wrapper.find('[data-test="management-list"]').exists()).toBe(true);
    expect(wrapper.find(".management-list-batch-bar").exists()).toBe(false);
    wrapper.unmount();

    fixtures.workspaceStore = createWorkspaceStore();
    const deleteWrapper = mountView();
    await flushPromises();
    vi.mocked(fixtures.workspaceStore.loadWorkspacePage).mockImplementation(async () => {
      fixtures.workspaceStore.pageItems = [secondPageWorkspace];
      return fixtures.workspaceStore.pageItems;
    });
    await deleteWrapper.get(`input[aria-label="选择${pageWorkspace.name}"]`).setValue(true);
    await deleteWrapper.get('[data-test="row"]').trigger("click");
    await deleteWrapper.get('[data-action="delete"]').trigger("click");
    await flushPromises();
    await deleteWrapper.get(".workspace-confirm-input input").setValue(pageWorkspace.name);
    await deleteWrapper.get(".workspace-confirm-card .primary-button.danger").trigger("click");
    await flushPromises();
    expect(fixtures.workspaceStore.deleteWorkspace).toHaveBeenCalledTimes(1);
    expect(deleteWrapper.find(".workspace-detail-page").exists()).toBe(false);
    expect(deleteWrapper.find('[data-test="management-list"]').exists()).toBe(true);
    expect(deleteWrapper.find(".management-list-batch-bar").exists()).toBe(false);
  });

  it("provides ManagementList empty and error states", async () => {
    fixtures.workspaceStore.pageItems = [];
    const emptyWrapper = mountView();
    await flushPromises();
    expect(emptyWrapper.get('[data-test="empty-slot"]').text()).toContain("没有匹配的业务空间");
    emptyWrapper.unmount();

    fixtures.workspaceStore = createWorkspaceStore();
    const errorWrapper = mountView();
    await flushPromises();
    fixtures.workspaceStore.pageError = "page failed";
    await nextTick();
    expect(errorWrapper.get('[data-test="error-slot"]').text()).toContain("page failed");
  });
});
