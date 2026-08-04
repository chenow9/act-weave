<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";

import type { OutboundCredentialsEnvelope } from "../../types/domain";

interface WorkflowTrialRunInputField {
  key: string;
  type: string;
  required: boolean;
  description: string;
}

export interface WorkflowTrialRunSubmitPayload {
  input: Record<string, unknown>;
  outboundCredentials?: OutboundCredentialsEnvelope;
}

const props = defineProps<{
  visible: boolean;
  workflowName?: string;
  inputSchema: WorkflowTrialRunInputField[];
  lastSuccessfulInput?: Record<string, unknown>;
  submitting?: boolean;
  /** When true, show write-only passthrough Token fields. */
  requiresPassthrough?: boolean;
  passthroughConnectionId?: string;
}>();

const emit = defineEmits<{
  submit: [payload: WorkflowTrialRunSubmitPayload];
  close: [];
}>();

const { t } = useI18n();

const form = reactive<Record<string, unknown>>({});
const touched = reactive<Record<string, boolean>>({});
const fieldErrors = reactive<Record<string, string>>({});
const inputMode = ref<"form" | "raw" | "reuse">("form");
const rawJsonInput = ref("{}");
const rawJsonError = ref("");
const dialogRef = ref<HTMLElement>();
const passthroughToken = ref("");
const passthroughExpiresAt = ref("");

const hasFields = computed(() => props.inputSchema.length > 0);
const hasLastSuccessfulInput = computed(() =>
  Boolean(props.lastSuccessfulInput && Object.keys(props.lastSuccessfulInput).length > 0),
);

watch(
  [() => props.visible, () => props.inputSchema],
  ([visible, schema]) => {
    if (!visible) return;
    resetForm(schema);
    inputMode.value = "form";
    rawJsonInput.value = JSON.stringify(buildPayloadFromDefaults(schema), null, 2);
    rawJsonError.value = "";
    passthroughToken.value = "";
    passthroughExpiresAt.value = "";
    void nextTick(focusInitialControl);
  },
  { immediate: true },
);

function buildOutboundEnvelope(): OutboundCredentialsEnvelope | undefined {
  if (!props.requiresPassthrough) return undefined;
  if (!props.passthroughConnectionId || !passthroughToken.value.trim()) {
    rawJsonError.value = t("workflow.passthroughTokenRequired");
    return undefined;
  }
  const expiresAt = passthroughExpiresAt.value
    ? new Date(passthroughExpiresAt.value).toISOString()
    : new Date(Date.now() + 10 * 60 * 1000).toISOString();
  return {
    schemaVersion: "outbound-credentials.v1",
    bindings: [
      {
        connectionId: props.passthroughConnectionId,
        credentialType: "ACCESS_TOKEN",
        value: passthroughToken.value,
        expiresAt,
      },
    ],
  };
}

function emitSubmit(input: Record<string, unknown>) {
  const outboundCredentials = buildOutboundEnvelope();
  if (props.requiresPassthrough && !outboundCredentials) return;
  emit("submit", { input, outboundCredentials });
  passthroughToken.value = "";
  if (outboundCredentials) {
    outboundCredentials.bindings.forEach((b) => {
      b.value = "";
    });
  }
}

function focusInitialControl() {
  const root = dialogRef.value;
  const target = root?.querySelector<HTMLElement>(
    'button[data-mode="form"], input:not(:disabled), textarea:not(:disabled), button:not(:disabled)',
  );
  (target || root)?.focus();
}

function closeIfIdle() {
  if (props.submitting) return;
  emit("close");
}

function handleEsc() {
  closeIfIdle();
}

function focusableElements(root: HTMLElement) {
  return Array.from(
    root.querySelectorAll<HTMLElement>(
      'button:not(:disabled), [href], input:not(:disabled), textarea:not(:disabled), select:not(:disabled), [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((element) => element.offsetParent !== null || element === document.activeElement);
}

function trapDialogFocus(event: KeyboardEvent) {
  const root = dialogRef.value;
  if (!root || event.key !== "Tab") return;

  const focusable = focusableElements(root);
  if (!focusable.length) {
    event.preventDefault();
    root.focus();
    return;
  }

  const first = focusable[0]!;
  const last = focusable[focusable.length - 1]!;
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

function resetForm(schema: WorkflowTrialRunInputField[]) {
  for (const key of Object.keys(form)) {
    delete form[key];
  }
  for (const key of Object.keys(touched)) {
    delete touched[key];
  }
  for (const key of Object.keys(fieldErrors)) {
    delete fieldErrors[key];
  }

  for (const field of schema) {
    form[field.key] = defaultValueFor(field.type);
  }
}

function defaultValueFor(type: string) {
  if (type === "boolean") return false;
  return "";
}

function buildPayloadFromDefaults(schema: WorkflowTrialRunInputField[]) {
  return Object.fromEntries(schema.map((field) => [field.key, defaultValueFor(field.type)]));
}

function inputType(type: string) {
  if (type === "integer" || type === "number") return "number";
  return "text";
}

function normalizeValue(type: string, value: unknown) {
  if (type === "boolean") return Boolean(value);
  if ((type === "integer" || type === "number") && value !== "") return Number(value);
  return value;
}

function markFieldTouched(key: string) {
  touched[key] = true;
  delete fieldErrors[key];
}

function hasValue(field: WorkflowTrialRunInputField, value: unknown) {
  if (field.type === "boolean") {
    return field.required || Boolean(touched[field.key]);
  }
  if (typeof value === "string") {
    return value.trim().length > 0;
  }
  return value !== "" && value !== undefined && value !== null;
}

function submit() {
  if (props.submitting) return;
  if (inputMode.value === "raw") {
    submitRawJson();
    return;
  }
  if (inputMode.value === "reuse") {
    if (!hasLastSuccessfulInput.value) return;
    emitSubmit({ ...(props.lastSuccessfulInput || {}) });
    return;
  }

  const payload: Record<string, unknown> = {};
  let hasErrors = false;

  for (const field of props.inputSchema) {
    const value = form[field.key];
    const present = hasValue(field, value);
    if (field.required && !present) {
      fieldErrors[field.key] = t("workflow.requiredFieldFill");
      hasErrors = true;
      continue;
    }
    if (!present) {
      continue;
    }
    payload[field.key] = normalizeValue(field.type, value);
  }

  if (hasErrors) {
    return;
  }

  emitSubmit(payload);
}

function submitRawJson() {
  if (props.submitting) return;
  rawJsonError.value = "";
  try {
    const parsed = JSON.parse(rawJsonInput.value || "{}") as unknown;
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      rawJsonError.value = t("workflow.jsonMustBeObject");
      return;
    }
    emitSubmit(parsed as Record<string, unknown>);
  } catch {
    rawJsonError.value = t("workflow.invalidJson");
  }
}
</script>

<template>
  <div v-if="visible" class="workflow-trial-run-backdrop workflow-modal-layer" @click.self="closeIfIdle">
    <section
      ref="dialogRef"
      class="workflow-trial-run-dialog"
      aria-modal="true"
      role="dialog"
      tabindex="-1"
      @keydown.esc.stop.prevent="handleEsc"
      @keydown.tab="trapDialogFocus"
    >
      <header class="workflow-trial-run-header">
        <div>
          <span>{{ t("workflow.trialInputTitle") }}</span>
          <h4>{{ workflowName || t("workflow.currentWorkflow") }}</h4>
          <p>{{ t("workflow.trialInputHint") }}</p>
        </div>
        <button class="ghost-button" type="button" :disabled="submitting" @click="closeIfIdle">
          {{ t("common.close") }}
        </button>
      </header>

      <div class="workflow-trial-run-mode-tabs">
        <button type="button" :class="{ active: inputMode === 'form' }" data-mode="form" @click="inputMode = 'form'">
          {{ t("workflow.formMode") }}
        </button>
        <button type="button" :class="{ active: inputMode === 'raw' }" data-mode="raw" @click="inputMode = 'raw'">
          JSON
        </button>
        <button
          type="button"
          :class="{ active: inputMode === 'reuse' }"
          data-mode="reuse"
          :disabled="!hasLastSuccessfulInput"
          @click="inputMode = 'reuse'"
        >
          {{ t("workflow.lastSuccess") }}
        </button>
      </div>

      <div v-if="inputMode === 'form' && hasFields" class="workflow-trial-run-fields">
        <label v-for="field in inputSchema" :key="field.key" class="workflow-trial-run-field">
          <span>{{ field.description || field.key }}</span>
          <small>{{ field.key }}</small>
          <input
            v-if="field.type !== 'boolean'"
            v-model="form[field.key]"
            :name="field.key"
            :required="field.required"
            :type="inputType(field.type)"
            @input="markFieldTouched(field.key)"
          />
          <input
            v-else
            v-model="form[field.key]"
            :name="field.key"
            type="checkbox"
            @change="markFieldTouched(field.key)"
          />
          <small v-if="fieldErrors[field.key]" class="workflow-trial-run-error">{{ fieldErrors[field.key] }}</small>
        </label>
      </div>

      <div v-else-if="inputMode === 'form'" class="workflow-trial-run-empty">
        <i class="fa-solid fa-vial" />
        <span>{{ t("workflow.noStartFields") }}</span>
      </div>

      <div v-else-if="inputMode === 'raw'" class="workflow-trial-run-raw">
        <textarea v-model="rawJsonInput" name="raw-json-input" spellcheck="false" />
        <small v-if="rawJsonError" class="workflow-trial-run-error">{{ rawJsonError }}</small>
      </div>

      <div v-else class="workflow-trial-run-reuse">
        <pre v-if="hasLastSuccessfulInput">{{ JSON.stringify(lastSuccessfulInput, null, 2) }}</pre>
        <span v-else>{{ t("workflow.noLastSuccess") }}</span>
      </div>

      <section
        v-if="requiresPassthrough"
        class="workflow-trial-outbound"
        data-testid="workflow-trial-outbound-envelope"
        :aria-label="t('workflow.outboundPassthroughAria')"
      >
        <header>
          <strong>{{ t("workflow.outboundPassthrough") }}</strong>
          <span>{{ t("workflow.outboundTokenHint") }}</span>
        </header>
        <label>
          {{ t("workflow.businessToken") }}
          <input
            v-model="passthroughToken"
            type="password"
            autocomplete="new-password"
            data-testid="workflow-trial-passthrough-token"
            :placeholder="t('workflow.oneTimeTokenPh')"
            :disabled="submitting"
          />
        </label>
        <label>
          {{ t("workflow.expiresAt") }}
          <input
            v-model="passthroughExpiresAt"
            type="datetime-local"
            data-testid="workflow-trial-passthrough-expires"
            :disabled="submitting"
          />
        </label>
      </section>

      <footer class="workflow-trial-run-actions">
        <button class="ghost-button" type="button" :disabled="submitting" @click="closeIfIdle">
          {{ t("common.cancel") }}
        </button>
        <button
          data-action="submit-trial-run"
          class="primary-button"
          type="button"
          :disabled="submitting"
          @click="submit"
        >
          {{ submitting ? t("workflow.submittingTrial") : t("workflow.simulateRun") }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.workflow-trial-run-backdrop {
  position: fixed;
  inset: 0;
  z-index: 3000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: rgba(15, 23, 42, 0.38);
  backdrop-filter: blur(8px);
}

.workflow-modal-layer {
  z-index: 3000;
}

.workflow-trial-run-dialog {
  width: min(560px, 100%);
  border-radius: 12px;
  border: 1px solid rgba(148, 163, 184, 0.25);
  background: #fff;
  box-shadow: 0 24px 60px rgba(15, 23, 42, 0.2);
}

.workflow-trial-run-header,
.workflow-trial-run-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 20px 24px;
}

.workflow-trial-run-header {
  border-bottom: 1px solid rgba(226, 232, 240, 0.9);
}

.workflow-trial-run-header span {
  display: block;
  font-size: 12px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: #64748b;
}

.workflow-trial-run-header h4 {
  margin: 4px 0 6px;
  font-size: 20px;
  color: #0f172a;
}

.workflow-trial-run-header p {
  margin: 0;
  color: #475569;
}

.workflow-trial-run-fields {
  display: grid;
  gap: 14px;
  padding: 24px;
}

.workflow-trial-run-mode-tabs {
  display: flex;
  gap: 8px;
  padding: 16px 24px 0;
}

.workflow-trial-run-mode-tabs button {
  min-height: 44px;
  border: 1px solid rgba(148, 163, 184, 0.45);
  border-radius: 8px;
  padding: 0 12px;
  color: #475569;
  background: #fff;
  cursor: pointer;
}

.workflow-trial-run-mode-tabs button.active {
  border-color: var(--aw-cyan);
  color: #0f766e;
  background: var(--aw-cyan-soft);
}

.workflow-trial-run-mode-tabs button:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.workflow-trial-run-field {
  display: grid;
  gap: 8px;
}

.workflow-trial-run-field span {
  font-weight: 600;
  color: #0f172a;
}

.workflow-trial-run-field small {
  color: #64748b;
}

.workflow-trial-run-error {
  color: #b42318;
}

.workflow-trial-run-field input {
  min-height: 44px;
  border: 1px solid rgba(148, 163, 184, 0.45);
  border-radius: 12px;
  padding: 0 14px;
  font: inherit;
  color: #0f172a;
  background: #fff;
}

.workflow-trial-run-field input[type="checkbox"] {
  min-height: 20px;
  justify-self: start;
}

.workflow-trial-run-empty {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 24px;
  color: #475569;
}

.workflow-trial-run-raw,
.workflow-trial-run-reuse {
  display: grid;
  gap: 8px;
  padding: 24px;
}

.workflow-trial-run-raw textarea,
.workflow-trial-run-reuse pre {
  min-height: 180px;
  margin: 0;
  border: 1px solid rgba(148, 163, 184, 0.45);
  border-radius: 8px;
  padding: 12px;
  color: #0f172a;
  background: #f8fafc;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
  line-height: 1.55;
}

.workflow-trial-run-raw textarea {
  resize: vertical;
}

.workflow-trial-run-reuse pre {
  overflow: auto;
  white-space: pre-wrap;
}

.workflow-trial-run-actions {
  border-top: 1px solid rgba(226, 232, 240, 0.9);
}

.workflow-trial-run-actions .primary-button,
.workflow-trial-run-actions .ghost-button,
.workflow-trial-run-header .ghost-button {
  min-height: 44px;
}

.workflow-trial-outbound {
  margin: 12px 0 0;
  padding: 10px 12px;
  border: 1px solid #dbeafe;
  border-radius: 8px;
  background: #f8fafc;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.workflow-trial-outbound header {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.workflow-trial-outbound header span {
  font-size: 12px;
  color: #64748b;
}
.workflow-trial-outbound label {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12px;
}
.workflow-trial-outbound input {
  padding: 6px 8px;
}
</style>
