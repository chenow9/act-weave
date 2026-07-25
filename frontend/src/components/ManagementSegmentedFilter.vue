<script lang="ts">
export type ManagementSegmentedFilterOption = {
  value: string;
  label: string;
  disabled?: boolean;
};

let managementFilterInstanceCount = 0;
</script>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";

const props = defineProps<{
  modelValue: string;
  options: ManagementSegmentedFilterOption[];
  ariaLabel: string;
}>();

const emit = defineEmits<{ "update:modelValue": [value: string] }>();
const instanceId = ++managementFilterInstanceCount;
const menuId = `management-filter-menu-${instanceId}`;
const rootRef = ref<HTMLElement | null>(null);
const triggerRef = ref<HTMLButtonElement | null>(null);
const menuRef = ref<HTMLElement | null>(null);
const optionRefs = ref<HTMLButtonElement[]>([]);
const menuOpen = ref(false);
const menuStyle = ref<Record<string, string>>({ position: "fixed", visibility: "hidden" });
const enabledIndexes = computed(() =>
  props.options.map((option, index) => ({ option, index })).filter(({ option }) => !option.disabled).map(({ index }) => index),
);
const selectedIndex = computed(() => props.options.findIndex((option) => !option.disabled && option.value === props.modelValue));
const selectedOption = computed(() => props.options[selectedIndex.value] || props.options.find((option) => !option.disabled));

function setOptionRef(element: HTMLButtonElement | null, index: number) {
  if (element) optionRefs.value[index] = element;
}

function positionMenu() {
  if (!menuOpen.value || !triggerRef.value || !menuRef.value) return;
  const triggerRect = triggerRef.value.getBoundingClientRect();
  const viewportMargin = 8;
  const gap = 7;
  const width = Math.max(triggerRect.width, menuRef.value.offsetWidth || 160);
  const height = menuRef.value.offsetHeight;
  const left = Math.min(Math.max(viewportMargin, triggerRect.left), Math.max(viewportMargin, window.innerWidth - width - viewportMargin));
  const below = triggerRect.bottom + gap;
  const above = triggerRect.top - height - gap;
  const top = below + height <= window.innerHeight - viewportMargin || above < viewportMargin ? below : above;
  menuStyle.value = {
    position: "fixed",
    top: `${Math.round(top)}px`,
    left: `${Math.round(left)}px`,
    minWidth: `${Math.round(width)}px`,
    visibility: "visible",
  };
}

function refreshMenuPosition() {
  if (menuOpen.value) void nextTick(positionMenu);
}

async function openMenu() {
  if (!enabledIndexes.value.length) return;
  menuOpen.value = true;
  menuStyle.value = { position: "fixed", visibility: "hidden" };
  await nextTick();
  positionMenu();
  optionRefs.value[selectedIndex.value >= 0 ? selectedIndex.value : enabledIndexes.value[0]]?.focus();
}

function closeMenu(restoreFocus = false) {
  menuOpen.value = false;
  if (restoreFocus) void nextTick(() => triggerRef.value?.focus());
}

function toggleMenu() {
  if (menuOpen.value) closeMenu(true);
  else void openMenu();
}

function choose(index: number, moveFocus = false, close = false) {
  const option = props.options[index];
  if (!option || option.disabled) return;
  emit("update:modelValue", option.value);
  if (close) {
    closeMenu(true);
  } else if (moveFocus) {
    void nextTick(() => optionRefs.value[index]?.focus());
  }
}

function move(index: number, offset: number) {
  if (!enabledIndexes.value.length) return;
  const currentPosition = enabledIndexes.value.indexOf(index);
  const start = currentPosition >= 0 ? currentPosition : 0;
  const nextPosition = (start + offset + enabledIndexes.value.length) % enabledIndexes.value.length;
  choose(enabledIndexes.value[nextPosition], true);
}

function handleOptionKeydown(event: KeyboardEvent, index: number) {
  if (event.key === "ArrowRight" || event.key === "ArrowDown") {
    event.preventDefault();
    move(index, 1);
  } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
    event.preventDefault();
    move(index, -1);
  } else if (event.key === "Home") {
    event.preventDefault();
    choose(enabledIndexes.value[0], true);
  } else if (event.key === "End") {
    event.preventDefault();
    choose(enabledIndexes.value.at(-1) ?? -1, true);
  } else if (event.key === "Escape") {
    event.preventDefault();
    closeMenu(true);
  } else if (event.key === "Tab") {
    closeMenu();
  }
}

function handleTriggerKeydown(event: KeyboardEvent) {
  if (["ArrowDown", "ArrowUp", "Enter", " "].includes(event.key)) {
    event.preventDefault();
    void openMenu();
  }
}

function handleDocumentPointerdown(event: PointerEvent) {
  if (!menuOpen.value || !(event.target instanceof Node) || rootRef.value?.contains(event.target)) return;
  closeMenu();
}

function handleDocumentKeydown(event: KeyboardEvent) {
  if (menuOpen.value && event.key === "Escape") {
    event.preventDefault();
    closeMenu(true);
  }
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
  <div ref="rootRef" class="management-segmented-filter" :class="{ open: menuOpen }">
    <button
      ref="triggerRef"
      class="management-filter-trigger"
      type="button"
      :aria-label="ariaLabel"
      aria-haspopup="listbox"
      :aria-controls="menuId"
      :aria-expanded="menuOpen"
      @click="toggleMenu"
      @keydown="handleTriggerKeydown"
    >
      <span>{{ selectedOption?.label || "请选择" }}</span>
      <i class="fa-solid fa-chevron-down" aria-hidden="true" />
    </button>

    <div
      v-show="menuOpen"
      :id="menuId"
      ref="menuRef"
      class="management-filter-menu"
      role="listbox"
      :aria-label="ariaLabel"
      :style="menuStyle"
    >
      <button
        v-for="(option, index) in options"
        :key="option.value"
        :ref="(element) => setOptionRef(element as HTMLButtonElement | null, index)"
        type="button"
        role="option"
        :value="option.value"
        :class="{ active: modelValue === option.value }"
        :aria-selected="modelValue === option.value"
        :tabindex="modelValue === option.value || (selectedIndex < 0 && enabledIndexes[0] === index) ? 0 : -1"
        :disabled="option.disabled"
        @click="choose(index, false, true)"
        @keydown="handleOptionKeydown($event, index)"
      >
        <span>{{ option.label }}</span>
        <i v-if="modelValue === option.value" class="fa-solid fa-check" aria-hidden="true" />
      </button>
    </div>
  </div>
</template>

<style scoped>
.management-segmented-filter {
  position: relative;
  display: inline-flex;
  min-width: 134px;
  height: 40px;
  flex: 0 0 auto;
}

.management-filter-trigger {
  display: flex;
  width: 100%;
  height: 40px;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 0 12px;
  border: 1px solid #e5e7eb;
  border-radius: 0.75rem;
  background: #f9fafb;
  color: #4b5563;
  font: inherit;
  font-size: var(--aw-table-toolbar-size, 0.875rem);
  font-weight: 600;
  text-align: left;
  white-space: nowrap;
}

.management-filter-trigger:hover {
  border-color: #d1d5db;
  color: #111827;
}

.management-filter-trigger:focus-visible,
.management-segmented-filter.open .management-filter-trigger {
  border-color: rgba(16, 185, 129, 0.55);
  outline: 0;
  background: #fff;
  box-shadow: 0 0 0 3px rgba(16, 185, 129, 0.15);
}

.management-filter-trigger i {
  color: #9ca3af;
  font-size: 0.5rem;
  transition: transform 0.15s ease;
}

.management-segmented-filter.open .management-filter-trigger i {
  transform: rotate(180deg);
}

.management-filter-menu {
  /* Above sticky topbar (2500); stay below modal stack (3000). */
  z-index: 2900;
  display: grid;
  width: max-content;
  max-width: calc(100vw - 16px);
  max-height: min(260px, calc(100vh - 16px));
  gap: 2px;
  overflow: auto;
  padding: 6px;
  border: 1px solid #f3f4f6;
  border-radius: 0.75rem;
  background: #fff;
  box-shadow: 0 16px 44px rgba(15, 23, 42, 0.1), 0 2px 7px rgba(15, 23, 42, 0.04);
}

.management-filter-menu button {
  display: flex;
  width: 100%;
  min-width: 122px;
  min-height: 36px;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 0 10px;
  border: 0;
  border-radius: 0.5rem;
  background: transparent;
  color: #4b5563;
  font: inherit;
  font-size: 0.8125rem;
  font-weight: 600;
  text-align: left;
  white-space: nowrap;
}

.management-filter-menu button:hover:not(:disabled),
.management-filter-menu button:focus-visible,
.management-filter-menu button.active {
  outline: 0;
  background: #ecfdf5;
  color: #059669;
  box-shadow: none;
}

.management-filter-menu button:disabled {
  cursor: not-allowed;
  opacity: 0.48;
}

.management-filter-menu button i {
  color: #10b981;
  font-size: 0.625rem;
}
</style>
