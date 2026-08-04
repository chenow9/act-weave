<script lang="ts">
import type { DataTableColumn, DataTableSelectionTone, DataTableSort, DataTableSortOrder } from "./DataTable.vue";

export type ManagementListColumn<Row extends object = Record<string, unknown>> = DataTableColumn<Row>;

export type ManagementListPagination = {
  page: number;
  pageSize: number;
  total: number;
  pageSizeOptions?: number[];
};

let managementListInstanceCount = 0;
</script>

<script setup lang="ts" generic="Row extends object">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useSlots, watch } from "vue";
import { useI18n } from "vue-i18n";

import DataTable from "./DataTable.vue";

const { t } = useI18n();

const props = withDefaults(
  defineProps<{
    rows: Row[];
    columns: ManagementListColumn<Row>[];
    rowKey: keyof Row | ((row: Row) => string | number);
    storageKey?: string;
    stickyLeftKeys?: string[];
    stickyRightKeys?: string[];
    selectedRowKey?: string | number;
    expandedRowKey?: string | number;
    selectionTone?: DataTableSelectionTone;
    selectable?: boolean;
    checkable?: boolean;
    checkedRowKeys?: Array<string | number>;
    rowSelectionLabel?: (row: Row) => string;
    loading?: boolean;
    error?: string | null;
    hasLoaded?: boolean;
    search?: string;
    searchPlaceholder?: string;
    searchAriaLabel?: string;
    clearSearchAriaLabel?: string;
    resetLabel?: string;
    resetAriaLabel?: string;
    resetDisabled?: boolean;
    pagination?: ManagementListPagination;
    sortBy?: string;
    sortOrder?: DataTableSortOrder;
  }>(),
  {
    storageKey: "",
    stickyLeftKeys: () => [],
    stickyRightKeys: () => [],
    selectedRowKey: "",
    selectionTone: "accent",
    selectable: true,
    checkable: false,
    checkedRowKeys: () => [],
    loading: false,
    error: null,
    hasLoaded: false,
    search: "",
    searchPlaceholder: "",
    searchAriaLabel: "",
    clearSearchAriaLabel: "",
    resetLabel: "",
    resetAriaLabel: "",
    resetDisabled: false,
    pagination: undefined,
  },
);

const resolvedSearchPlaceholder = computed(() => props.searchPlaceholder || t("common.search"));
const resolvedSearchAriaLabel = computed(() => props.searchAriaLabel || t("common.searchList"));
const resolvedClearSearchAriaLabel = computed(() => props.clearSearchAriaLabel || t("common.clearSearch"));
const resolvedResetLabel = computed(() => props.resetLabel || t("common.clearFilters"));
const resolvedResetAriaLabel = computed(() => props.resetAriaLabel || t("common.clearFiltersAria"));
const emptyLikePlaceholders = computed(() => [
  "-",
  "--",
  t("common.emptyPlaceholder"),
  t("common.noData"),
  t("common.noObservation"),
  t("common.untested"),
]);

const emit = defineEmits<{
  "select-row": [row: Row];
  "update:checked-row-keys": [keys: Array<string | number>];
  "update:search": [value: string];
  reset: [];
  "page-change": [pagination: { page: number; pageSize: number }];
  "sort-change": [sort: DataTableSort];
}>();

const slots = useSlots();
const COLUMN_SETTINGS_MENU_WIDTH = 218;
const VIEWPORT_MARGIN = 8;
const POPOVER_GAP = 8;
const managementListInstanceId = ++managementListInstanceCount;
const columnSettingsId = `management-list-column-settings-${managementListInstanceId}`;
const pageSizeMenuId = `management-list-page-size-menu-${managementListInstanceId}`;
const hasCardSlot = computed(() => Boolean(slots.card));
const hasRows = computed(() => props.rows.length > 0);
const showInitialLoading = computed(() => props.loading && !props.hasLoaded && !hasRows.value);
const showErrorState = computed(() => Boolean(props.error) && !hasRows.value);
const showEmptyState = computed(() => props.hasLoaded && !props.loading && !props.error && !hasRows.value);
const showList = computed(() => hasRows.value);
const hidableColumns = computed(() => props.columns.filter((column) => column.hidable));
const showColumnSettings = computed(() => showList.value && hidableColumns.value.length > 0);
const storedVisibleColumnKeys = ref<string[] | null>(readStoredVisibleColumnKeys());
const columnSettingsOpen = ref(false);
const columnSettingsToolsRef = ref<HTMLElement | null>(null);
const columnSettingsTriggerRef = ref<HTMLButtonElement | null>(null);
const columnSettingsMenuRef = ref<HTMLElement | null>(null);
const columnSettingsMenuStyle = ref<Record<string, string>>({ position: "fixed", visibility: "hidden" });
const pageSizeMenuOpen = ref(false);
const pageSizeMenuActiveIndex = ref(0);
const pageSizeMenuRef = ref<HTMLElement | null>(null);
const pageSizeTriggerRef = ref<HTMLButtonElement | null>(null);
const pageSizeOptionRefs = ref<HTMLButtonElement[]>([]);
const pageCount = computed(() => {
  if (!props.pagination) return 0;
  return Math.max(1, Math.ceil(props.pagination.total / props.pagination.pageSize));
});
const visiblePageNumbers = computed(() => {
  if (!props.pagination) return [];
  const count = pageCount.value;
  if (count <= 5) return Array.from({ length: count }, (_, index) => index + 1);
  const start = Math.min(Math.max(1, props.pagination.page - 2), count - 4);
  return Array.from({ length: 5 }, (_, index) => start + index);
});
const pageSizeOptions = computed(() => {
  if (!props.pagination) return [];
  const options = props.pagination.pageSizeOptions || [10, 20, 50];
  return options.includes(props.pagination.pageSize) ? options : [props.pagination.pageSize, ...options];
});
const currentPageSizeIndex = computed(() => {
  const index = pageSizeOptions.value.indexOf(props.pagination?.pageSize || 0);
  return index >= 0 ? index : 0;
});
const minimumPageSize = computed(() => Math.min(...pageSizeOptions.value));
const showPageSizeControl = computed(() => Boolean(props.pagination && props.pagination.total > minimumPageSize.value));
const showPageNavigation = computed(() => pageCount.value > 1);
const showPaginationControls = computed(() => showPageSizeControl.value || showPageNavigation.value);
const defaultVisibleColumnKeys = computed(() =>
  props.columns
    .filter((column) => !column.hidable || (!column.defaultHidden && !isColumnEmptyByDefault(column)))
    .map((column) => column.key),
);
const visibleColumnKeys = computed(() => {
  if (!storedVisibleColumnKeys.value) return defaultVisibleColumnKeys.value;

  const validKeys = new Set(props.columns.map((column) => column.key));
  const storedKeys = storedVisibleColumnKeys.value.filter((key) => validKeys.has(key));
  const requiredKeys = props.columns.filter((column) => !column.hidable).map((column) => column.key);
  return Array.from(new Set([...requiredKeys, ...storedKeys]));
});

watch(
  () => props.storageKey,
  () => {
    storedVisibleColumnKeys.value = readStoredVisibleColumnKeys();
  },
);

function displayCellValue(value: unknown) {
  if (value === null || value === undefined || value === "") return "-";
  return String(value);
}

function columnValue(row: Row, column: ManagementListColumn<Row>) {
  if (column.getValue) return column.getValue(row);
  return (row as Record<string, unknown>)[column.key];
}

function isEmptyLike(value: unknown, placeholderValues?: string[]) {
  const placeholders = placeholderValues ?? emptyLikePlaceholders.value;
  if (value === null || value === undefined) return true;
  if (typeof value !== "string") return false;
  const normalized = value.trim();
  return normalized === "" || placeholders.includes(normalized);
}

function isColumnEmptyByDefault(column: ManagementListColumn<Row>) {
  if (!column.defaultHiddenWhenEmpty || props.rows.length === 0) return false;
  return props.rows.every((row) => isEmptyLike(columnValue(row, column), column.placeholderValues));
}

function readStoredVisibleColumnKeys() {
  if (!props.storageKey || typeof window === "undefined") return null;
  try {
    const parsed = JSON.parse(window.localStorage.getItem(props.storageKey) || "null");
    return Array.isArray(parsed) ? parsed.filter((key): key is string => typeof key === "string") : null;
  } catch {
    return null;
  }
}

function writeStoredVisibleColumnKeys(keys: string[]) {
  if (!props.storageKey || typeof window === "undefined") return;
  window.localStorage.setItem(props.storageKey, JSON.stringify(keys));
}

function isColumnVisible(key: string) {
  return visibleColumnKeys.value.includes(key);
}

function setColumnVisible(key: string, visible: boolean) {
  const nextKeys = new Set(visibleColumnKeys.value);
  if (visible) nextKeys.add(key);
  else nextKeys.delete(key);
  props.columns.forEach((column) => {
    if (!column.hidable) nextKeys.add(column.key);
  });
  storedVisibleColumnKeys.value = props.columns
    .filter((column) => nextKeys.has(column.key))
    .map((column) => column.key);
  writeStoredVisibleColumnKeys(storedVisibleColumnKeys.value);
}

function restoreDefaultColumns() {
  storedVisibleColumnKeys.value = null;
  if (props.storageKey && typeof window !== "undefined") window.localStorage.removeItem(props.storageKey);
}

function clamp(value: number, minimum: number, maximum: number) {
  return Math.min(Math.max(value, minimum), Math.max(minimum, maximum));
}

function positionColumnSettingsMenu() {
  if (!columnSettingsOpen.value || !columnSettingsTriggerRef.value || !columnSettingsMenuRef.value) return;
  const triggerRect = columnSettingsTriggerRef.value.getBoundingClientRect();
  const menuHeight = columnSettingsMenuRef.value.offsetHeight;
  const menuWidth = Math.min(COLUMN_SETTINGS_MENU_WIDTH, window.innerWidth - VIEWPORT_MARGIN * 2);
  const left = clamp(triggerRect.right - menuWidth, VIEWPORT_MARGIN, window.innerWidth - menuWidth - VIEWPORT_MARGIN);
  const below = triggerRect.bottom + POPOVER_GAP;
  const above = triggerRect.top - menuHeight - POPOVER_GAP;
  const top =
    below + menuHeight <= window.innerHeight - VIEWPORT_MARGIN
      ? below
      : above >= VIEWPORT_MARGIN
        ? above
        : clamp(below, VIEWPORT_MARGIN, window.innerHeight - menuHeight - VIEWPORT_MARGIN);
  columnSettingsMenuStyle.value = {
    position: "fixed",
    top: `${Math.round(top)}px`,
    left: `${Math.round(left)}px`,
    visibility: "visible",
  };
}

function refreshColumnSettingsMenuPosition() {
  if (columnSettingsOpen.value) void nextTick(positionColumnSettingsMenu);
}

async function openColumnSettings() {
  closePageSizeMenu();
  columnSettingsOpen.value = true;
  columnSettingsMenuStyle.value = { position: "fixed", visibility: "hidden" };
  await nextTick();
  positionColumnSettingsMenu();
  columnSettingsMenuRef.value?.querySelector<HTMLInputElement>('input[type="checkbox"]')?.focus();
}

function closeColumnSettings(restoreFocus = false) {
  columnSettingsOpen.value = false;
  if (restoreFocus) void nextTick(() => columnSettingsTriggerRef.value?.focus());
}

function toggleColumnSettings() {
  if (columnSettingsOpen.value) closeColumnSettings(true);
  else openColumnSettings();
}

function updateSearch(event: Event) {
  emit("update:search", (event.target as HTMLInputElement).value);
}

function requestPage(page: number) {
  if (!props.pagination || page < 1 || page > pageCount.value || page === props.pagination.page) return;
  emit("page-change", { page, pageSize: props.pagination.pageSize });
}

function setPageSizeOptionRef(element: HTMLButtonElement | null, index: number) {
  if (element) pageSizeOptionRefs.value[index] = element;
}

function focusPageSizeOption(index: number) {
  pageSizeMenuActiveIndex.value = Math.max(0, Math.min(index, pageSizeOptions.value.length - 1));
  void nextTick(() => pageSizeOptionRefs.value[pageSizeMenuActiveIndex.value]?.focus());
}

function openPageSizeMenu(index = currentPageSizeIndex.value) {
  if (!pageSizeOptions.value.length) return;
  closeColumnSettings();
  pageSizeMenuOpen.value = true;
  focusPageSizeOption(index);
}

function closePageSizeMenu(restoreFocus = false) {
  pageSizeMenuOpen.value = false;
  pageSizeOptionRefs.value = [];
  if (restoreFocus) pageSizeTriggerRef.value?.focus();
}

function togglePageSizeMenu() {
  if (pageSizeMenuOpen.value) {
    closePageSizeMenu(true);
    return;
  }
  openPageSizeMenu();
}

function movePageSizeOption(offset: number) {
  if (!pageSizeMenuOpen.value) {
    openPageSizeMenu(offset > 0 ? currentPageSizeIndex.value : pageSizeOptions.value.length - 1);
    return;
  }
  const nextIndex =
    (pageSizeMenuActiveIndex.value + offset + pageSizeOptions.value.length) % pageSizeOptions.value.length;
  focusPageSizeOption(nextIndex);
}

function selectPageSize(pageSize: number) {
  closePageSizeMenu(true);
  if (!props.pagination || pageSize === props.pagination.pageSize) return;
  emit("page-change", { page: 1, pageSize });
}

function handlePageSizeMenuKeydown(event: KeyboardEvent) {
  if (event.key === "ArrowDown") {
    event.preventDefault();
    movePageSizeOption(1);
  } else if (event.key === "ArrowUp") {
    event.preventDefault();
    movePageSizeOption(-1);
  } else if (event.key === "Home") {
    event.preventDefault();
    focusPageSizeOption(0);
  } else if (event.key === "End") {
    event.preventDefault();
    focusPageSizeOption(pageSizeOptions.value.length - 1);
  } else if (event.key === "Escape") {
    event.preventDefault();
    closePageSizeMenu(true);
  }
}

function handlePageSizeDocumentPointerdown(event: PointerEvent) {
  if (pageSizeMenuOpen.value && pageSizeMenuRef.value && !pageSizeMenuRef.value.contains(event.target as Node)) {
    closePageSizeMenu();
  }
  const target = event.target as Node;
  const insideTools = columnSettingsToolsRef.value?.contains(target);
  const insideMenu = columnSettingsMenuRef.value?.contains(target);
  if (columnSettingsOpen.value && !insideTools && !insideMenu) closeColumnSettings();
}

function handleDocumentKeydown(event: KeyboardEvent) {
  if (columnSettingsOpen.value && event.key === "Escape") {
    event.preventDefault();
    closeColumnSettings(true);
  }
}

function handlePageSizeFocusout(event: FocusEvent) {
  const nextTarget = event.relatedTarget;
  if (nextTarget instanceof Node && pageSizeMenuRef.value?.contains(nextTarget)) return;
  closePageSizeMenu();
}

onMounted(() => {
  document.addEventListener("pointerdown", handlePageSizeDocumentPointerdown);
  document.addEventListener("keydown", handleDocumentKeydown);
  window.addEventListener("resize", refreshColumnSettingsMenuPosition);
  document.addEventListener("scroll", refreshColumnSettingsMenuPosition, true);
});
onBeforeUnmount(() => {
  document.removeEventListener("pointerdown", handlePageSizeDocumentPointerdown);
  document.removeEventListener("keydown", handleDocumentKeydown);
  window.removeEventListener("resize", refreshColumnSettingsMenuPosition);
  document.removeEventListener("scroll", refreshColumnSettingsMenuPosition, true);
});
</script>

<template>
  <section class="management-list" :class="{ 'has-card-list': hasCardSlot }" :aria-busy="loading">
    <div class="management-list-toolbar">
      <!--
        Search always stays in document flow to reserve height/width.
        Batch bar overlays it so selection never pushes the table down.
      -->
      <div class="management-list-toolbar-start">
        <label class="management-list-search" :class="{ 'is-covered': checkedRowKeys.length > 0 }">
          <i class="fa-solid fa-magnifying-glass" aria-hidden="true" />
          <input
            :value="search"
            type="search"
            :aria-label="resolvedSearchAriaLabel"
            :placeholder="resolvedSearchPlaceholder"
            :tabindex="checkedRowKeys.length ? -1 : undefined"
            @input="updateSearch"
          />
          <button
            v-if="search"
            type="button"
            :title="resolvedClearSearchAriaLabel"
            :aria-label="resolvedClearSearchAriaLabel"
            :tabindex="checkedRowKeys.length ? -1 : undefined"
            @click="emit('update:search', '')"
          >
            <i class="fa-solid fa-circle-xmark" aria-hidden="true" />
          </button>
        </label>
        <div v-if="checkedRowKeys.length" class="management-list-batch-bar" role="status" aria-live="polite">
          <span>{{ t("common.selectedCount", { n: checkedRowKeys.length }) }}</span>
          <slot name="batch-actions" :checked-row-keys="checkedRowKeys" />
          <button
            type="button"
            class="management-list-clear-selection"
            :aria-label="t('common.clearSelection')"
            @click="emit('update:checked-row-keys', [])"
          >
            {{ t("common.clearSelection") }}
          </button>
        </div>
      </div>
      <div class="management-list-toolbar-actions">
        <div v-if="$slots.filters" class="management-list-filters">
          <slot name="filters" />
        </div>
        <button
          v-if="!resetDisabled"
          class="management-list-reset"
          type="button"
          :aria-label="resolvedResetAriaLabel"
          @click="emit('reset')"
        >
          <i class="fa-solid fa-rotate-left" aria-hidden="true" />
          {{ resolvedResetLabel }}
        </button>
        <template v-if="showColumnSettings">
          <span class="management-list-toolbar-divider" aria-hidden="true" />
          <div ref="columnSettingsToolsRef" class="management-list-column-tools data-table-tools">
            <button
              ref="columnSettingsTriggerRef"
              class="data-table-column-button"
              type="button"
              :aria-label="t('common.columnSettings')"
              :title="t('common.columnSettings')"
              aria-haspopup="true"
              :aria-controls="columnSettingsId"
              :aria-expanded="columnSettingsOpen"
              @click="toggleColumnSettings"
            >
              <i class="fa-solid fa-table-columns" aria-hidden="true" />
            </button>
            <Teleport to="body">
              <div
                v-if="columnSettingsOpen"
                :id="columnSettingsId"
                ref="columnSettingsMenuRef"
                class="data-table-column-menu"
                role="group"
                :aria-label="t('common.tableColumnSettings')"
                :style="columnSettingsMenuStyle"
              >
                <div class="data-table-column-menu-title">{{ t("common.setVisibleColumns") }}</div>
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
                  <i
                    v-if="!column.hidable"
                    class="fa-solid fa-lock data-table-column-option-lock"
                    :aria-label="t('common.pinColumn')"
                  />
                </label>
                <button
                  class="data-table-column-reset"
                  type="button"
                  :aria-label="t('common.restoreDefaultColumns')"
                  @click="restoreDefaultColumns"
                >
                  {{ t("common.restoreDefaultColumns") }}
                </button>
              </div>
            </Teleport>
          </div>
        </template>
      </div>
    </div>

    <div class="management-list-content">
      <slot v-if="error && hasRows" name="error" :error="error" />

      <div v-if="showInitialLoading" class="management-list-loading" role="status" aria-live="polite">
        <span class="management-list-loading-line" />
        <span class="management-list-loading-line" />
        <span class="management-list-loading-line" />
      </div>

      <slot v-else-if="showErrorState" name="error" :error="error">
        <div class="management-list-state management-list-error" role="alert">{{ error }}</div>
      </slot>

      <slot v-else-if="showEmptyState" name="empty">
        <div class="management-list-state">{{ t("common.noData") }}</div>
      </slot>

      <template v-else-if="showList">
        <DataTable
          class="management-list-data-table"
          :rows="rows"
          :columns="columns"
          :row-key="rowKey"
          :sticky-left-keys="stickyLeftKeys"
          :sticky-right-keys="stickyRightKeys"
          :selected-row-key="selectedRowKey"
          :expanded-row-key="expandedRowKey"
          :selection-tone="selectionTone"
          :selectable="selectable"
          :checkable="checkable"
          :checked-row-keys="checkedRowKeys"
          :row-selection-label="rowSelectionLabel"
          :visible-column-keys="visibleColumnKeys"
          :sort-by="sortBy"
          :sort-order="sortOrder"
          @select-row="emit('select-row', $event)"
          @update:checked-row-keys="emit('update:checked-row-keys', $event)"
          @sort-change="emit('sort-change', $event)"
        >
          <template v-for="column in columns" :key="column.key" #[`cell-${column.key}`]="cell">
            <slot :name="`cell-${column.key}`" v-bind="cell">
              {{ displayCellValue(cell.value) }}
            </slot>
          </template>
          <template v-if="$slots['row-detail']" #row-detail="detail">
            <slot name="row-detail" v-bind="detail" />
          </template>
        </DataTable>

        <div v-if="hasCardSlot" class="management-list-card-list">
          <slot
            v-for="row in rows"
            name="card"
            :key="typeof rowKey === 'function' ? rowKey(row) : String(row[rowKey])"
            :row="row"
          />
        </div>
      </template>
    </div>

    <nav
      v-if="pagination && (showList || hasLoaded)"
      class="management-list-pagination"
      :aria-label="t('common.paginationAria')"
    >
      <span>{{
        t("common.paginationSummary", {
          total: pagination.total,
          page: pagination.page,
          pages: pageCount,
        })
      }}</span>
      <div v-if="showPaginationControls" class="management-list-pagination-controls">
        <div
          v-if="showPageSizeControl"
          ref="pageSizeMenuRef"
          class="management-list-page-size"
          @focusout="handlePageSizeFocusout"
        >
          <button
            ref="pageSizeTriggerRef"
            class="management-list-page-size-trigger"
            type="button"
            :aria-label="t('common.pageSizeAria', { n: pagination.pageSize })"
            aria-haspopup="listbox"
            :aria-controls="pageSizeMenuId"
            :aria-expanded="pageSizeMenuOpen"
            @click.stop="togglePageSizeMenu"
            @keydown.down.prevent="movePageSizeOption(1)"
            @keydown.up.prevent="movePageSizeOption(-1)"
            @keydown.esc.stop="closePageSizeMenu(true)"
          >
            <span>{{ t("common.pageSizeLabel", { n: pagination.pageSize }) }}</span>
            <i class="fa-solid fa-chevron-down" :class="{ open: pageSizeMenuOpen }" aria-hidden="true" />
          </button>
          <div
            v-if="pageSizeMenuOpen"
            :id="pageSizeMenuId"
            class="management-list-page-size-menu"
            role="listbox"
            :aria-label="t('common.pageSizeSelectAria')"
            @keydown="handlePageSizeMenuKeydown"
          >
            <button
              v-for="(option, index) in pageSizeOptions"
              :key="option"
              :ref="(element) => setPageSizeOptionRef(element as HTMLButtonElement | null, index)"
              class="management-list-page-size-option"
              type="button"
              role="option"
              :aria-selected="pagination.pageSize === option"
              :tabindex="index === pageSizeMenuActiveIndex ? 0 : -1"
              @click="selectPageSize(option)"
            >
              <span>{{ t("common.pageSizeOption", { n: option }) }}</span>
              <i v-if="pagination.pageSize === option" class="fa-solid fa-check" aria-hidden="true" />
            </button>
          </div>
        </div>
        <div v-if="showPageNavigation" class="management-list-page-navigation">
          <button
            class="management-list-pagination-button"
            type="button"
            :aria-label="t('common.prevPage')"
            :disabled="pagination.page <= 1"
            @click="requestPage(pagination.page - 1)"
          >
            <i class="fa-solid fa-chevron-left" aria-hidden="true" />
          </button>
          <span class="management-list-page-status">{{ pagination.page }} / {{ pageCount }}</span>
          <button
            v-for="pageNumber in visiblePageNumbers"
            :key="pageNumber"
            class="management-list-pagination-button management-list-page-number"
            :class="{ active: pagination.page === pageNumber }"
            type="button"
            :aria-label="t('common.gotoPage', { n: pageNumber })"
            :aria-current="pagination.page === pageNumber ? 'page' : undefined"
            @click="requestPage(pageNumber)"
          >
            {{ pageNumber }}
          </button>
          <button
            class="management-list-pagination-button"
            type="button"
            :aria-label="t('common.nextPage')"
            :disabled="pagination.page >= pageCount"
            @click="requestPage(pagination.page + 1)"
          >
            <i class="fa-solid fa-chevron-right" aria-hidden="true" />
          </button>
        </div>
      </div>
    </nav>
  </section>
</template>

<style scoped>
.management-list {
  position: relative;
  width: 100%;
  min-width: 0;
  max-width: 100%;
  display: flex;
  min-height: 0;
  flex-direction: column;
  container-name: management-list;
  container-type: inline-size;
}

.management-list-toolbar {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 16px;
  padding: 0 0 14px;
  border-bottom: 0;
}

.management-list-toolbar-start {
  position: relative;
  flex: 0 0 auto;
  width: min(280px, 100%);
  height: 40px;
  min-height: 40px;
  max-height: 40px;
}

.management-list-toolbar-actions {
  display: flex;
  min-width: 0;
  flex: 1 1 auto;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}

.management-list-search {
  position: relative;
  display: block;
  width: 100%;
  height: 40px;
}

.management-list-search.is-covered {
  visibility: hidden;
  pointer-events: none;
}

.management-list-batch-bar {
  position: absolute;
  top: 0;
  left: 0;
  z-index: 4;
  display: inline-flex;
  height: 40px;
  min-height: 40px;
  max-height: 40px;
  width: max-content;
  min-width: 100%;
  max-width: min(720px, calc(100cqw - 24px));
  align-items: center;
  gap: 6px;
  padding: 0 6px 0 12px;
  border: 1px solid #e2e8f0;
  border-radius: 999px;
  background: #fff;
  color: #334155;
  font-size: var(--aw-table-toolbar-size, 0.8125rem);
  font-weight: 700;
  white-space: nowrap;
  box-sizing: border-box;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
  overflow-x: auto;
  overflow-y: hidden;
  scrollbar-width: thin;
}

.management-list-batch-bar > span {
  color: #334155;
  font-size: 12px;
  font-weight: 700;
}

.management-list-batch-bar > button.management-list-clear-selection {
  min-height: 28px;
  height: 28px;
  padding: 0 10px;
  border: 1px solid transparent;
  border-radius: 999px;
  background: transparent;
  color: #64748b;
  font: inherit;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  flex: 0 0 auto;
}

.management-list-batch-bar > button.management-list-clear-selection:hover,
.management-list-batch-bar > button.management-list-clear-selection:focus-visible {
  outline: 0;
  background: #f8fafc;
  color: #0f172a;
}

/*
 * Slot content is compiled by the parent, so scoped rules must use :slotted().
 * Also keep unscoped utility classes in page CSS for reliability.
 */
.management-list-batch-bar :slotted(.management-list-batch-action),
.management-list-batch-bar :slotted(button) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-height: 28px;
  height: 28px;
  padding: 0 10px;
  border: 1px solid transparent;
  border-radius: 999px;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
  line-height: 1;
  cursor: pointer;
  box-sizing: border-box;
  flex: 0 0 auto;
  transition:
    background-color 0.15s ease,
    border-color 0.15s ease,
    color 0.15s ease,
    box-shadow 0.15s ease;
}

.management-list-batch-bar :slotted(.management-list-batch-action i),
.management-list-batch-bar :slotted(button i) {
  font-size: 11px;
}

.management-list-batch-bar :slotted(.management-list-batch-action.is-primary) {
  border-color: rgba(13, 148, 136, 0.35);
  background: #fff;
  color: #0d9488;
  box-shadow: none;
}

.management-list-batch-bar :slotted(.management-list-batch-action.is-primary:hover:not(:disabled)),
.management-list-batch-bar :slotted(.management-list-batch-action.is-primary:focus-visible:not(:disabled)) {
  outline: 0;
  border-color: rgba(13, 148, 136, 0.5);
  background: #ecfdf5;
  color: #0f766e;
  box-shadow: 0 0 0 3px rgba(13, 148, 136, 0.1);
}

.management-list-batch-bar :slotted(.management-list-batch-action.is-danger) {
  border-color: #fecaca;
  background: #fff;
  color: #b91c1c;
}

.management-list-batch-bar :slotted(.management-list-batch-action.is-danger:hover:not(:disabled)),
.management-list-batch-bar :slotted(.management-list-batch-action.is-danger:focus-visible:not(:disabled)) {
  outline: 0;
  border-color: #fca5a5;
  background: #fef2f2;
  color: #991b1b;
  box-shadow: 0 0 0 3px rgba(220, 38, 38, 0.12);
}

.management-list-batch-bar :slotted(.management-list-batch-action:disabled),
.management-list-batch-bar :slotted(button:disabled) {
  cursor: not-allowed;
  opacity: 0.55;
  box-shadow: none;
}

.management-list-search > i {
  position: absolute;
  top: 50%;
  left: 12px;
  color: #9ca3af;
  font-size: 0.75rem;
  pointer-events: none;
  transform: translateY(-50%);
}

.management-list-search input {
  width: 100%;
  height: 40px;
  min-height: 40px;
  padding: 0 40px 0 36px;
  border: 1px solid #e5e7eb;
  border-radius: 0.75rem;
  outline: none;
  background: #f9fafb;
  color: #111827;
  font: inherit;
  font-size: var(--aw-table-toolbar-size, 0.875rem);
  line-height: 1.25;
  /* Keep type=search for a11y, but hide native clear — we render our own. */
  -webkit-appearance: none;
  appearance: none;
}

.management-list-search input[type="search"]::-webkit-search-decoration,
.management-list-search input[type="search"]::-webkit-search-cancel-button,
.management-list-search input[type="search"]::-webkit-search-results-button,
.management-list-search input[type="search"]::-webkit-search-results-decoration {
  -webkit-appearance: none;
  appearance: none;
  display: none;
}

.management-list-search input:focus {
  border-color: rgba(16, 185, 129, 0.55);
  background: #fff;
  box-shadow: 0 0 0 3px rgba(16, 185, 129, 0.15);
}

.management-list-search button,
.management-list-reset,
.management-list-page-size-trigger,
.management-list-page-size-option,
.management-list-pagination-button {
  border: 0;
  font: inherit;
}

.management-list-search button {
  position: absolute;
  top: 0;
  right: 0;
  display: inline-flex;
  width: 40px;
  height: 40px;
  align-items: center;
  justify-content: center;
  background: transparent;
  color: #9ca3af;
}

.management-list-search button:hover {
  color: #4b5563;
}

.management-list-filters {
  display: flex;
  min-width: 0;
  flex: 0 1 auto;
  flex-wrap: nowrap;
  align-items: center;
  gap: 8px;
}

.management-list-reset {
  display: inline-flex;
  height: 40px;
  min-height: 40px;
  align-items: center;
  gap: 7px;
  padding: 8px 14px;
  border: 1px solid #e5e7eb;
  border-radius: 0.75rem;
  background: #f9fafb;
  color: #4b5563;
  font-size: var(--aw-table-toolbar-size, 0.875rem);
  font-weight: 600;
  line-height: 1.25;
}

.management-list-reset:hover {
  border-color: #d1d5db;
  background: #fff;
  color: #111827;
}

.management-list-toolbar-divider {
  width: 1px;
  height: 24px;
  margin: 0 2px;
  background: #e5e7eb;
}

.management-list-column-tools {
  position: relative;
  z-index: 10;
  display: flex;
  width: 40px;
  height: 40px;
  flex: 0 0 40px;
  justify-content: flex-end;
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
.data-table-column-button:focus-visible,
.data-table-column-button[aria-expanded="true"] {
  border-color: #d1d5db;
  background: #fff;
  color: #111827;
  outline: none;
  box-shadow: 0 2px 8px rgba(15, 23, 42, 0.06);
}

.data-table-column-menu {
  position: fixed;
  /* Above sticky topbar (2500); stay below modal stack (3000). */
  z-index: 2900;
  width: 218px;
  max-width: calc(100vw - 16px);
  max-height: calc(100vh - 16px);
  overflow-y: auto;
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
  outline: none;
}

.management-list-content {
  position: relative;
  width: 100%;
  min-height: 0;
  max-width: 100%;
  flex: 1 1 auto;
  overflow: hidden;
  border: 1px solid #f3f4f6;
  border-radius: 1.25rem 1.25rem 0 0;
  background: #fff;
  box-shadow: 0 4px 20px -4px rgba(0, 0, 0, 0.04);
}

.management-list-content:not(:has(+ .management-list-pagination)) {
  border-radius: 1.25rem;
}

.management-list-data-table,
.management-list-data-table :deep(.data-table-shell) {
  height: auto;
  min-height: 0;
}

.management-list-data-table {
  display: flex;
  flex-direction: column;
}

.management-list-data-table :deep(.data-table-shell) {
  display: flex;
  flex-direction: column;
}

.management-list-data-table :deep(.data-table-scroll) {
  min-height: 0;
  flex: 1 1 auto;
  overflow: auto;
}

.management-list-data-table :deep(.data-table th:first-child),
.management-list-data-table :deep(.data-table td[data-column-key]:first-child:not([data-column-key="actions"])) {
  padding-left: 16px;
}

.management-list-data-table :deep(.data-table th:last-child),
.management-list-data-table :deep(.data-table td[data-column-key]:last-child:not([data-column-key="actions"])) {
  padding-right: 16px;
}

.management-list-loading,
.management-list-state {
  display: grid;
  min-height: 220px;
  place-content: center;
  gap: 12px;
  padding: 24px 20px;
  background: #fff;
  color: #6b7280;
  text-align: center;
}

.management-list-loading-line {
  display: block;
  width: min(420px, 70vw);
  height: 14px;
  border-radius: 0.5rem;
  background: #f3f4f6;
}

.management-list-error {
  color: #e11d48;
}

.management-list-card-list {
  display: none;
}

.management-list-pagination {
  display: flex;
  min-height: 56px;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 0 16px;
  border: 1px solid #f3f4f6;
  border-top: 0;
  border-radius: 0 0 1.25rem 1.25rem;
  background: #fff;
  color: var(--aw-table-header-color, #6b7280);
  font-size: var(--aw-table-header-size, 0.85rem);
  box-shadow: 0 4px 20px -4px rgba(0, 0, 0, 0.04);
}

.management-list-pagination-controls,
.management-list-page-navigation {
  display: flex;
  align-items: center;
  gap: 4px;
}

.management-list-page-size-trigger,
.management-list-pagination-button {
  min-width: 32px;
  min-height: 32px;
  padding: 0 8px;
  border: 0;
  border-radius: 0.5rem;
  background: transparent;
  color: #6b7280;
}

.management-list-page-size {
  position: relative;
}

.management-list-page-size-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-width: 100px;
  text-align: left;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.management-list-page-size-trigger:hover,
.management-list-page-size-trigger:focus-visible,
.management-list-page-size-trigger[aria-expanded="true"] {
  background: #f3f4f6;
  color: #111827;
  outline: none;
}

.management-list-page-size-trigger i {
  color: #9ca3af;
  font-size: 0.75rem;
  transition: transform 160ms ease;
}

.management-list-page-status {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  clip-path: inset(50%);
  white-space: nowrap;
}

.management-list-page-number.active,
.management-list-pagination-button:hover:not(:disabled) {
  background: #f3f4f6;
  color: #111827;
  font-weight: 700;
}

.management-list-page-size-trigger i.open {
  transform: rotate(180deg);
}

.management-list-page-size-menu {
  position: absolute;
  right: 0;
  bottom: calc(100% + 8px);
  z-index: 10;
  width: 100%;
  padding: 6px;
  border: 1px solid #f3f4f6;
  border-radius: 0.75rem;
  background: #fff;
  box-shadow: 0 14px 34px -12px rgba(15, 23, 42, 0.2);
}

.management-list-page-size-option {
  position: relative;
  display: flex;
  width: 100%;
  min-height: 40px;
  align-items: center;
  justify-content: flex-start;
  padding: 7px 30px 7px 10px;
  border: 0;
  border-radius: 0.5rem;
  background: transparent;
  color: #4b5563;
  font-size: 0.8125rem;
  text-align: left;
  white-space: nowrap;
}

.management-list-page-size-option:hover,
.management-list-page-size-option:focus-visible,
.management-list-page-size-option[aria-selected="true"] {
  background: #ecfdf5;
  color: #059669;
  outline: none;
}

.management-list-page-size-option[aria-selected="true"] {
  font-weight: 700;
}

.management-list-page-size-option i {
  position: absolute;
  right: 10px;
  color: #10b981;
  font-size: 0.75rem;
}

.management-list-pagination-button:disabled {
  cursor: not-allowed;
  opacity: 0.42;
}

@media (max-width: 900px) {
  .management-list-toolbar {
    align-items: stretch;
    flex-direction: column;
  }

  .management-list-toolbar-start {
    width: 100%;
  }

  .management-list-batch-bar {
    max-width: 100%;
  }

  .management-list-toolbar-actions {
    width: 100%;
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  .management-list-filters {
    flex: 1 1 auto;
  }
}

@container management-list (max-width: 760px) {
  .management-list-toolbar {
    align-items: stretch;
    flex-direction: column;
  }

  .management-list-toolbar-start {
    width: 100%;
  }

  .management-list-batch-bar {
    max-width: 100%;
  }

  .management-list-toolbar-actions {
    width: 100%;
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  .management-list-filters {
    flex: 1 1 auto;
    overflow-x: auto;
    scrollbar-width: thin;
  }
}

@container management-list (max-width: 520px) {
  .management-list-toolbar-actions,
  .management-list-filters {
    width: 100%;
    flex-direction: column;
    align-items: stretch;
  }

  .management-list-filters {
    overflow-x: visible;
  }

  .management-list-filters :deep(.management-segmented-filter),
  .management-list-reset {
    width: 100%;
  }

  .management-list-reset {
    justify-content: center;
  }

  .management-list-toolbar-divider,
  .management-list-column-tools {
    display: none;
  }
}

@media (max-width: 640px) {
  .management-list.has-card-list .management-list-data-table {
    display: none;
  }

  .management-list.has-card-list .management-list-card-list {
    display: grid;
    gap: 12px;
    padding: 12px;
    background: #fff;
  }

  .management-list-pagination {
    align-items: flex-start;
    flex-wrap: wrap;
  }

  .management-list-pagination-controls {
    flex-wrap: wrap;
    justify-content: flex-end;
  }
}
</style>
