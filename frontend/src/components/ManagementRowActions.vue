<script lang="ts">
export type ManagementRowActionTone = "default" | "primary" | "danger";

export interface ManagementRowAction {
  key: string;
  label: string;
  shortLabel?: string;
  icon: string;
  tone?: ManagementRowActionTone;
  disabled?: boolean;
  loading?: boolean;
  disabledReason?: string;
}

let managementRowActionsInstanceCount = 0;
</script>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";

const props = withDefaults(
  defineProps<{
    /** Direct icon buttons (legacy). Prefer empty + menuActions for menu-only rows. */
    primaryActions?: ManagementRowAction[];
    menuActions?: ManagementRowAction[];
    menuLabel?: string;
  }>(),
  {
    primaryActions: () => [],
    menuActions: () => [],
    menuLabel: "更多操作",
  },
);

const emit = defineEmits<{
  action: [key: string];
}>();

const MENU_WIDTH = 208;
const VIEWPORT_MARGIN = 8;
const POPOVER_GAP = 8;
const instanceId = ++managementRowActionsInstanceCount;
const menuId = `management-row-actions-menu-${instanceId}`;
const rootRef = ref<HTMLElement | null>(null);
const triggerRef = ref<HTMLButtonElement | null>(null);
const menuRef = ref<HTMLElement | null>(null);
const menuOpen = ref(false);
const menuPlacement = ref<"top" | "bottom">("bottom");
const menuStyle = ref<Record<string, string>>({ position: "fixed", visibility: "hidden" });
const menuItemRefs = ref<Array<HTMLButtonElement | null>>([]);
const activeMenuIndex = ref(-1);
// Strict menu-only when primaryActions is empty: never promote a sole menu item to a direct button
// (ZKL-33). Sole-promote only applies when callers still expose primary actions.
const promoteSoleMenuAction = computed(
  () => props.primaryActions.length > 0 && props.menuActions.length === 1 && props.primaryActions.length <= 2,
);
const hasMenu = computed(() => props.menuActions.length > 0 && !promoteSoleMenuAction.value);
const visiblePrimaryActions = computed(() => {
  if (promoteSoleMenuAction.value) return [...props.primaryActions, props.menuActions[0]].slice(0, 3);
  return props.primaryActions.slice(0, hasMenu.value ? 2 : 3);
});
const isMenuOnly = computed(() => hasMenu.value && visiblePrimaryActions.value.length === 0);
const actionContainerStyle = computed(() => {
  const actionCount = visiblePrimaryActions.value.length + (hasMenu.value ? 1 : 0);
  // Menu-only rows use a lighter 36px control so the sticky actions rail feels less heavy.
  const unit = isMenuOnly.value ? 36 : 44;
  const width = Math.max(unit, actionCount * unit + Math.max(0, actionCount - 1) * 4);
  return { width: `${width}px`, flexBasis: `${width}px` };
});
const enabledMenuIndexes = computed(() =>
  props.menuActions.map((action, index) => ({ action, index })).filter(({ action }) => !isUnavailable(action)).map(({ index }) => index),
);

function isUnavailable(action: ManagementRowAction) {
  return Boolean(action.disabled || action.loading);
}

function actionTitle(action: ManagementRowAction) {
  if (isUnavailable(action) && action.disabledReason) return action.disabledReason;
  return action.label;
}

function actionTone(action: ManagementRowAction) {
  return `tone-${action.tone || "default"}`;
}

function actionShortLabel(action: ManagementRowAction) {
  const label = action.shortLabel?.trim() || action.label.trim();
  return Array.from(label).slice(0, 4).join("");
}

function clamp(value: number, minimum: number, maximum: number) {
  return Math.min(Math.max(value, minimum), Math.max(minimum, maximum));
}

function positionMenu() {
  if (!menuOpen.value || !triggerRef.value || !menuRef.value) return;

  const triggerRect = triggerRef.value.getBoundingClientRect();
  const measuredWidth = menuRef.value.offsetWidth || MENU_WIDTH;
  const measuredHeight = menuRef.value.offsetHeight || props.menuActions.length * 40 + 12;
  const availableWidth = Math.max(0, window.innerWidth - VIEWPORT_MARGIN * 2);
  const menuWidth = Math.min(measuredWidth, availableWidth);
  const left = clamp(triggerRect.right - menuWidth, VIEWPORT_MARGIN, window.innerWidth - menuWidth - VIEWPORT_MARGIN);
  const viewportTop = VIEWPORT_MARGIN;
  const viewportBottom = Math.max(viewportTop, window.innerHeight - VIEWPORT_MARGIN);
  const belowTop = triggerRect.bottom + POPOVER_GAP;
  const aboveBottom = triggerRect.top - POPOVER_GAP;
  const spaceBelow = Math.max(0, viewportBottom - belowTop);
  const spaceAbove = Math.max(0, aboveBottom - viewportTop);
  let top: number;
  let availableHeight: number;

  if (measuredHeight <= spaceBelow) {
    top = belowTop;
    availableHeight = spaceBelow;
    menuPlacement.value = "bottom";
  } else if (measuredHeight <= spaceAbove) {
    top = aboveBottom - measuredHeight;
    availableHeight = spaceAbove;
    menuPlacement.value = "top";
  } else if (spaceBelow >= spaceAbove && spaceBelow > 0) {
    top = belowTop;
    availableHeight = spaceBelow;
    menuPlacement.value = "bottom";
  } else if (spaceAbove > 0) {
    top = viewportTop;
    availableHeight = spaceAbove;
    menuPlacement.value = "top";
  } else {
    top = viewportTop;
    availableHeight = Math.max(0, viewportBottom - viewportTop);
    menuPlacement.value = triggerRect.top > window.innerHeight / 2 ? "top" : "bottom";
  }

  menuStyle.value = {
    position: "fixed",
    top: `${Math.round(top)}px`,
    left: `${Math.round(left)}px`,
    maxWidth: `${availableWidth}px`,
    maxHeight: `${Math.round(availableHeight)}px`,
    visibility: "visible",
  };
}

function refreshMenuPosition() {
  if (menuOpen.value) void nextTick(positionMenu);
}

async function openMenu() {
  if (!hasMenu.value) return;
  activeMenuIndex.value = enabledMenuIndexes.value[0] ?? -1;
  menuItemRefs.value = [];
  menuOpen.value = true;
  menuStyle.value = { position: "fixed", visibility: "hidden" };
  await nextTick();
  positionMenu();
  menuItemRefs.value[activeMenuIndex.value]?.focus();
}

function closeMenu(restoreFocus = false) {
  menuOpen.value = false;
  activeMenuIndex.value = -1;
  menuItemRefs.value = [];
  if (restoreFocus) void nextTick(() => triggerRef.value?.focus());
}

function toggleMenu() {
  if (menuOpen.value) closeMenu(true);
  else void openMenu();
}

function selectAction(action: ManagementRowAction) {
  if (isUnavailable(action)) return;
  closeMenu();
  emit("action", action.key);
}

function setMenuItemRef(element: HTMLButtonElement | null, index: number) {
  menuItemRefs.value[index] = element;
}

function menuItemTabIndex(action: ManagementRowAction, index: number) {
  return !isUnavailable(action) && activeMenuIndex.value === index ? 0 : -1;
}

function activateMenuIndex(action: ManagementRowAction, index: number) {
  if (!isUnavailable(action)) activeMenuIndex.value = index;
}

function focusMenuIndex(index: number) {
  if (!enabledMenuIndexes.value.includes(index)) return;
  activeMenuIndex.value = index;
  void nextTick(() => menuItemRefs.value[index]?.focus());
}

function moveMenuFocus(offset: number) {
  const enabledIndexes = enabledMenuIndexes.value;
  if (!enabledIndexes.length) return;
  const currentPosition = enabledIndexes.indexOf(activeMenuIndex.value);
  const nextPosition = currentPosition < 0 ? (offset > 0 ? 0 : enabledIndexes.length - 1) : (currentPosition + offset + enabledIndexes.length) % enabledIndexes.length;
  focusMenuIndex(enabledIndexes[nextPosition]);
}

function handleMenuKeydown(event: KeyboardEvent) {
  if (event.key === "ArrowDown") {
    event.preventDefault();
    moveMenuFocus(1);
  } else if (event.key === "ArrowUp") {
    event.preventDefault();
    moveMenuFocus(-1);
  } else if (event.key === "Home") {
    event.preventDefault();
    focusMenuIndex(enabledMenuIndexes.value[0] ?? -1);
  } else if (event.key === "End") {
    event.preventDefault();
    focusMenuIndex(enabledMenuIndexes.value.at(-1) ?? -1);
  }
}

function handleDocumentPointerdown(event: PointerEvent) {
  if (!menuOpen.value || !(event.target instanceof Node)) return;
  if (rootRef.value?.contains(event.target) || menuRef.value?.contains(event.target)) return;
  closeMenu();
}

function handleDocumentKeydown(event: KeyboardEvent) {
  if (!menuOpen.value || event.key !== "Escape") return;
  event.preventDefault();
  closeMenu(true);
}

onMounted(() => {
  document.addEventListener("pointerdown", handleDocumentPointerdown);
  document.addEventListener("keydown", handleDocumentKeydown);
  document.addEventListener("scroll", refreshMenuPosition, true);
  window.addEventListener("resize", refreshMenuPosition);
});

onBeforeUnmount(() => {
  document.removeEventListener("pointerdown", handleDocumentPointerdown);
  document.removeEventListener("keydown", handleDocumentKeydown);
  document.removeEventListener("scroll", refreshMenuPosition, true);
  window.removeEventListener("resize", refreshMenuPosition);
});
</script>

<template>
  <div
    ref="rootRef"
    class="management-row-actions"
    :class="{ 'is-menu-only': isMenuOnly }"
    :style="actionContainerStyle"
    @click.stop
  >
    <button
      v-for="actionItem in visiblePrimaryActions"
      :key="actionItem.key"
      class="management-row-action-button"
      :class="[actionTone(actionItem), { 'is-loading': actionItem.loading }]"
      type="button"
      data-action-kind="primary"
      :data-action-key="actionItem.key"
      :title="actionTitle(actionItem)"
      :aria-label="actionItem.label"
      :aria-busy="Boolean(actionItem.loading)"
      :disabled="isUnavailable(actionItem)"
      @click="selectAction(actionItem)"
    >
      <i :class="actionItem.loading ? 'fa-solid fa-spinner fa-spin' : actionItem.icon" aria-hidden="true" />
      <span>{{ actionShortLabel(actionItem) }}</span>
    </button>

    <button
      v-if="hasMenu"
      ref="triggerRef"
      class="management-row-action-button management-row-actions-trigger"
      type="button"
      :title="menuLabel"
      :aria-label="menuLabel"
      aria-haspopup="menu"
      :aria-controls="menuId"
      :aria-expanded="menuOpen"
      @click="toggleMenu"
    >
      <i class="fa-solid fa-ellipsis" aria-hidden="true" />
      <span>更多</span>
    </button>

    <Teleport to="body">
      <div
        v-if="menuOpen && hasMenu"
        :id="menuId"
        ref="menuRef"
        class="management-row-actions-menu"
        role="menu"
        :aria-label="menuLabel"
        :data-placement="menuPlacement"
        :style="menuStyle"
        @click.stop
        @keydown="handleMenuKeydown"
      >
        <button
          v-for="(actionItem, index) in menuActions"
          :key="actionItem.key"
          :ref="(element) => setMenuItemRef(element as HTMLButtonElement | null, index)"
          class="management-row-actions-menu-item"
          :class="[actionTone(actionItem), { 'is-loading': actionItem.loading }]"
          type="button"
          role="menuitem"
          :data-action-key="actionItem.key"
          :title="actionTitle(actionItem)"
          :aria-label="actionItem.label"
          :aria-disabled="isUnavailable(actionItem)"
          :aria-busy="Boolean(actionItem.loading)"
          :disabled="isUnavailable(actionItem)"
          :tabindex="menuItemTabIndex(actionItem, index)"
          @click="selectAction(actionItem)"
          @focus="activateMenuIndex(actionItem, index)"
        >
          <i :class="actionItem.loading ? 'fa-solid fa-spinner fa-spin' : actionItem.icon" aria-hidden="true" />
          <span>{{ actionItem.label }}</span>
        </button>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.management-row-actions {
  display: flex;
  width: 140px;
  height: 44px;
  flex: 0 0 140px;
  align-items: center;
  justify-content: center;
  gap: 4px;
  margin: 0 auto;
  padding: 0;
  box-sizing: border-box;
}

.management-row-action-button {
  display: inline-flex;
  width: 44px;
  height: 44px;
  flex: 0 0 auto;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  padding: 0;
  border: 0;
  border-radius: 0.5rem;
  background: transparent;
  color: #6b7280;
  font: inherit;
  font-size: 0.75rem;
  cursor: pointer;
  transition: background-color 0.2s ease, color 0.2s ease;
}

.management-row-actions.is-menu-only {
  justify-content: flex-end;
}

.management-row-actions.is-menu-only .management-row-actions-trigger > span {
  display: none;
}

.management-row-actions.is-menu-only .management-row-action-button {
  width: 36px;
  height: 36px;
  color: #9ca3af;
  border-radius: 0.5rem;
}

.management-row-actions.is-menu-only .management-row-action-button:hover:not(:disabled),
.management-row-actions.is-menu-only .management-row-action-button:focus-visible {
  background: #f3f4f6;
  color: #4b5563;
}

.management-row-action-button > span {
  display: block;
  width: 100%;
  overflow: hidden;
  font-size: 8px;
  font-weight: 600;
  line-height: 11px;
  text-align: center;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.management-row-action-button:hover:not(:disabled),
.management-row-action-button:focus-visible {
  background: #f3f4f6;
  color: #111827;
}

.management-row-action-button.tone-primary:hover:not(:disabled),
.management-row-action-button.tone-primary:focus-visible {
  background: #ecfdf5;
  color: #059669;
}

.management-row-action-button.tone-danger:hover:not(:disabled),
.management-row-action-button.tone-danger:focus-visible {
  background: #fff1f2;
  color: #e11d48;
}

.management-row-action-button:focus-visible,
.management-row-actions-menu-item:focus-visible {
  outline: 2px solid rgba(16, 185, 129, 0.45);
  outline-offset: -2px;
}

.management-row-action-button:disabled {
  cursor: not-allowed;
  opacity: 0.62;
}

.management-row-action-button.is-loading:disabled {
  cursor: progress;
}

.management-row-actions-menu {
  /* Above sticky topbar (2500) so menus near the viewport top stay visible. */
  z-index: 2900;
  display: grid;
  width: 208px;
  gap: 2px;
  padding: 6px;
  overflow-y: auto;
  border: 1px solid #f3f4f6;
  border-radius: 0.75rem;
  box-sizing: border-box;
  background: #fff;
  box-shadow: 0 16px 44px rgba(15, 23, 42, 0.1);
}

.management-row-actions-menu-item {
  display: flex;
  width: 100%;
  min-height: 36px;
  align-items: center;
  gap: 10px;
  padding: 0 12px;
  border: 0;
  border-radius: 0.5rem;
  background: transparent;
  color: #4b5563;
  font: inherit;
  font-size: 0.8125rem;
  text-align: left;
  cursor: pointer;
}

.management-row-actions-menu-item i {
  width: 16px;
  color: #6b7280;
  text-align: center;
}

.management-row-actions-menu-item:hover:not(:disabled),
.management-row-actions-menu-item:focus-visible {
  background: #f3f4f6;
}

.management-row-actions-menu-item.tone-primary:hover:not(:disabled),
.management-row-actions-menu-item.tone-primary:focus-visible {
  background: #eff6ff;
  color: #2563eb;
}

.management-row-actions-menu-item.tone-danger:hover:not(:disabled),
.management-row-actions-menu-item.tone-danger:focus-visible {
  background: #fff1f2;
  color: #e11d48;
}

.management-row-actions-menu-item.tone-primary:hover:not(:disabled) i,
.management-row-actions-menu-item.tone-primary:focus-visible i {
  color: #2563eb;
}

.management-row-actions-menu-item.tone-danger:hover:not(:disabled) i,
.management-row-actions-menu-item.tone-danger:focus-visible i {
  color: #e11d48;
}

.management-row-actions-menu-item:disabled {
  cursor: not-allowed;
  opacity: 0.52;
}

.management-row-actions-menu-item.is-loading:disabled {
  cursor: progress;
}
</style>
