<script setup lang="ts">
/**
 * Recursive agent audit timeline node — supports A→B→C… nesting of any depth.
 */
import { computed, ref } from "vue";
import type { AgentAuditStep } from "../types/domain";
import AgentAuditStepNode from "./AgentAuditStepNode.vue";

const props = defineProps<{
  step: AgentAuditStep;
  index: number;
  depth?: number;
  formatLatency: (ms: number) => string;
  stepText: (step: AgentAuditStep) => string;
  displayJson: (value: unknown, state?: string) => unknown;
  stepIcon: (type: string) => string;
  delegationMeta: (step: AgentAuditStep) => string;
}>();

const expanded = ref(!(props.step.collapsed || (props.step.depth != null && props.step.depth > 1)));

function toggle() {
  expanded.value = !expanded.value;
}

function childKey(child: AgentAuditStep, cIndex: number) {
  return `${child.stepId || child.type}-${child.timeOffsetMs}-${cIndex}-d${(props.depth ?? 0) + 1}`;
}

function shortId(id?: string) {
  if (!id) return "";
  return id.length > 8 ? `${id.slice(0, 8)}…` : id;
}

function agentLabel(name?: string, id?: string) {
  if (name?.trim()) return name.trim();
  return shortId(id);
}

/** Failure/timeout tone for icon + card accent (overrides type color when error). */
const statusTone = computed(() => {
  const s = (props.step.status || "").toUpperCase();
  if (s === "FAILED" || s === "ERROR") return "is-error";
  if (s === "TIMED_OUT") return "is-timeout";
  if (s === "CANCELLED") return "is-cancelled";
  if (s === "RUNNING" || s === "ACCEPTED" || s === "WAITING_CONFIRMATION" || s === "WAITING_INTERACTION") {
    return "is-running";
  }
  if (s === "SUCCEEDED" || s === "SUCCESS") return "is-success";
  return "";
});

/**
 * Origin-aware delegation path in human form:
 * - Prefer agent display names when API provides them
 * - EXTERNAL inbound: external → target
 * - INTERNAL: caller → target
 * - outbound A2A: caller → external
 */
function delegationPath(step: AgentAuditStep): string {
  const origin = (step.origin || "").toUpperCase();
  const protocol = (step.protocol || "").toUpperCase();
  const caller = agentLabel(step.callerAgentName, step.callerAgentId);
  const target = agentLabel(step.targetAgentName, step.targetAgentId);
  const ext = (step.externalAgentRef || "").trim();
  if (origin === "EXTERNAL") {
    if (ext && target) return `${ext} → ${target}`;
    if (ext) return ext;
    if (target) return target;
    return "";
  }
  if (protocol === "A2A" && ext) {
    if (caller) return `${caller} → ${ext}`;
    return ext;
  }
  if (caller && target) return `${caller} → ${target}`;
  if (target) return target;
  if (caller) return caller;
  return "";
}
</script>

<template>
  <div class="timeline-item" :class="{ nested: (depth ?? 0) > 0 }">
    <div class="timeline-rail">
      <div class="timeline-icon" :class="[step.type, statusTone]">
        <i :class="stepIcon(step.type)" aria-hidden="true" />
      </div>
      <span class="time mono">{{ (step.timeOffsetMs / 1000).toFixed(2) }}s</span>
    </div>
    <div class="timeline-card" :class="[step.type, step.status, statusTone, { nested: (depth ?? 0) > 0 }]">
      <div class="timeline-card-head">
        <h3>
          <button
            v-if="step.type === 'agent_delegation' && (step.children?.length ?? 0) > 0"
            type="button"
            class="delegation-toggle"
            :aria-expanded="expanded"
            @click="toggle"
          >
            <i :class="expanded ? 'fa-solid fa-chevron-down' : 'fa-solid fa-chevron-right'"></i>
          </button>
          {{ step.title }}
        </h3>
        <span v-if="step.latencyMs != null" class="mono pill">{{ formatLatency(step.latencyMs) }}</span>
        <span
          v-if="step.type === 'agent_delegation' && step.tokensKnown"
          class="mono pill"
          data-testid="delegation-tokens"
        >
          tok {{ step.inputTokens ?? "—" }}/{{ step.outputTokens ?? "—" }}/{{ step.totalTokens ?? "—" }}
        </span>
        <span
          v-else-if="step.type === 'agent_delegation'"
          class="mono pill muted"
          data-testid="delegation-tokens-unknown"
        >
          tok unknown
        </span>
        <span
          v-if="step.type === 'agent_delegation'"
          class="mono pill"
          data-testid="delegation-attempts"
        >
          尝试 {{ step.attemptCount ?? 0 }} / 重试 {{ step.retryCount ?? 0 }}
        </span>
      </div>
      <p
        v-if="step.type === 'agent_delegation'"
        class="step-content muted delegation-meta"
        data-testid="delegation-meta-line"
      >
        <span data-testid="delegation-meta-text">{{ delegationMeta(step) }}</span>
        <span v-if="delegationPath(step)" data-testid="delegation-path">
          · 路径 {{ delegationPath(step) }}
        </span>
        <span v-if="step.childRunId" :title="step.childRunId"> · 子任务</span>
        <span v-if="step.remoteTaskId" :title="step.remoteTaskId"> · 远端任务</span>
        <span v-if="step.remoteEndpointRef"> · {{ step.remoteEndpointRef }}</span>
        <span v-if="step.protocolStatus"> · 协议状态 {{ step.protocolStatus }}</span>
        <span v-if="step.errorMessage"> · {{ step.errorMessage }}</span>
      </p>
      <p v-if="stepText(step)" class="step-content" :class="{ reasoning: step.type === 'reasoning' }">
        {{ stepText(step) }}
      </p>
      <div v-if="step.type === 'tool'" class="tool-grid">
        <div>
          <div class="json-label">参数</div>
          <pre class="json-view">{{ JSON.stringify(displayJson(step.params, step.paramsState), null, 2) }}</pre>
        </div>
        <div>
          <div class="json-label">结果</div>
          <pre class="json-view">{{ JSON.stringify(displayJson(step.result, step.resultState), null, 2) }}</pre>
        </div>
      </div>
      <div v-else-if="step.type === 'agent_delegation'" class="tool-grid">
        <div>
          <div class="json-label">委派输入</div>
          <pre class="json-view">{{ JSON.stringify(displayJson(step.params, step.paramsState), null, 2) }}</pre>
        </div>
        <div>
          <div class="json-label">委派输出</div>
          <pre class="json-view">{{ JSON.stringify(displayJson(step.result, step.resultState), null, 2) }}</pre>
        </div>
      </div>
      <div
        v-if="step.type === 'agent_delegation' && expanded && (step.children?.length ?? 0) > 0"
        class="delegation-children"
      >
        <AgentAuditStepNode
          v-for="(child, cIndex) in step.children"
          :key="childKey(child, cIndex)"
          :step="child"
          :index="cIndex"
          :depth="(depth ?? 0) + 1"
          :format-latency="formatLatency"
          :step-text="stepText"
          :display-json="displayJson"
          :step-icon="stepIcon"
          :delegation-meta="delegationMeta"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.timeline-item {
  display: flex;
  gap: 0.75rem;
}
.timeline-item.nested {
  margin-top: 0.25rem;
}
.timeline-rail {
  display: flex;
  flex-direction: column;
  align-items: center;
  min-width: 3.5rem;
}
.timeline-icon {
  width: 1.85rem;
  height: 1.85rem;
  border-radius: 999px;
  display: grid;
  place-items: center;
  border: 1px solid #e2e8f0;
  background: #f8fafc;
  color: #64748b;
  font-size: 0.72rem;
  flex-shrink: 0;
}

/* Semantic type colors */
.timeline-icon.input {
  color: #1d4ed8;
  background: #eff6ff;
  border-color: #bfdbfe;
}
.timeline-icon.reasoning {
  color: #7c3aed;
  background: #f5f3ff;
  border-color: #ddd6fe;
}
.timeline-icon.agent_delegation {
  color: #4338ca;
  background: #e0e7ff;
  border-color: #c7d2fe;
}
.timeline-icon.tool {
  color: #b45309;
  background: #fffbeb;
  border-color: #fde68a;
}
.timeline-icon.output {
  color: #047857;
  background: #ecfdf5;
  border-color: #a7f3d0;
}
.timeline-icon.context_compaction {
  color: #0f766e;
  background: #f0fdfa;
  border-color: #99f6e4;
}

/* Status overrides (failure wins over type) */
.timeline-icon.is-error {
  color: #b91c1c;
  background: #fef2f2;
  border-color: #fecaca;
}
.timeline-icon.is-timeout {
  color: #c2410c;
  background: #fff7ed;
  border-color: #fed7aa;
}
.timeline-icon.is-cancelled {
  color: #64748b;
  background: #f1f5f9;
  border-color: #cbd5e1;
}
.timeline-icon.is-running {
  color: #0369a1;
  background: #f0f9ff;
  border-color: #bae6fd;
}

.time {
  font-size: 0.7rem;
  color: #9ca3af;
  margin-top: 0.25rem;
}
.timeline-card {
  flex: 1;
  border: 1px solid #e5e7eb;
  border-radius: 0.5rem;
  padding: 0.65rem 0.85rem;
  background: #fff;
  min-width: 0;
  border-left-width: 3px;
  border-left-color: #e2e8f0;
}
.timeline-card.input {
  border-left-color: #3b82f6;
}
.timeline-card.reasoning {
  border-left-color: #8b5cf6;
  background: #fcfbff;
}
.timeline-card.agent_delegation {
  border-left-color: #6366f1;
}
.timeline-card.tool {
  border-left-color: #f59e0b;
}
.timeline-card.output {
  border-left-color: #10b981;
}
.timeline-card.context_compaction {
  border-left-color: #14b8a6;
}
.timeline-card.is-error,
.timeline-card.FAILED,
.timeline-card.ERROR {
  border-left-color: #ef4444;
  border-color: #fecaca;
  background: #fffafa;
}
.timeline-card.is-timeout,
.timeline-card.TIMED_OUT {
  border-left-color: #f97316;
  border-color: #fed7aa;
  background: #fffaf5;
}
.timeline-card.is-cancelled,
.timeline-card.CANCELLED {
  border-left-color: #94a3b8;
}
.timeline-card.nested {
  background: #fafafa;
}
.timeline-card.reasoning.nested {
  background: #faf8ff;
}
.timeline-card-head {
  display: flex;
  justify-content: space-between;
  gap: 0.5rem;
  align-items: center;
}
.timeline-card-head h3 {
  margin: 0;
  font-size: 0.9rem;
  font-weight: 600;
  display: flex;
  align-items: center;
}
.delegation-toggle {
  border: none;
  background: transparent;
  cursor: pointer;
  margin-right: 0.35rem;
  color: inherit;
  padding: 0 0.15rem;
}
.delegation-children {
  margin-top: 0.75rem;
  padding-left: 0.75rem;
  border-left: 2px solid #e5e7eb;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.tool-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.5rem;
  margin-top: 0.5rem;
}
.json-label {
  font-size: 0.75rem;
  color: #6b7280;
  margin-bottom: 0.25rem;
}
.json-view {
  margin: 0;
  font-size: 0.72rem;
  background: #f9fafb;
  border-radius: 0.35rem;
  padding: 0.4rem;
  overflow: auto;
  max-height: 12rem;
}
.step-content {
  margin: 0.35rem 0 0;
  font-size: 0.85rem;
  white-space: pre-wrap;
}
.muted {
  color: #6b7280;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.delegation-meta {
  font-size: 0.78rem;
  line-height: 1.45;
}
.pill {
  font-size: 0.72rem;
  background: #f3f4f6;
  border-radius: 999px;
  padding: 0.1rem 0.45rem;
}
</style>
