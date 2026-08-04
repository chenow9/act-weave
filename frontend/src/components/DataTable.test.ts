import { mount } from "@vue/test-utils";
import { h, nextTick } from "vue";
import { describe, expect, it, vi } from "vitest";

import DataTable, { type DataTableColumn, type DataTableSelectionTone } from "./DataTable.vue";
import { createTestI18n } from "../test-utils/i18n";

type TestRow = {
  id: string;
  name: string;
  owner: string;
  status: string;
  observation: string;
};

const rows: TestRow[] = [
  { id: "row-1", name: "生产网关", owner: "Ops", status: "Connected", observation: "暂无观测" },
  { id: "row-2", name: "备用网关", owner: "Platform", status: "Connected", observation: "暂无观测" },
];

const columns: DataTableColumn<TestRow>[] = [
  { key: "name", label: "配置名称", width: 220, getValue: (row) => row.name },
  { key: "owner", label: "Owner", width: 140, hidable: true, getValue: (row) => row.owner },
  { key: "status", label: "状态", width: 120, hidable: true, getValue: (row) => row.status },
  {
    key: "observation",
    label: "观测",
    width: 160,
    hidable: true,
    defaultHiddenWhenEmpty: true,
    getValue: (row) => row.observation,
  },
  { key: "actions", label: "操作", width: 128, align: "right" },
];

function mountTable(storageKey = "test-table-columns", options: { selectionTone?: DataTableSelectionTone } = {}) {
  return mount(DataTable<TestRow>, {
    props: {
      rows,
      columns,
      rowKey: "id",
      stickyLeftKeys: ["name"],
      stickyRightKeys: ["actions"],
      storageKey,
      selectedRowKey: "row-1",
      ...options,
    },
    slots: {
      "cell-name": ({ row }: { row: TestRow }) => `<strong>${row.name}</strong><span>${row.owner}</span>`,
      "cell-actions": () => h("button", { type: "button" }, "编辑"),
    },
    global: { plugins: [createTestI18n("zh-CN")] },
  });
}

function renderedColumnWidth(wrapper: ReturnType<typeof mountTable>, key: string) {
  return Number.parseFloat((wrapper.get(`th[data-column-key="${key}"]`).element as HTMLElement).style.width);
}

describe("DataTable", () => {
  it("renders one controlled detail row for the matching key with the visible column span", async () => {
    const detailRows = rows.map((row) => ({ ...row, observation: "Healthy" }));
    const wrapper = mount(DataTable<TestRow>, {
      props: { rows: detailRows, columns, rowKey: "id", expandedRowKey: "row-2" },
      global: { plugins: [createTestI18n("zh-CN")] },
      slots: {
        "row-detail": ({ row, columns: visibleColumns }: { row: TestRow; columns: DataTableColumn<TestRow>[] }) =>
          h("section", { class: "test-row-detail", "data-column-count": visibleColumns.length }, row.name),
      },
    });

    expect(wrapper.findAll(".data-table-detail-row")).toHaveLength(1);
    expect(wrapper.get(".test-row-detail").text()).toBe("备用网关");
    expect(wrapper.get(".test-row-detail").attributes("data-column-count")).toBe(String(columns.length));
    expect(wrapper.get(".data-table-detail-cell").attributes("colspan")).toBe(String(columns.length));

    await wrapper.setProps({ visibleColumnKeys: ["name", "status", "actions"] });

    expect(wrapper.get(".data-table-detail-cell").attributes("colspan")).toBe("3");
    expect(wrapper.get(".test-row-detail").attributes("data-column-count")).toBe("3");
  });

  it("keeps the existing row DOM when no expanded key is provided", () => {
    const wrapper = mount(DataTable<TestRow>, {
      global: { plugins: [createTestI18n("zh-CN")] },
      props: { rows, columns, rowKey: "id" },
      slots: {
        "row-detail": ({ row }: { row: TestRow }) => h("section", { class: "test-row-detail" }, row.name),
      },
    });

    expect(wrapper.find(".data-table-detail-row").exists()).toBe(false);
    expect(wrapper.find(".data-table-detail-cell").exists()).toBe(false);
    expect(wrapper.findAll("tbody tr")).toHaveLength(rows.length);
  });

  it("emits controlled three-state sorting without changing fixed action geometry or row order", async () => {
    const sortableColumns: DataTableColumn<TestRow>[] = [
      { key: "name", label: "配置名称", width: 220, sortable: true, sortKey: "name", getValue: (row) => row.name },
      { key: "actions", label: "操作", width: 148 },
    ];
    const wrapper = mount(DataTable<TestRow>, {
      global: { plugins: [createTestI18n("zh-CN")] },
      props: { rows, columns: sortableColumns, rowKey: "id", stickyRightKeys: ["actions"] },
    });

    const button = wrapper.get('button[aria-label="按配置名称升序排序"]');
    const actionHeader = wrapper.get('th[data-column-key="actions"]');
    expect(wrapper.get('th[data-column-key="name"]').attributes("aria-sort")).toBe("none");
    expect(actionHeader.find("button").exists()).toBe(false);
    expect(actionHeader.attributes("style")).toContain("width: 148px");

    await button.trigger("click");
    expect(wrapper.emitted("sort-change")?.[0]).toEqual([{ sortBy: "name", sortOrder: "asc" }]);
    expect(wrapper.findAll("tbody tr")[0].text()).toContain("生产网关");

    await wrapper.setProps({ sortBy: "name", sortOrder: "asc" });
    expect(wrapper.get('th[data-column-key="name"]').attributes("aria-sort")).toBe("ascending");
    await button.trigger("click");
    expect(wrapper.emitted("sort-change")?.[1]).toEqual([{ sortBy: "name", sortOrder: "desc" }]);

    await wrapper.setProps({ sortBy: "name", sortOrder: "desc" });
    expect(wrapper.get('th[data-column-key="name"]').attributes("aria-sort")).toBe("descending");
    await button.trigger("click");
    expect(wrapper.emitted("sort-change")?.[2]).toEqual([{}]);
  });

  it("keeps configured left and right columns sticky with visible separators", () => {
    const wrapper = mountTable();

    const nameHeader = wrapper.find('th[data-column-key="name"]');
    const actionHeader = wrapper.find('th[data-column-key="actions"]');
    const nameCell = wrapper.find('tbody td[data-column-key="name"]');
    const actionCell = wrapper.find('tbody td[data-column-key="actions"]');

    expect(wrapper.find(".data-table-scroll").exists()).toBe(true);
    expect(nameHeader.classes()).toContain("is-sticky-left");
    expect(nameCell.classes()).toContain("is-sticky-left");
    expect(actionHeader.classes()).toContain("is-sticky-right");
    expect(actionCell.classes()).toContain("is-sticky-right");
    expect(nameHeader.classes()).toContain("is-sticky-boundary-right");
    expect(actionHeader.classes()).toContain("is-sticky-boundary-left");
    expect(nameHeader.attributes("style")).toContain("left: 0px");
    expect(actionHeader.attributes("style")).toContain("right: 0px");
  });

  it("keeps body alignment separate from default header alignment", () => {
    const wrapper = mountTable();

    const actionHeader = wrapper.find('th[data-column-key="actions"]');
    const actionCell = wrapper.find('tbody td[data-column-key="actions"]');

    expect(actionHeader.classes()).not.toContain("is-align-right");
    expect(actionCell.classes()).toContain("is-align-right");
  });

  it("preserves right-sticky widths and distributes surplus across the remaining visible columns", async () => {
    const resizeCallbacks: ResizeObserverCallback[] = [];
    const disconnect = vi.fn();

    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(callback: ResizeObserverCallback) {
          resizeCallbacks.push(callback);
        }

        observe() {}
        unobserve() {}
        disconnect() {
          disconnect();
        }
      },
    );

    const wrapper = mountTable("fixed-action-width-columns");
    const scroll = wrapper.get(".data-table-scroll");
    Object.defineProperty(scroll.element, "clientWidth", { configurable: true, value: 808 });

    expect(resizeCallbacks).toHaveLength(1);
    resizeCallbacks[0]([], {} as ResizeObserver);
    await nextTick();

    expect(renderedColumnWidth(wrapper, "actions")).toBe(128);
    expect(renderedColumnWidth(wrapper, "name")).toBeCloseTo(311.67, 1);
    expect(renderedColumnWidth(wrapper, "owner")).toBeCloseTo(198.33, 1);
    expect(renderedColumnWidth(wrapper, "status")).toBeCloseTo(170, 1);
    expect(
      ["name", "owner", "status", "actions"].reduce((total, key) => total + renderedColumnWidth(wrapper, key), 0),
    ).toBeCloseTo(808, 1);

    Object.defineProperty(scroll.element, "clientWidth", { configurable: true, value: 500 });
    resizeCallbacks[0]([], {} as ResizeObserver);
    await nextTick();

    expect(renderedColumnWidth(wrapper, "name")).toBe(220);
    expect(renderedColumnWidth(wrapper, "owner")).toBe(140);
    expect(renderedColumnWidth(wrapper, "status")).toBe(120);
    expect(renderedColumnWidth(wrapper, "actions")).toBe(128);
    expect(wrapper.get(".data-table").attributes("style")).toContain("min-width: 608px");

    Object.defineProperty(scroll.element, "clientWidth", { configurable: true, value: 808 });
    resizeCallbacks[0]([], {} as ResizeObserver);
    await nextTick();

    await wrapper.get('button[aria-label="列设置"]').trigger("click");
    await wrapper.get('input[value="owner"]').setValue(false);
    await nextTick();

    expect(renderedColumnWidth(wrapper, "actions")).toBe(128);
    expect(renderedColumnWidth(wrapper, "name")).toBeCloseTo(440, 1);
    expect(renderedColumnWidth(wrapper, "status")).toBeCloseTo(240, 1);

    wrapper.unmount();
    expect(disconnect).toHaveBeenCalledOnce();
  });

  it("defaults placeholder-only hidable columns to hidden", async () => {
    const wrapper = mountTable();

    expect(wrapper.find('th[data-column-key="observation"]').exists()).toBe(false);

    await wrapper.find('button[aria-label="列设置"]').trigger("click");

    const observationToggle = wrapper.find('input[value="observation"]');
    expect(observationToggle.exists()).toBe(true);
    expect((observationToggle.element as HTMLInputElement).checked).toBe(false);
  });

  it("persists user column visibility preferences", async () => {
    const wrapper = mountTable("persisted-table-columns");

    await wrapper.find('button[aria-label="列设置"]').trigger("click");
    await wrapper.find('input[value="owner"]').setValue(false);

    expect(wrapper.find('th[data-column-key="owner"]').exists()).toBe(false);
    expect(JSON.parse(localStorage.getItem("persisted-table-columns") || "[]")).toEqual(["name", "status", "actions"]);

    wrapper.unmount();
    const nextWrapper = mountTable("persisted-table-columns");
    expect(nextWrapper.find('th[data-column-key="owner"]').exists()).toBe(false);
  });

  it("exposes column settings as a dismissible checkbox group with restore defaults", async () => {
    const wrapper = mountTable("accessible-table-columns");
    const trigger = wrapper.get('button[aria-label="列设置"]');

    expect(trigger.attributes("aria-expanded")).toBe("false");
    await trigger.trigger("click");

    expect(trigger.attributes("aria-expanded")).toBe("true");
    expect(wrapper.get('[role="group"][aria-label="表格列设置"]').exists()).toBe(true);
    await wrapper.get('input[value="owner"]').setValue(false);
    expect(wrapper.find('th[data-column-key="owner"]').exists()).toBe(false);

    await wrapper.get('button[aria-label="恢复默认列"]').trigger("click");
    expect(wrapper.find('th[data-column-key="owner"]').exists()).toBe(true);
    expect(localStorage.getItem("accessible-table-columns")).toBeNull();

    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await wrapper.vm.$nextTick();
    expect(wrapper.find('[role="group"][aria-label="表格列设置"]').exists()).toBe(false);
    expect(trigger.attributes("aria-expanded")).toBe("false");
  });

  it("does not turn keyboard activation on cell controls into row selection", () => {
    const wrapper = mountTable();
    const actionButton = wrapper.get('tbody button[type="button"]');

    for (const key of ["Enter", " "]) {
      const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
      const defaultAllowed = actionButton.element.dispatchEvent(event);

      expect(defaultAllowed).toBe(true);
    }

    expect(wrapper.emitted("select-row")).toBeUndefined();
  });
});
