<script setup lang="ts">
import { computed, ref, toRef } from "vue";
import { useI18n } from "vue-i18n";

import { useModalFocus } from "../composables/useModalFocus";

const props = withDefaults(
  defineProps<{
    open?: boolean;
    title: string;
    eyebrow?: string;
    description?: string;
    icon?: string;
    size?: "sm" | "md" | "lg";
    tone?: "default" | "danger";
    closeAriaLabel?: string;
    closeDisabled?: boolean;
    closeOnBackdrop?: boolean;
    ariaLabel?: string;
    testid?: string;
    backdropClass?: string;
    cardClass?: string;
    footerClass?: string;
    initialFocusSelector?: string;
  }>(),
  {
    open: true,
    eyebrow: "",
    description: "",
    icon: "",
    size: "md",
    tone: "default",
    closeAriaLabel: "",
    closeDisabled: false,
    closeOnBackdrop: true,
    ariaLabel: "",
    testid: "",
    backdropClass: "",
    cardClass: "",
    footerClass: "",
    initialFocusSelector: "[data-modal-initial-focus]",
  },
);

const emit = defineEmits<{
  close: [source: "header" | "backdrop" | "escape"];
  backdrop: [];
}>();

const { t } = useI18n();
const cardRef = ref<HTMLElement | null>(null);
const titleId = `management-dialog-title-${Math.random().toString(36).slice(2, 9)}`;
const resolvedCloseLabel = computed(() => props.closeAriaLabel || t("common.close"));
const resolvedAriaLabel = computed(() => props.ariaLabel || props.title);

useModalFocus({
  visible: toRef(props, "open"),
  modalRef: cardRef,
  onClose: () => requestClose("escape"),
  initialFocusSelector: props.initialFocusSelector,
});

function requestClose(source: "header" | "backdrop" | "escape") {
  if (props.closeDisabled) return;
  emit("close", source);
}

function onBackdropClick() {
  if (!props.closeOnBackdrop) {
    emit("backdrop");
    return;
  }
  requestClose("backdrop");
}
</script>

<template>
  <Transition name="modal-fade">
    <div
      v-if="open"
      class="modal-backdrop management-dialog-backdrop"
      :class="backdropClass"
      :data-testid="testid || undefined"
      @click.self="onBackdropClick"
    >
      <section
        ref="cardRef"
        class="modal-card management-dialog-card"
        :class="[`is-${size}`, `is-${tone}`, cardClass]"
        role="dialog"
        aria-modal="true"
        :aria-label="resolvedAriaLabel"
        :aria-labelledby="titleId"
        tabindex="-1"
      >
        <header class="management-dialog-head">
          <div class="management-dialog-heading">
            <span v-if="icon" class="management-dialog-icon" aria-hidden="true">
              <i :class="icon" />
            </span>
            <div>
              <span v-if="eyebrow" class="management-dialog-eyebrow">{{ eyebrow }}</span>
              <h2 :id="titleId" class="management-dialog-title">{{ title }}</h2>
              <p v-if="description" class="management-dialog-description">{{ description }}</p>
            </div>
          </div>
          <button
            class="management-dialog-close"
            type="button"
            :title="resolvedCloseLabel"
            :aria-label="resolvedCloseLabel"
            :disabled="closeDisabled"
            @click="requestClose('header')"
          >
            <i class="fa-solid fa-xmark" aria-hidden="true" />
          </button>
        </header>

        <div class="management-dialog-body">
          <slot />
        </div>

        <footer v-if="$slots.footer" class="management-dialog-footer" :class="footerClass">
          <slot name="footer" />
        </footer>
      </section>
    </div>
  </Transition>
</template>

<style scoped>
.management-dialog-card {
  width: min(560px, calc(100vw - 48px));
  border-radius: 16px;
}

.management-dialog-card.is-sm {
  width: min(440px, calc(100vw - 48px));
}

.management-dialog-card.is-lg {
  width: min(720px, calc(100vw - 48px));
}

.management-dialog-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 18px 20px 14px;
  border-bottom: 1px solid var(--aw-border-soft, #eef2f7);
}

.management-dialog-heading {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  gap: 12px;
}

.management-dialog-icon {
  display: grid;
  width: 36px;
  height: 36px;
  flex: 0 0 36px;
  place-items: center;
  border-radius: 10px;
  background: var(--aw-green-soft, #eaf8f2);
  color: var(--aw-green-ink, #087653);
  font-size: 14px;
}

.management-dialog-card.is-danger .management-dialog-icon {
  background: var(--aw-red-soft, #fff0f2);
  color: var(--aw-red, #cc3f57);
}

.management-dialog-eyebrow {
  display: block;
  margin-bottom: 2px;
  color: var(--aw-muted, #64748b);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.01em;
  line-height: 1.3;
}

.management-dialog-card.is-danger .management-dialog-eyebrow {
  color: var(--aw-red, #cc3f57);
}

.management-dialog-title {
  margin: 0;
  color: var(--aw-text, #0f172a);
  font-size: 18px;
  font-weight: 700;
  letter-spacing: -0.02em;
  line-height: 1.25;
}

.management-dialog-description {
  margin: 6px 0 0;
  color: var(--aw-muted, #64748b);
  font-size: 13px;
  line-height: 1.5;
}

.management-dialog-close {
  display: inline-flex;
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  align-items: center;
  justify-content: center;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  background: #fff;
  color: #6b7280;
  cursor: pointer;
}

.management-dialog-close:hover:not(:disabled),
.management-dialog-close:focus-visible {
  border-color: #d1d5db;
  background: #f8fafc;
  color: #111827;
  outline: none;
}

.management-dialog-close:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.management-dialog-body {
  display: flex;
  min-height: 0;
  flex-direction: column;
  gap: 14px;
  overflow: auto;
  padding: 18px 20px;
}

.management-dialog-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  padding: 14px 20px 16px;
  border-top: 1px solid var(--aw-border-soft, #eef2f7);
}
</style>
