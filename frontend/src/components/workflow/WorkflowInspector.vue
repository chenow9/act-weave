<script setup lang="ts">
import { useI18n } from "vue-i18n";

import type { Tool, WorkflowGraphNode } from "../../types/domain";
import EndNodeEditor from "./editors/EndNodeEditor.vue";
import ForEachNodeEditor from "./editors/ForEachNodeEditor.vue";
import HTTPNodeEditor from "./editors/HTTPNodeEditor.vue";
import ParallelNodeEditor from "./editors/ParallelNodeEditor.vue";
import StartNodeEditor from "./editors/StartNodeEditor.vue";
import SubWorkflowNodeEditor from "./editors/SubWorkflowNodeEditor.vue";
import ToolNodeEditor from "./editors/ToolNodeEditor.vue";

const { t } = useI18n();

const props = defineProps<{
  node?: WorkflowGraphNode;
  toolOptions?: Array<{ label: string; value: string }>;
  tools?: Tool[];
  variableRefs: string[];
  toolCatalogError?: string;
  aiReason?: string;
  locked?: boolean;
}>();

const emit = defineEmits<{
  (event: "update-node-label", value: string): void;
  (event: "update-node-data", payload: { key: string; value: unknown }): void;
}>();

function nodeDataValue(key: string) {
  const value = props.node?.data?.[key];
  return typeof value === "string" ? value : "";
}
</script>

<template>
  <section class="workflow-inspector">
    <div class="workflow-panel-heading">
      <span>{{ t("workflow.inspectorTitle") }}</span>
      <h3>{{ props.node?.label || t("workflow.selectNode") }}</h3>
      <p>{{ props.node ? t("workflow.inspectorHintSelected") : t("workflow.inspectorHintEmpty") }}</p>
    </div>

    <div v-if="props.node" class="workflow-inspector-form">
      <label class="drawer-field">
        <span>{{ t("workflow.nodeName") }}</span>
        <input
          name="node-label"
          :value="props.node.label"
          :disabled="props.locked"
          @input="emit('update-node-label', ($event.target as HTMLInputElement).value)"
        />
      </label>
      <label class="drawer-field">
        <span>{{ t("workflow.nodeType") }}</span>
        <input :value="props.node.type" readonly />
      </label>

      <div class="workflow-inspector-meta">
        <span>{{ t("workflow.nodeId", { id: props.node.id }) }}</span>
        <span>{{
          t("workflow.nodePosition", {
            x: Math.round(props.node.position.x),
            y: Math.round(props.node.position.y),
          })
        }}</span>
      </div>

      <section v-if="props.aiReason" class="workflow-inspector-ai-reason" data-testid="workflow-generate-ai-reason">
        <strong>{{ t("workflow.generateAiReason") }}</strong>
        <p>{{ props.aiReason }}</p>
      </section>

      <section class="workflow-inspector-vars">
        <div class="workflow-section-caption">
          <strong>{{ t("workflow.nodeParams") }}</strong>
          <small>{{ props.node.type }}</small>
        </div>

        <StartNodeEditor
          v-if="props.node.type === 'Start'"
          :node="props.node"
          @update-node-data="emit('update-node-data', $event)"
        />

        <ToolNodeEditor
          v-else-if="props.node.type === 'Tool'"
          :node="props.node"
          :tools="props.tools || []"
          :tool-options="props.toolOptions || []"
          :variable-refs="props.variableRefs"
          :tool-catalog-error="props.toolCatalogError"
          @update-node-data="emit('update-node-data', $event)"
        />

        <HTTPNodeEditor
          v-else-if="props.node.type === 'HTTP'"
          :node="props.node"
          :variable-refs="props.variableRefs"
          @update-node-data="emit('update-node-data', $event)"
        />

        <SubWorkflowNodeEditor
          v-else-if="props.node.type === 'SubWorkflow'"
          :node="props.node"
          :variable-refs="props.variableRefs"
          @update-node-data="emit('update-node-data', $event)"
        />

        <label v-else-if="props.node.type === 'Condition'" class="drawer-field">
          <span>{{ t("workflow.conditionExpression") }}</span>
          <textarea
            name="node-condition-expression"
            rows="4"
            :value="nodeDataValue('expression')"
            :placeholder="t('workflow.conditionPh')"
            @input="
              emit('update-node-data', { key: 'expression', value: ($event.target as HTMLTextAreaElement).value })
            "
          />
        </label>

        <label v-else-if="props.node.type === 'Transform'" class="drawer-field">
          <span>{{ t("workflow.transformTemplate") }}</span>
          <textarea
            name="node-transform-template"
            rows="4"
            :value="nodeDataValue('template')"
            :placeholder="t('workflow.transformPh')"
            @input="emit('update-node-data', { key: 'template', value: ($event.target as HTMLTextAreaElement).value })"
          />
        </label>

        <label v-else-if="props.node.type === 'Approval'" class="drawer-field">
          <span>{{ t("workflow.approvalReason") }}</span>
          <textarea
            name="node-approval-reason"
            rows="4"
            :value="nodeDataValue('reason')"
            :placeholder="t('workflow.approvalPh')"
            @input="emit('update-node-data', { key: 'reason', value: ($event.target as HTMLTextAreaElement).value })"
          />
        </label>

        <ParallelNodeEditor
          v-else-if="props.node.type === 'Parallel'"
          :node="props.node"
          @update-node-data="emit('update-node-data', $event)"
        />

        <ForEachNodeEditor
          v-else-if="props.node.type === 'ForEach'"
          :node="props.node"
          :variable-refs="props.variableRefs"
          @update-node-data="emit('update-node-data', $event)"
        />

        <EndNodeEditor
          v-else-if="props.node.type === 'End'"
          :node="props.node"
          :variable-refs="props.variableRefs"
          @update-node-data="emit('update-node-data', $event)"
        />
      </section>

      <section class="workflow-inspector-vars">
        <div class="workflow-section-caption">
          <strong>{{ t("workflow.availableVars") }}</strong>
          <small>{{ t("workflow.availableVarsHint") }}</small>
        </div>
        <div class="workflow-token-list">
          <span v-for="variable in props.variableRefs" :key="variable" class="workflow-token">{{ variable }}</span>
        </div>
      </section>
    </div>

    <div v-else class="workflow-inspector-empty">
      <i class="fa-solid fa-arrow-pointer" />
      <strong>{{ t("workflow.noNodeSelected") }}</strong>
      <small>{{ t("workflow.noNodeSelectedHint") }}</small>
    </div>
  </section>
</template>
