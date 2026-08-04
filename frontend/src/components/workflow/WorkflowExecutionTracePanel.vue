<script setup lang="ts">
import { useI18n } from "vue-i18n";

import type { Execution, ExecutionStepRecord } from "../../types/domain";

const { t } = useI18n();

defineProps<{
  execution?: Execution;
  selectedNodeId?: string;
}>();

const emit = defineEmits<{
  "select-node": [nodeId: string];
}>();

function selectStep(step: ExecutionStepRecord) {
  if (!step.nodeId) return;
  emit("select-node", step.nodeId);
}

function statusClass(status?: string) {
  return (status || "unknown")
    .replace(/([a-z0-9])([A-Z])/g, "$1-$2")
    .toLowerCase()
    .replace(/\s+/g, "-");
}

function stepLabel(step: ExecutionStepRecord) {
  return [step.nodeId || "system", step.nodeType || "runtime"].join(" · ");
}
</script>

<template>
  <section class="workflow-execution-trace-panel">
    <div v-if="execution" class="workflow-execution-trace-content">
      <header class="workflow-execution-trace-header">
        <div>
          <span>Execution Trace</span>
          <strong>{{ execution.id }}</strong>
        </div>
        <span class="status-pill" :class="statusClass(execution.status)">{{ execution.status }}</span>
      </header>

      <dl class="workflow-execution-trace-meta">
        <div>
          <dt>Duration</dt>
          <dd>{{ execution.durationMs }} ms</dd>
        </div>
        <div>
          <dt>Trace ID</dt>
          <dd>{{ execution.traceId }}</dd>
        </div>
        <div>
          <dt>Revision</dt>
          <dd>{{ execution.workflowVersion || "-" }}</dd>
        </div>
        <div>
          <dt>User</dt>
          <dd>{{ execution.userId || "-" }}</dd>
        </div>
        <div>
          <dt>Input</dt>
          <dd>{{ execution.inputSummary || "-" }}</dd>
        </div>
        <div>
          <dt>Output</dt>
          <dd>{{ execution.outputSummary || "-" }}</dd>
        </div>
        <div>
          <dt>Payload</dt>
          <dd>{{ execution.rawPayloadObjectAddress || "-" }}</dd>
        </div>
        <div v-if="execution.errorMessage">
          <dt>Error</dt>
          <dd class="workflow-execution-trace-error">{{ execution.errorMessage }}</dd>
        </div>
      </dl>

      <div class="workflow-execution-trace-steps">
        <button
          v-for="step in execution.steps"
          :key="step.id"
          class="workflow-execution-trace-step"
          :class="{ selected: step.nodeId && step.nodeId === selectedNodeId }"
          :data-step-node-id="step.nodeId"
          type="button"
          @click="selectStep(step)"
        >
          <span class="status-pill" :class="statusClass(step.status)">{{ step.status }}</span>
          <span>
            <strong>{{ step.name }}</strong>
            <small>{{ stepLabel(step) }} · {{ step.durationMs }} ms</small>
          </span>
          <em v-if="step.inputSummary">Input: {{ step.inputSummary }}</em>
          <em v-if="step.outputSummary">Output: {{ step.outputSummary }}</em>
          <em v-if="step.rawPayloadObjectAddress">Payload: {{ step.rawPayloadObjectAddress }}</em>
          <em v-if="step.errorMessage" class="workflow-execution-trace-error">Error: {{ step.errorMessage }}</em>
        </button>
      </div>
    </div>

    <div v-else class="workflow-execution-trace-empty">
      <i class="fa-solid fa-vial-circle-check" />
      <span>{{ t("workflow.noTrialTrace") }}</span>
    </div>
  </section>
</template>

<style scoped>
.workflow-execution-trace-panel {
  display: grid;
  gap: 12px;
  border: 1px solid var(--aw-border-soft);
  border-radius: 8px;
  background: #fff;
}

.workflow-execution-trace-content {
  display: grid;
  gap: 12px;
  padding: 14px;
}

.workflow-execution-trace-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.workflow-execution-trace-header span:first-child {
  display: block;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--aw-muted);
}

.workflow-execution-trace-header strong {
  display: block;
  margin-top: 4px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 13px;
  color: var(--aw-text);
}

.workflow-execution-trace-meta {
  display: grid;
  gap: 8px;
  margin: 0;
}

.workflow-execution-trace-meta div {
  display: grid;
  grid-template-columns: 82px minmax(0, 1fr);
  gap: 10px;
}

.workflow-execution-trace-meta dt {
  color: var(--aw-muted);
  font-size: 12px;
}

.workflow-execution-trace-meta dd {
  margin: 0;
  min-width: 0;
  overflow-wrap: anywhere;
  color: var(--aw-text);
  font-size: 12px;
}

.workflow-execution-trace-steps {
  display: grid;
  gap: 8px;
}

.workflow-execution-trace-step {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 8px 10px;
  width: 100%;
  padding: 10px;
  border: 1px solid var(--aw-border-soft);
  border-radius: 8px;
  background: #f8fafc;
  color: inherit;
  text-align: left;
  cursor: pointer;
}

.workflow-execution-trace-step.selected {
  border-color: var(--aw-cyan);
  box-shadow: 0 0 0 2px var(--aw-cyan-soft);
}

.workflow-execution-trace-step span:not(.status-pill) {
  min-width: 0;
}

.workflow-execution-trace-step strong,
.workflow-execution-trace-step small,
.workflow-execution-trace-step em {
  display: block;
  min-width: 0;
  overflow-wrap: anywhere;
}

.workflow-execution-trace-step strong {
  color: var(--aw-text);
  font-size: 13px;
}

.workflow-execution-trace-step small,
.workflow-execution-trace-step em {
  color: var(--aw-muted);
  font-size: 12px;
  font-style: normal;
}

.workflow-execution-trace-step em {
  grid-column: 1 / -1;
}

.workflow-execution-trace-error {
  color: var(--aw-red) !important;
}

.workflow-execution-trace-empty {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px;
  color: var(--aw-muted);
  font-size: 13px;
}
</style>
