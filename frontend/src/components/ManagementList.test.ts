import { mount } from "@vue/test-utils";
import { h, nextTick } from "vue";
import { describe, expect, it } from "vitest";

import DataTable from "./DataTable.vue";
import ManagementList, { type ManagementListColumn } from "./ManagementList.vue";
import { createTestI18n } from "../test-utils/i18n";

type TestRow = {
  id: string;
  name: string;
  status: string;
};

const rows: TestRow[] = [
  { id: "row-1", name: "Primary gateway", status: "Connected" },
  { id: "row-2", name: "Backup gateway", status: "Untested" },
];

const columns: ManagementListColumn<TestRow>[] = [
  { key: "name", label: "Name", width: 220, getValue: (row) => row.name },
  { key: "status", label: "Status", width: 140, hidable: true, getValue: (row) => row.status },
  { key: "actions", label: "Actions", width: 128 },
];

function mountList(overrides: Record<string, unknown> = {}) {
  return mount(ManagementList<TestRow>, {
    props: {
      rows,
      columns,
      rowKey: "id",
      storageKey: "management-list-columns",
      search: "",
      searchPlaceholder: "Search records",
      searchAriaLabel: "Search records",
      resetDisabled: false,
      ...overrides,
    },
    slots: {
      filters: "<button type='button'>Connected</button>",
      "cell-actions": "<button type='button'>Edit</button>",
      card: ({ row }: { row: TestRow }) => h("article", { class: "test-card" }, row.name),
      empty: "<p>No matching records</p>",
      error: ({ error }: { error: string }) => `<p>Load failed: ${error}</p>`,
    },
    global: { plugins: [createTestI18n("zh-CN")], stubs: { Teleport: true } },
  });
}

describe("ManagementList", () => {
  it("forwards controlled sorting without coordinating pagination", async () => {
    const sortableColumns: ManagementListColumn<TestRow>[] = [
      { key: "name", label: "Name", width: 220, sortable: true, sortKey: "name", getValue: (row) => row.name },
      { key: "actions", label: "Actions", width: 148 },
    ];
    const wrapper = mountList({
      columns: sortableColumns,
      sortBy: "name",
      sortOrder: "asc",
      pagination: { page: 3, pageSize: 10, total: 40 },
    });

    await wrapper.get('button[aria-label="按Name降序排序"]').trigger("click");

    expect(wrapper.emitted("sort-change")).toEqual([[{ sortBy: "name", sortOrder: "desc" }]]);
    expect(wrapper.emitted("page-change")).toBeUndefined();
  });

  it("shows reset only for active filters and keeps column settings inside the toolbar action group", () => {
    const inactive = mountList({ resetDisabled: true });

    expect(inactive.find('button[aria-label="清除筛选条件"]').exists()).toBe(false);
    expect(inactive.find('.management-list-toolbar button[aria-label="列设置"]').exists()).toBe(true);

    const active = mountList({ resetDisabled: false });
    expect(active.find('button[aria-label="清除筛选条件"]').exists()).toBe(true);
  });

  it("keeps column selection in local storage through the supplied storage key", async () => {
    const wrapper = mountList();

    await wrapper.get('button[aria-label="列设置"]').trigger("click");
    await wrapper.get('input[value="status"]').setValue(false);

    expect(wrapper.find('th[data-column-key="status"]').exists()).toBe(false);
    expect(JSON.parse(localStorage.getItem("management-list-columns") || "[]")).toEqual(["name", "actions"]);
  });

  it("supports prototype default-hidden columns while keeping fixed columns locked", async () => {
    const prototypeColumns: ManagementListColumn<TestRow>[] = [
      columns[0],
      { ...columns[1], defaultHidden: true },
      columns[2],
    ];
    const wrapper = mountList({ columns: prototypeColumns, storageKey: "prototype-default-columns" });

    expect(wrapper.find('th[data-column-key="status"]').exists()).toBe(false);
    await wrapper.get('button[aria-label="列设置"]').trigger("click");
    expect(wrapper.get('input[value="name"]').attributes("disabled")).toBeDefined();
    expect(wrapper.get('input[value="status"]').element).toMatchObject({ checked: false, disabled: false });
    expect(wrapper.findAll('[aria-label="固定列"]')).toHaveLength(2);
  });

  it("teleports column settings outside overflow-hidden list cards", async () => {
    const host = document.createElement("div");
    host.style.overflow = "hidden";
    document.body.append(host);
    const wrapper = mount(ManagementList<TestRow>, {
      attachTo: host,
      props: { rows, columns, rowKey: "id", storageKey: "teleported-columns", search: "", resetDisabled: true },
      global: { plugins: [createTestI18n("zh-CN")], stubs: { Teleport: false } },
    });

    await wrapper.get('button[aria-label="列设置"]').trigger("click");
    await nextTick();
    const menu = document.body.querySelector<HTMLElement>('[role="group"][aria-label="表格列设置"]');
    expect(menu).not.toBeNull();
    expect(host.contains(menu)).toBe(false);
    expect(getComputedStyle(menu!).position).toBe("fixed");

    menu!.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
    expect(document.body.contains(menu)).toBe(true);
    wrapper.unmount();
    host.remove();
  });

  it("restores default columns and returns focus when the Teleported menu closes with Esc", async () => {
    const wrapper = mount(ManagementList<TestRow>, {
      attachTo: document.body,
      props: { rows, columns, rowKey: "id", storageKey: "native-column-menu", search: "", resetDisabled: true },
      global: { plugins: [createTestI18n("zh-CN")], stubs: { Teleport: false } },
    });

    await wrapper.get('button[aria-label="列设置"]').trigger("click");
    await nextTick();
    const menu = document.body.querySelector<HTMLElement>('[role="group"][aria-label="表格列设置"]');
    const statusCheckbox = menu?.querySelector<HTMLInputElement>('input[value="status"]');
    expect(menu).not.toBeNull();
    expect(statusCheckbox).not.toBeNull();
    statusCheckbox!.checked = false;
    statusCheckbox!.dispatchEvent(new Event("change", { bubbles: true }));
    expect(localStorage.getItem("native-column-menu")).not.toBeNull();

    menu!.querySelector<HTMLButtonElement>('button[aria-label="恢复默认列"]')!.click();
    expect(localStorage.getItem("native-column-menu")).toBeNull();
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await nextTick();
    expect(document.body.querySelector('[role="group"][aria-label="表格列设置"]')).toBeNull();
    expect(wrapper.get('button[aria-label="列设置"]').element).toBe(document.activeElement);
    wrapper.unmount();
  });

  it("emits page changes only when a server pagination contract is provided", async () => {
    const wrapper = mountList({ pagination: { page: 1, pageSize: 20, total: 46, pageSizeOptions: [20, 50] } });

    expect(wrapper.get('[aria-label="列表分页"]').text()).toContain("共 46 项 · 第 1 / 3 页");
    await wrapper.get('button[aria-label="下一页"]').trigger("click");
    await wrapper.get('button[aria-label="每页 20 条"]').trigger("click");
    const fiftyOption = wrapper.findAll('[role="option"]').find((option) => option.text().includes("50 条"));
    expect(fiftyOption).toBeDefined();
    await fiftyOption!.trigger("click");

    expect(wrapper.emitted("page-change")).toEqual([[{ page: 2, pageSize: 20 }], [{ page: 1, pageSize: 50 }]]);
  });

  it("renders the page size menu with the selected option and keyboard navigation", async () => {
    const wrapper = mountList({ pagination: { page: 1, pageSize: 20, total: 46, pageSizeOptions: [10, 20, 50] } });

    await wrapper.get('button[aria-label="每页 20 条"]').trigger("click");

    expect(wrapper.find(".management-list-page-size-menu").exists()).toBe(true);
    expect(wrapper.find('[role="option"][aria-selected="true"]').text()).toContain("20 条");
    await wrapper.get('button[aria-label="每页 20 条"]').trigger("keydown", { key: "ArrowDown" });
    await nextTick();
    const activeOption = wrapper.findAll('[role="option"]').find((option) => option.attributes("tabindex") === "0");
    expect(activeOption?.text()).toContain("50 条");
  });

  it("keeps single-page small datasets free of inactive pagination controls", () => {
    const wrapper = mountList({ pagination: { page: 1, pageSize: 10, total: 2, pageSizeOptions: [10, 20, 50] } });

    expect(wrapper.get('[aria-label="列表分页"]').text()).toContain("共 2 项 · 第 1 / 1 页");
    expect(wrapper.find('button[aria-label="每页 10 条"]').exists()).toBe(false);
    expect(wrapper.find('button[aria-label="上一页"]').exists()).toBe(false);
    expect(wrapper.find('button[aria-label="下一页"]').exists()).toBe(false);
  });

  it("keeps the server total visible for an empty filtered page", () => {
    const wrapper = mountList({
      rows: [],
      hasLoaded: true,
      pagination: { page: 1, pageSize: 10, total: 0, pageSizeOptions: [10, 20, 50] },
    });

    expect(wrapper.get('[aria-label="列表分页"]').text()).toContain("共 0 项 · 第 1 / 1 页");
    expect(wrapper.find('button[aria-label="每页 10 条"]').exists()).toBe(false);
  });

  it("renders a card slot alongside the table for responsive presentation", () => {
    const wrapper = mountList();

    expect(wrapper.find(".data-table-scroll").exists()).toBe(true);
    expect(wrapper.findAll(".test-card")).toHaveLength(2);
    expect(wrapper.classes()).toContain("has-card-list");
  });

  it("defaults to accent selection and passes an opt-in neutral tone to DataTable", () => {
    const accentList = mountList({ selectedRowKey: "row-1" });

    expect(accentList.props("selectionTone")).toBe("accent");
    expect(accentList.getComponent(DataTable).props("selectionTone")).toBe("accent");
    expect(accentList.findAll("tbody tr").every((row) => !row.classes().includes("is-selection-neutral"))).toBe(true);

    const neutralList = mountList({ selectedRowKey: "row-1", selectionTone: "neutral" });

    expect(neutralList.getComponent(DataTable).props("selectionTone")).toBe("neutral");
    expect(neutralList.findAll("tbody tr").every((row) => row.classes().includes("is-selection-neutral"))).toBe(true);
  });

  it("separates initial loading, error, and empty states", () => {
    const loading = mountList({ rows: [], loading: true, hasLoaded: false });
    expect(loading.find(".management-list-loading").exists()).toBe(true);
    expect(loading.text()).not.toContain("No matching records");

    const failed = mountList({ rows: [], error: "network unavailable", hasLoaded: false });
    expect(failed.text()).toContain("Load failed: network unavailable");

    const empty = mountList({ rows: [], hasLoaded: true });
    expect(empty.text()).toContain("No matching records");
  });
});
