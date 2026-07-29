<script lang="ts">
export type DataTableAlign = "left" | "center" | "right";
export type DataTableSelectionTone = "accent" | "neutral";
export type DataTableSortOrder = "asc" | "desc";
export type DataTableSort = { sortBy?: string; sortOrder?: DataTableSortOrder };

export type DataTableColumn<Row extends object = Record<string, unknown>> = {
  key: string;
  label: string;
  width: number;
  align?: DataTableAlign;
  headerAlign?: DataTableAlign;
  hidable?: boolean;
  defaultHidden?: boolean;
  defaultHiddenWhenEmpty?: boolean;
  placeholderValues?: string[];
  sortable?: boolean;
  sortKey?: string;
  getValue?: (row: Row) => unknown;
};

let dataTableInstanceCount = 0;
</script>

<script setup lang="ts" generic="Row extends object">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import type { StyleValue } from "vue";

const DEFAULT_PLACEHOLDERS = ["-", "--", "暂无", "暂无数据", "暂无观测", "未测试"];

const props = withDefaults(
  defineProps<{
    rows: Row[];
    columns: DataTableColumn<Row>[];
    rowKey: keyof Row | ((row: Row) => string | number);
    stickyLeftKeys?: string[];
    stickyRightKeys?: string[];
    storageKey?: string;
    selectedRowKey?: string | number;
    expandedRowKey?: string | number;
    selectionTone?: DataTableSelectionTone;
    selectable?: boolean;
    checkable?: boolean;
    checkedRowKeys?: Array<string | number>;
    rowSelectionLabel?: (row: Row) => string;
    visibleColumnKeys?: string[];
    sortBy?: string;
    sortOrder?: DataTableSortOrder;
  }>(),
  {
    stickyLeftKeys: () => [],
    stickyRightKeys: () => [],
    storageKey: "",
    selectedRowKey: "",
    selectionTone: "accent",
    selectable: true,
    checkable: false,
    checkedRowKeys: () => [],
  },
);

const emit = defineEmits<{
  "select-row": [row: Row];
  "update:checked-row-keys": [keys: Array<string | number>];
  "sort-change": [sort: DataTableSort];
}>();

const settingsOpen = ref(false);
const settingsToolsRef = ref<HTMLElement | null>(null);
const settingsTriggerRef = ref<HTMLButtonElement | null>(null);
const tableScrollRef = ref<HTMLElement | null>(null);
const tableViewportWidth = ref(0);
const canScrollLeft = ref(false);
const canScrollRight = ref(false);
const storedVisibleKeys = ref<string[] | null>(readStoredVisibleKeys());
const dataTableInstanceId = ++dataTableInstanceCount;
const columnSettingsId = `data-table-column-settings-${dataTableInstanceId}`;
let tableResizeObserver: ResizeObserver | null = null;

const columnByKey = computed(() => new Map(props.columns.map((column) => [column.key, column])));
const hidableColumns = computed(() => props.columns.filter((column) => column.hidable));
const hasInternalColumnSettings = computed(
  () => props.visibleColumnKeys === undefined && hidableColumns.value.length > 0,
);

const defaultVisibleKeys = computed(() =>
  props.columns
    .filter((column) => !column.hidable || (!column.defaultHidden && !isColumnEmptyByDefault(column)))
    .map((column) => column.key),
);

const internalVisibleColumnKeys = computed(() => {
  if (!storedVisibleKeys.value) return defaultVisibleKeys.value;

  const validKeys = new Set(props.columns.map((column) => column.key));
  const storedKeys = storedVisibleKeys.value.filter((key) => validKeys.has(key));
  const requiredKeys = props.columns.filter((column) => !column.hidable).map((column) => column.key);
  return Array.from(new Set([...requiredKeys, ...storedKeys]));
});

const resolvedVisibleColumnKeys = computed(() => {
  if (props.visibleColumnKeys === undefined) return internalVisibleColumnKeys.value;

  const validKeys = new Set(props.columns.map((column) => column.key));
  const requestedKeys = props.visibleColumnKeys.filter((key) => validKeys.has(key));
  const requiredKeys = props.columns.filter((column) => !column.hidable).map((column) => column.key);
  return Array.from(new Set([...requiredKeys, ...requestedKeys]));
});

const visibleColumns = computed(() =>
  props.columns.filter((column) => resolvedVisibleColumnKeys.value.includes(column.key)),
);
const SELECTION_COLUMN_WIDTH = 44;
const tableMinWidth = computed(() =>
  visibleColumns.value.reduce((total, column) => total + column.width, props.checkable ? SELECTION_COLUMN_WIDTH : 0),
);
const visibleStickyLeftKeys = computed(() =>
  props.stickyLeftKeys.filter((key) => resolvedVisibleColumnKeys.value.includes(key)),
);
const visibleStickyRightKeys = computed(() =>
  props.stickyRightKeys.filter((key) => resolvedVisibleColumnKeys.value.includes(key)),
);
const expandableVisibleWidth = computed(() =>
  visibleColumns.value
    .filter((column) => !visibleStickyRightKeys.value.includes(column.key))
    .reduce((total, column) => total + column.width, 0),
);
const distributableTableSurplus = computed(() => {
  if (visibleStickyRightKeys.value.length === 0) return 0;
  return Math.max(0, tableViewportWidth.value - tableMinWidth.value);
});
const currentRowKeys = computed(() => props.rows.map(rowIdentity));
const checkedRowKeySet = computed(() => new Set(props.checkedRowKeys));
const allCurrentRowsChecked = computed(
  () => currentRowKeys.value.length > 0 && currentRowKeys.value.every((key) => checkedRowKeySet.value.has(key)),
);
const someCurrentRowsChecked = computed(
  () => !allCurrentRowsChecked.value && currentRowKeys.value.some((key) => checkedRowKeySet.value.has(key)),
);

watch(
  () => props.storageKey,
  () => {
    storedVisibleKeys.value = readStoredVisibleKeys();
  },
);

function rowIdentity(row: Row) {
  if (typeof props.rowKey === "function") return props.rowKey(row);
  return row[props.rowKey] as string | number;
}

function columnValue(row: Row, column: DataTableColumn<Row>) {
  if (column.getValue) return column.getValue(row);
  return (row as Record<string, unknown>)[column.key];
}

function displayValue(row: Row, column: DataTableColumn<Row>) {
  const value = columnValue(row, column);
  if (value === null || value === undefined || value === "") return "-";
  return String(value);
}

function isColumnEmptyByDefault(column: DataTableColumn<Row>) {
  if (!column.defaultHiddenWhenEmpty || props.rows.length === 0) return false;
  return props.rows.every((row) => isEmptyLike(columnValue(row, column), column.placeholderValues));
}

function isEmptyLike(value: unknown, placeholderValues: string[] = DEFAULT_PLACEHOLDERS) {
  if (value === null || value === undefined) return true;
  if (typeof value === "string") {
    const normalized = value.trim();
    return normalized === "" || placeholderValues.includes(normalized);
  }
  return false;
}

function isColumnVisible(key: string) {
  return resolvedVisibleColumnKeys.value.includes(key);
}

function setColumnVisible(key: string, visible: boolean) {
  const nextKeys = new Set(resolvedVisibleColumnKeys.value);
  if (visible) {
    nextKeys.add(key);
  } else {
    nextKeys.delete(key);
  }
  props.columns.forEach((column) => {
    if (!column.hidable) nextKeys.add(column.key);
  });
  storedVisibleKeys.value = props.columns.filter((column) => nextKeys.has(column.key)).map((column) => column.key);
  writeStoredVisibleKeys(storedVisibleKeys.value);
}

function restoreDefaultColumns() {
  storedVisibleKeys.value = null;
  if (props.storageKey && typeof window !== "undefined") {
    window.localStorage.removeItem(props.storageKey);
  }
}

function openColumnSettings() {
  settingsOpen.value = true;
  void nextTick(() => settingsToolsRef.value?.querySelector<HTMLInputElement>('input[type="checkbox"]')?.focus());
}

function closeColumnSettings(restoreFocus = false) {
  settingsOpen.value = false;
  if (restoreFocus) {
    void nextTick(() => settingsTriggerRef.value?.focus());
  }
}

function toggleColumnSettings() {
  if (settingsOpen.value) {
    closeColumnSettings(true);
    return;
  }
  openColumnSettings();
}

function handleColumnSettingsPointerdown(event: PointerEvent) {
  if (settingsOpen.value && settingsToolsRef.value && !settingsToolsRef.value.contains(event.target as Node)) {
    closeColumnSettings();
  }
}

function handleColumnSettingsKeydown(event: KeyboardEvent) {
  if (settingsOpen.value && event.key === "Escape") {
    event.preventDefault();
    closeColumnSettings(true);
  }
}

function selectRow(row: Row) {
  if (!props.selectable) return;
  emit("select-row", row);
}

function selectRowFromKeyboard(event: KeyboardEvent, row: Row) {
  if (!props.selectable || event.target !== event.currentTarget) return;
  event.preventDefault();
  selectRow(row);
}

function isRowChecked(row: Row) {
  return checkedRowKeySet.value.has(rowIdentity(row));
}

function toggleRowChecked(row: Row, checked: boolean) {
  const keys = new Set(props.checkedRowKeys);
  const key = rowIdentity(row);
  if (checked) keys.add(key);
  else keys.delete(key);
  emit("update:checked-row-keys", Array.from(keys));
}

function toggleCurrentRows(checked: boolean) {
  const keys = new Set(props.checkedRowKeys);
  currentRowKeys.value.forEach((key) => {
    if (checked) keys.add(key);
    else keys.delete(key);
  });
  emit("update:checked-row-keys", Array.from(keys));
}

function selectionLabel(row: Row) {
  return props.rowSelectionLabel?.(row) || `选择 ${rowIdentity(row)}`;
}

function columnSortKey(column: DataTableColumn<Row>) {
  return column.sortKey || column.key;
}

function columnSortOrder(column: DataTableColumn<Row>) {
  if (props.sortBy !== columnSortKey(column)) return undefined;
  return props.sortOrder;
}

function ariaSort(column: DataTableColumn<Row>) {
  const sortOrder = columnSortOrder(column);
  if (sortOrder === "asc") return "ascending";
  if (sortOrder === "desc") return "descending";
  return "none";
}

function sortButtonLabel(column: DataTableColumn<Row>) {
  const sortOrder = columnSortOrder(column);
  if (sortOrder === "asc") return `按${column.label}降序排序`;
  if (sortOrder === "desc") return `取消按${column.label}排序`;
  return `按${column.label}升序排序`;
}

function nextSort(column: DataTableColumn<Row>) {
  const sortKey = columnSortKey(column);
  const sortOrder = columnSortOrder(column);
  if (sortOrder === "asc") {
    emit("sort-change", { sortBy: sortKey, sortOrder: "desc" });
  } else if (sortOrder === "desc") {
    emit("sort-change", {});
  } else {
    emit("sort-change", { sortBy: sortKey, sortOrder: "asc" });
  }
}

function stickyColumnClasses(column: DataTableColumn<Row>) {
  return {
    "is-sticky-left": isStickyLeft(column.key),
    "is-sticky-right": isStickyRight(column.key),
    "is-sticky-boundary-right": visibleStickyLeftKeys.value.at(-1) === column.key,
    "is-sticky-boundary-left": visibleStickyRightKeys.value[0] === column.key,
  };
}

function headerColumnClasses(column: DataTableColumn<Row>) {
  return {
    [`is-align-${column.headerAlign || "left"}`]: true,
    ...stickyColumnClasses(column),
  };
}

function bodyColumnClasses(column: DataTableColumn<Row>) {
  return {
    [`is-align-${column.align || "left"}`]: true,
    ...stickyColumnClasses(column),
  };
}

function rowClasses(row: Row) {
  return {
    "is-selected": props.selectedRowKey !== "" && rowIdentity(row) === props.selectedRowKey,
    "is-checked": isRowChecked(row),
    "is-selection-neutral": props.selectionTone === "neutral",
    "is-selectable": props.selectable,
  };
}

function columnStyle(column: DataTableColumn<Row>): StyleValue {
  const width = effectiveColumnWidth(column);
  const style: Record<string, string> = {
    width: `${width}px`,
    minWidth: `${width}px`,
  };
  if (isStickyLeft(column.key)) {
    style.left = `${stickyLeftOffset(column.key)}px`;
  }
  if (isStickyRight(column.key)) {
    style.right = `${stickyRightOffset(column.key)}px`;
  }
  return style;
}

function effectiveColumnWidth(column: DataTableColumn<Row>) {
  const expandableWidth = expandableVisibleWidth.value;
  const surplus = distributableTableSurplus.value;
  if (!surplus || !expandableWidth || isStickyRight(column.key)) return column.width;
  return column.width + surplus * (column.width / expandableWidth);
}

function isStickyLeft(key: string) {
  return visibleStickyLeftKeys.value.includes(key);
}

function isStickyRight(key: string) {
  return visibleStickyRightKeys.value.includes(key);
}

function stickyLeftOffset(key: string) {
  let offset = props.checkable ? SELECTION_COLUMN_WIDTH : 0;
  for (const stickyKey of visibleStickyLeftKeys.value) {
    if (stickyKey === key) return offset;
    const column = columnByKey.value.get(stickyKey);
    offset += column ? effectiveColumnWidth(column) : 0;
  }
  return offset;
}

function stickyRightOffset(key: string) {
  let offset = 0;
  for (const stickyKey of [...visibleStickyRightKeys.value].reverse()) {
    if (stickyKey === key) return offset;
    const column = columnByKey.value.get(stickyKey);
    offset += column ? effectiveColumnWidth(column) : 0;
  }
  return offset;
}

function syncTableMetrics() {
  const scrollElement = tableScrollRef.value;
  if (!scrollElement) {
    tableViewportWidth.value = 0;
    canScrollLeft.value = false;
    canScrollRight.value = false;
    return;
  }

  tableViewportWidth.value = scrollElement.clientWidth;
  const maxScrollLeft = Math.max(0, scrollElement.scrollWidth - scrollElement.clientWidth);
  canScrollLeft.value = scrollElement.scrollLeft > 1;
  // Only show the right sticky edge when content can still scroll further right.
  canScrollRight.value = maxScrollLeft > 1 && scrollElement.scrollLeft < maxScrollLeft - 1;
}

function readStoredVisibleKeys() {
  if (!props.storageKey || typeof window === "undefined") return null;
  try {
    const parsed = JSON.parse(window.localStorage.getItem(props.storageKey) || "null");
    return Array.isArray(parsed) ? parsed.filter((key): key is string => typeof key === "string") : null;
  } catch {
    return null;
  }
}

function writeStoredVisibleKeys(keys: string[]) {
  if (!props.storageKey || typeof window === "undefined") return;
  window.localStorage.setItem(props.storageKey, JSON.stringify(keys));
}

watch([visibleColumns, () => props.rows.length, () => props.checkable], async () => {
  await nextTick();
  syncTableMetrics();
});

onMounted(() => {
  document.addEventListener("pointerdown", handleColumnSettingsPointerdown);
  document.addEventListener("keydown", handleColumnSettingsKeydown);
  syncTableMetrics();
  if (typeof ResizeObserver !== "undefined" && tableScrollRef.value) {
    tableResizeObserver = new ResizeObserver(syncTableMetrics);
    tableResizeObserver.observe(tableScrollRef.value);
  }
});

onBeforeUnmount(() => {
  document.removeEventListener("pointerdown", handleColumnSettingsPointerdown);
  document.removeEventListener("keydown", handleColumnSettingsKeydown);
  tableResizeObserver?.disconnect();
  tableResizeObserver = null;
});
</script>

<template>
  <div class="data-table-shell">
    <div v-if="hasInternalColumnSettings" ref="settingsToolsRef" class="data-table-tools">
      <button
        ref="settingsTriggerRef"
        class="data-table-column-button"
        type="button"
        aria-label="列设置"
        title="列设置"
        aria-haspopup="true"
        :aria-controls="columnSettingsId"
        :aria-expanded="settingsOpen"
        @click="toggleColumnSettings"
      >
        <i class="fa-solid fa-table-columns" aria-hidden="true" />
      </button>
      <div
        v-if="settingsOpen"
        :id="columnSettingsId"
        class="data-table-column-menu"
        role="group"
        aria-label="表格列设置"
      >
        <div class="data-table-column-menu-title">设置显示列</div>
        <label
          v-for="column in columns"
          :key="column.key"
          class="data-table-column-option"
          :class="{ locked: !column.hidable }"
        >
          <input
            type="checkbox"
            :value="column.key"
            :checked="isColumnVisible(column.key)"
            :disabled="!column.hidable"
            @change="setColumnVisible(column.key, ($event.target as HTMLInputElement).checked)"
          />
          <span>{{ column.label }}</span>
          <i v-if="!column.hidable" class="fa-solid fa-lock data-table-column-option-lock" aria-label="固定列" />
        </label>
        <button class="data-table-column-reset" type="button" aria-label="恢复默认列" @click="restoreDefaultColumns">
          恢复默认列
        </button>
      </div>
    </div>

    <div
      ref="tableScrollRef"
      class="data-table-scroll"
      :class="{
        'has-scroll-left': canScrollLeft,
        'has-scroll-right': canScrollRight,
      }"
      @scroll.passive="syncTableMetrics"
    >
      <table class="data-table" :style="{ minWidth: `${tableMinWidth}px` }">
        <colgroup>
          <col v-if="checkable" :style="{ width: `${SELECTION_COLUMN_WIDTH}px` }" />
          <col
            v-for="column in visibleColumns"
            :key="column.key"
            :style="{ width: `${effectiveColumnWidth(column)}px` }"
          />
        </colgroup>
        <thead>
          <tr>
            <th
              v-if="checkable"
              class="data-table-selection-cell is-sticky-left"
              :style="{ width: `${SELECTION_COLUMN_WIDTH}px`, minWidth: `${SELECTION_COLUMN_WIDTH}px`, left: '0px' }"
              scope="col"
            >
              <input
                class="data-table-checkbox"
                type="checkbox"
                aria-label="选择当前页全部"
                :checked="allCurrentRowsChecked"
                :indeterminate="someCurrentRowsChecked"
                @change="toggleCurrentRows(($event.target as HTMLInputElement).checked)"
              />
            </th>
            <th
              v-for="column in visibleColumns"
              :key="column.key"
              :class="headerColumnClasses(column)"
              :style="columnStyle(column)"
              :data-column-key="column.key"
              scope="col"
              :aria-sort="column.sortable ? ariaSort(column) : undefined"
            >
              <button
                v-if="column.sortable"
                class="data-table-sort-button"
                type="button"
                :aria-label="sortButtonLabel(column)"
                @click="nextSort(column)"
              >
                <span>{{ column.label }}</span>
                <i
                  class="fa-solid"
                  :class="
                    columnSortOrder(column) === 'asc'
                      ? 'fa-arrow-up'
                      : columnSortOrder(column) === 'desc'
                        ? 'fa-arrow-down'
                        : 'fa-sort'
                  "
                  aria-hidden="true"
                />
              </button>
              <template v-else>{{ column.label }}</template>
            </th>
          </tr>
        </thead>
        <tbody>
          <template v-for="row in rows" :key="rowIdentity(row)">
            <tr
              :class="rowClasses(row)"
              :role="selectable ? 'button' : undefined"
              :tabindex="selectable ? 0 : undefined"
              :aria-selected="selectable ? rowIdentity(row) === selectedRowKey : undefined"
              @click="selectRow(row)"
              @keydown.enter="selectRowFromKeyboard($event, row)"
              @keydown.space="selectRowFromKeyboard($event, row)"
            >
              <td
                v-if="checkable"
                class="data-table-selection-cell is-sticky-left"
                :style="{ width: `${SELECTION_COLUMN_WIDTH}px`, minWidth: `${SELECTION_COLUMN_WIDTH}px`, left: '0px' }"
              >
                <input
                  class="data-table-checkbox"
                  type="checkbox"
                  :aria-label="selectionLabel(row)"
                  :checked="isRowChecked(row)"
                  @click.stop
                  @change="toggleRowChecked(row, ($event.target as HTMLInputElement).checked)"
                />
              </td>
              <td
                v-for="column in visibleColumns"
                :key="column.key"
                :class="bodyColumnClasses(column)"
                :style="columnStyle(column)"
                :data-column-key="column.key"
              >
                <slot :name="`cell-${column.key}`" :row="row" :column="column" :value="columnValue(row, column)">
                  {{ displayValue(row, column) }}
                </slot>
              </td>
            </tr>
            <tr v-if="$slots['row-detail'] && rowIdentity(row) === expandedRowKey" class="data-table-detail-row">
              <td class="data-table-detail-cell" :colspan="visibleColumns.length + (checkable ? 1 : 0)">
                <slot name="row-detail" :row="row" :columns="visibleColumns" />
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.data-table-shell {
  position: relative;
  width: 100%;
  min-width: 0;
  max-width: 100%;
}

.data-table-tools {
  position: relative;
  z-index: 10;
  display: flex;
  justify-content: flex-end;
  padding: 8px 16px;
  border-bottom: 1px solid #f3f4f6;
  background: #fff;
}

.data-table-column-button {
  display: inline-flex;
  width: 40px;
  height: 40px;
  align-items: center;
  justify-content: center;
  border: 1px solid #e5e7eb;
  border-radius: 0.75rem;
  background: #f9fafb;
  color: #4b5563;
  cursor: pointer;
  transition:
    background-color 0.2s ease,
    color 0.2s ease,
    box-shadow 0.2s ease,
    border-color 0.2s ease;
}

.data-table-column-button:hover,
.data-table-column-button:focus-visible {
  border-color: #d1d5db;
  background: #fff;
  color: #111827;
  box-shadow: 0 2px 8px rgba(15, 23, 42, 0.06);
}

.data-table-column-menu {
  position: absolute;
  top: calc(100% + 8px);
  right: 0;
  min-width: 180px;
  padding: 8px;
  border: 1px solid #f3f4f6;
  border-radius: 0.75rem;
  background: #fff;
  box-shadow: 0 16px 44px rgba(15, 23, 42, 0.1);
}

.data-table-column-menu-title {
  padding: 8px 8px 6px;
  color: #111827;
  font-size: 0.8125rem;
  font-weight: 700;
  line-height: 1.25;
}

.data-table-column-option {
  display: flex;
  min-height: 36px;
  align-items: center;
  gap: 8px;
  padding: 7px 8px;
  border-radius: 0.5rem;
  color: #4b5563;
  font-size: 0.8125rem;
  font-weight: 600;
  line-height: 1.25;
  cursor: pointer;
}

.data-table-column-option:hover {
  background: #ecfdf5;
}

.data-table-column-option input {
  width: 16px;
  height: 16px;
  accent-color: var(--aw-primary, #0d9488);
}

.data-table-column-option.locked {
  color: #9ca3af;
  cursor: default;
}

.data-table-column-option-lock {
  margin-left: auto;
  color: #9ca3af;
  font-size: 0.625rem;
}

.data-table-column-reset {
  width: 100%;
  min-height: 40px;
  margin-top: 6px;
  padding: 8px;
  border: 0;
  border-top: 1px solid #f3f4f6;
  background: transparent;
  color: #059669;
  font: inherit;
  font-size: 0.8125rem;
  font-weight: 600;
  cursor: pointer;
}

.data-table-column-reset:hover,
.data-table-column-reset:focus-visible {
  background: #f9fafb;
}

.data-table-scroll {
  width: 100%;
  min-width: 0;
  max-width: 100%;
  overflow-x: auto;
  scrollbar-color: #d1d5db #fff;
  scrollbar-width: thin;
}

.data-table-scroll::-webkit-scrollbar {
  width: 9px;
  height: 9px;
}

.data-table-scroll::-webkit-scrollbar-track {
  background: #fff;
}

.data-table-scroll::-webkit-scrollbar-thumb {
  border: 3px solid #fff;
  border-radius: 10px;
  background: #d1d5db;
}

.data-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
  text-align: left;
  white-space: nowrap;
  font-family: var(
    --aw-table-font,
    Inter,
    -apple-system,
    BlinkMacSystemFont,
    "Segoe UI",
    Roboto,
    "Helvetica Neue",
    Arial,
    sans-serif
  );
}

.data-table thead tr {
  border-bottom: 1px solid #f3f4f6;
  background: #f9fafb;
}

.data-table th {
  position: sticky;
  top: 0;
  z-index: 3;
  height: 44px;
  padding: 0 16px;
  background: #f9fafb;
  color: var(--aw-table-header-color, #6b7280);
  font-size: var(--aw-table-header-size, 0.75rem);
  font-weight: var(--aw-table-header-weight, 600);
  letter-spacing: normal;
  line-height: 1.4;
  text-transform: none;
}

.data-table-sort-button {
  display: flex;
  width: calc(100% + 32px);
  height: 44px;
  min-height: 44px;
  align-items: center;
  justify-content: flex-start;
  gap: 6px;
  margin: -1px -16px;
  padding: 0 16px;
  border: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  letter-spacing: inherit;
  text-transform: inherit;
  cursor: pointer;
}

.data-table th.is-align-center .data-table-sort-button {
  justify-content: center;
}

.data-table th.is-align-right .data-table-sort-button {
  justify-content: flex-end;
}

.data-table-sort-button:focus-visible {
  border-radius: 0.5rem;
  outline: 2px solid rgba(16, 185, 129, 0.45);
  outline-offset: 2px;
}

.data-table tbody {
  color: var(--aw-table-body-color, #374151);
  font-size: var(--aw-table-body-size, 0.8125rem);
  font-weight: var(--aw-table-body-weight, 400);
}

.data-table tbody tr {
  border-bottom: 1px solid #f3f4f6;
  outline: none;
  transition: background-color 0.15s ease;
}

.data-table tbody tr.is-selectable {
  cursor: pointer;
}

.data-table tbody tr:hover,
.data-table tbody tr:focus-visible {
  background: #fafafa;
}

.data-table tbody tr:focus-visible {
  box-shadow: inset 0 0 0 2px rgba(148, 163, 184, 0.45);
}

.data-table tbody tr.is-selected,
.data-table tbody tr.is-checked {
  box-shadow: inset 2px 0 0 #0d9488;
  background: #f8fafc;
}

.data-table tbody tr.is-selection-neutral:focus-visible {
  box-shadow: inset 0 0 0 2px rgba(100, 116, 139, 0.35);
}

.data-table tbody tr.is-selection-neutral.is-selected {
  box-shadow: none;
  background: #fbfdff;
}

.data-table td {
  height: 56px;
  padding: 0 16px;
  overflow: hidden;
  background: #fff;
  color: var(--aw-table-body-color, #374151);
  font-size: var(--aw-table-body-size, 0.8125rem);
  font-weight: var(--aw-table-body-weight, 400);
  line-height: 1.4;
  vertical-align: middle;
}

.data-table th[data-column-key="actions"],
.data-table tbody td[data-column-key="actions"] {
  padding-right: 12px;
  padding-left: 8px;
}

.data-table tbody tr:hover td,
.data-table tbody tr:focus-visible td {
  background: #fafafa;
}

.data-table tbody tr.is-selected td {
  background: #f8fafc;
}

.data-table tbody tr.is-checked td {
  background: #f8fafc;
}

.data-table tbody tr.is-selection-neutral.is-selected td {
  background: #fbfdff;
}

.data-table-selection-cell {
  width: 44px;
  min-width: 44px;
  padding: 0 !important;
  text-align: center;
}

.data-table-checkbox {
  appearance: none;
  -webkit-appearance: none;
  width: 14px;
  height: 14px;
  margin: 0;
  border: 1px solid #cbd5e1;
  border-radius: 3px;
  background: #fff;
  box-shadow: none;
  cursor: pointer;
  vertical-align: middle;
  transition:
    background-color 0.12s ease,
    border-color 0.12s ease,
    box-shadow 0.12s ease;
}

.data-table-checkbox:hover {
  border-color: #94a3b8;
}

.data-table-checkbox:focus-visible {
  outline: 0;
  border-color: #0d9488;
  box-shadow: 0 0 0 2px rgba(13, 148, 136, 0.2);
}

/* Scheme A: solid brand fill + thin white check */
.data-table-checkbox:checked {
  border-color: #0d9488;
  background-color: #0d9488;
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16' fill='none'%3E%3Cpath d='M3.5 8.2 6.6 11.2 12.5 4.8' stroke='%23ffffff' stroke-width='1.7' stroke-linecap='round' stroke-linejoin='round'/%3E%3C/svg%3E");
  background-repeat: no-repeat;
  background-position: center;
  background-size: 12px 12px;
}

.data-table-checkbox:indeterminate {
  border-color: #0d9488;
  background-color: #0d9488;
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16' fill='none'%3E%3Cpath d='M4 8h8' stroke='%23ffffff' stroke-width='1.7' stroke-linecap='round'/%3E%3C/svg%3E");
  background-repeat: no-repeat;
  background-position: center;
  background-size: 12px 12px;
}

.data-table th.data-table-selection-cell {
  z-index: 5;
  background: #f9fafb;
}

.data-table td.data-table-selection-cell {
  z-index: 3;
}

.data-table tbody td.data-table-detail-cell {
  padding: 0;
  background: inherit;
}

.data-table .is-align-center {
  text-align: center;
}

.data-table .is-align-right {
  text-align: right;
}

.data-table .is-sticky-left,
.data-table .is-sticky-right {
  position: sticky;
  background: #fff;
}

/* Clip sticky cells so long content cannot paint into neighboring columns.
   Edge shadows use inset box-shadow (not external pseudo) so overflow:hidden is safe. */
.data-table .is-sticky-boundary-right,
.data-table .is-sticky-boundary-left {
  isolation: isolate;
  overflow: hidden;
}

.data-table th.is-sticky-left,
.data-table th.is-sticky-right {
  z-index: 4;
  background: #f9fafb;
  overflow: hidden;
}

.data-table td.is-sticky-left,
.data-table td.is-sticky-right {
  z-index: 2;
  overflow: hidden;
}

/* Left sticky edge appears only after the table has been scrolled horizontally. */
.data-table-scroll.has-scroll-left .data-table .is-sticky-boundary-right {
  box-shadow: 4px 0 10px -6px rgba(15, 23, 42, 0.16);
}

/* Right sticky edge appears only while more content remains off-screen to the right. */
.data-table-scroll.has-scroll-right .data-table .is-sticky-boundary-left {
  box-shadow: -4px 0 10px -6px rgba(15, 23, 42, 0.16);
}
</style>
