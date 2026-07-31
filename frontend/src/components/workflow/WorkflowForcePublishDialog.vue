<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";

const props = defineProps<{
  visible: boolean;
  workflowName?: string;
  submitting?: boolean;
}>();

const emit = defineEmits<{
  submit: [reason: string];
  close: [];
}>();

const dialogRef = ref<HTMLElement>();
const reasonInputRef = ref<HTMLInputElement>();
const reason = ref("local-dev skip trial");
const touched = ref(false);

const reasonTrimmed = computed(() => reason.value.trim());
const reasonValid = computed(() => reasonTrimmed.value.length >= 8);
const showError = computed(() => touched.value && !reasonValid.value);

watch(
  () => props.visible,
  (visible) => {
    if (!visible) return;
    reason.value = "local-dev skip trial";
    touched.value = false;
    void nextTick(() => {
      reasonInputRef.value?.focus();
      reasonInputRef.value?.select();
    });
  },
  { immediate: true },
);

function closeIfIdle() {
  if (props.submitting) return;
  emit("close");
}

function handleEsc() {
  closeIfIdle();
}

function submit() {
  if (props.submitting) return;
  touched.value = true;
  if (!reasonValid.value) return;
  emit("submit", reasonTrimmed.value);
}

function trapDialogFocus(event: KeyboardEvent) {
  const root = dialogRef.value;
  if (!root || event.key !== "Tab") return;
  const focusable = Array.from(
    root.querySelectorAll<HTMLElement>(
      'button:not(:disabled), [href], input:not(:disabled), textarea:not(:disabled), select:not(:disabled), [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((el) => el.offsetParent !== null || el === document.activeElement);
  if (focusable.length === 0) return;
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}
</script>

<template>
  <div
    v-if="visible"
    class="workflow-force-publish-backdrop workflow-modal-layer"
    data-testid="workflow-force-publish-dialog"
    @click.self="closeIfIdle"
  >
    <section
      ref="dialogRef"
      class="workflow-force-publish-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="workflow-force-publish-title"
      tabindex="-1"
      @keydown.esc.stop.prevent="handleEsc"
      @keydown.tab="trapDialogFocus"
    >
      <header class="workflow-force-publish-header">
        <div class="workflow-force-publish-badge" aria-hidden="true">
          <i class="fa-solid fa-bolt" />
        </div>
        <div>
          <span>平台管理员 · 强制发布</span>
          <h4 id="workflow-force-publish-title">{{ workflowName || "当前流程" }}</h4>
          <p>
            将<strong>跳过试运行</strong>，把当前 VALID
            编译直接冻结为已发布版本。不会调用真实业务链路做验证，请确认影响面后继续。
          </p>
        </div>
        <button class="ghost-button" type="button" :disabled="submitting" aria-label="关闭" @click="closeIfIdle">
          关闭
        </button>
      </header>

      <div class="workflow-force-publish-body">
        <div class="workflow-force-publish-warning" role="note">
          <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
          <div>
            <strong>高风险操作</strong>
            <span>Agent 将可立即调用该版本；若工具配置有误，会直接进入生产可调用状态。</span>
          </div>
        </div>

        <label class="workflow-force-publish-field">
          <span>发布原因 <em>（至少 8 个字符，用于审计）</em></span>
          <input
            ref="reasonInputRef"
            v-model="reason"
            type="text"
            name="force-publish-reason"
            autocomplete="off"
            maxlength="500"
            placeholder="例如：local-dev skip trial for AI pipeline"
            :disabled="submitting"
            :aria-invalid="showError"
            @keydown.enter.prevent="submit"
            @input="touched = true"
          />
          <small v-if="showError" class="workflow-force-publish-error">请填写至少 8 个字符的原因。</small>
          <small v-else class="workflow-force-publish-hint">{{ reasonTrimmed.length }} / 500</small>
        </label>
      </div>

      <footer class="workflow-force-publish-actions">
        <button class="ghost-button" type="button" :disabled="submitting" @click="closeIfIdle">取消</button>
        <button
          class="workflow-force-publish-confirm"
          type="button"
          data-action="confirm-force-publish"
          :disabled="submitting || !reasonValid"
          @click="submit"
        >
          {{ submitting ? "正在强制发布…" : "确认强制发布" }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.workflow-force-publish-backdrop {
  position: fixed;
  inset: 0;
  z-index: 3100;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 28px;
  background: rgba(15, 23, 42, 0.42);
  backdrop-filter: blur(5px);
}

.workflow-force-publish-dialog {
  width: min(480px, calc(100vw - 40px));
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: #fff;
  border: 1px solid rgba(251, 146, 60, 0.35);
  border-radius: 16px;
  box-shadow:
    0 28px 70px rgba(15, 23, 42, 0.28),
    0 0 0 1px rgba(255, 255, 255, 0.4) inset;
}

.workflow-force-publish-dialog:focus-visible {
  outline: 3px solid rgba(234, 88, 12, 0.45);
  outline-offset: 3px;
}

.workflow-force-publish-header {
  display: grid;
  grid-template-columns: auto 1fr auto;
  gap: 14px;
  align-items: start;
  padding: 18px 18px 14px;
  border-bottom: 1px solid #ffedd5;
  background: linear-gradient(180deg, #fff7ed 0%, #fff 100%);
}

.workflow-force-publish-badge {
  width: 40px;
  height: 40px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #c2410c;
  background: #ffedd5;
  border: 1px solid #fdba74;
  border-radius: 12px;
  font-size: 16px;
}

.workflow-force-publish-header span {
  display: block;
  color: #c2410c;
  font-size: 11px;
  font-weight: 800;
  letter-spacing: 0.02em;
  text-transform: uppercase;
}

.workflow-force-publish-header h4 {
  margin: 4px 0 6px;
  color: #0f172a;
  font-size: 17px;
  font-weight: 800;
  line-height: 1.3;
}

.workflow-force-publish-header p {
  margin: 0;
  color: #64748b;
  font-size: 13px;
  line-height: 1.55;
}

.workflow-force-publish-header p strong {
  color: #9a3412;
  font-weight: 700;
}

.workflow-force-publish-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 16px 18px;
}

.workflow-force-publish-warning {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  padding: 12px 14px;
  color: #9a3412;
  background: #fff7ed;
  border: 1px solid #fdba74;
  border-radius: 12px;
}

.workflow-force-publish-warning i {
  margin-top: 2px;
  color: #ea580c;
  font-size: 15px;
}

.workflow-force-publish-warning strong {
  display: block;
  margin-bottom: 2px;
  color: #9a3412;
  font-size: 13px;
  font-weight: 800;
}

.workflow-force-publish-warning span {
  color: #b45309;
  font-size: 12px;
  line-height: 1.5;
}

.workflow-force-publish-field {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.workflow-force-publish-field > span {
  color: #334155;
  font-size: 13px;
  font-weight: 700;
}

.workflow-force-publish-field > span em {
  color: #94a3b8;
  font-style: normal;
  font-weight: 500;
}

.workflow-force-publish-field input {
  min-height: 42px;
  padding: 8px 12px;
  color: #0f172a;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  font: inherit;
  font-size: 14px;
  font-weight: 500;
  transition:
    border-color 0.15s ease,
    box-shadow 0.15s ease;
}

.workflow-force-publish-field input:focus {
  outline: none;
  border-color: #fb923c;
  box-shadow: 0 0 0 3px rgba(251, 146, 60, 0.2);
}

.workflow-force-publish-field input[aria-invalid="true"] {
  border-color: #f87171;
  box-shadow: 0 0 0 3px rgba(248, 113, 113, 0.15);
}

.workflow-force-publish-error {
  color: #b91c1c;
  font-size: 12px;
  font-weight: 600;
}

.workflow-force-publish-hint {
  color: #94a3b8;
  font-size: 12px;
}

.workflow-force-publish-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 12px 18px 18px;
  border-top: 1px solid #f1f5f9;
}

.workflow-force-publish-confirm {
  min-height: 40px;
  padding: 0 16px;
  color: #fff;
  background: linear-gradient(180deg, #ea580c 0%, #c2410c 100%);
  border: 1px solid #9a3412;
  border-radius: 10px;
  font-size: 14px;
  font-weight: 700;
  cursor: pointer;
  box-shadow: 0 1px 2px rgba(154, 52, 18, 0.2);
  transition:
    filter 0.15s ease,
    opacity 0.15s ease;
}

.workflow-force-publish-confirm:hover:not(:disabled),
.workflow-force-publish-confirm:focus-visible:not(:disabled) {
  filter: brightness(1.05);
}

.workflow-force-publish-confirm:disabled {
  cursor: not-allowed;
  opacity: 0.55;
  filter: none;
}
</style>
