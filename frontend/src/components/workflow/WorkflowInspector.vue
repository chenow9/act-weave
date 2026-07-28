<script setup lang="ts">
import StartNodeEditor from "./editors/StartNodeEditor.vue";
import EndNodeEditor from "./editors/EndNodeEditor.vue";
import ToolNodeEditor from "./editors/ToolNodeEditor.vue";
import HTTPNodeEditor from "./editors/HTTPNodeEditor.vue";
import SubWorkflowNodeEditor from "./editors/SubWorkflowNodeEditor.vue";
import ParallelNodeEditor from "./editors/ParallelNodeEditor.vue";
import ForEachNodeEditor from "./editors/ForEachNodeEditor.vue";
import type { Tool, WorkflowGraphNode } from "../../types/domain";

const props = defineProps<{
  node?: WorkflowGraphNode;
  toolOptions?: Array<{ label: string; value: string }>;
  tools?: Tool[];
  variableRefs: string[];
  toolCatalogError?: string;
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
      <span>属性面板</span>
      <h3>{{ props.node?.label || "选择一个节点" }}</h3>
      <p>{{ props.node ? "修改节点名称后，画布会立即同步。" : "从中间画布选择节点后在这里编辑。" }}</p>
    </div>

    <div v-if="props.node" class="workflow-inspector-form">
      <label class="drawer-field">
        <span>节点名称</span>
        <input
          name="node-label"
          :value="props.node.label"
          @input="emit('update-node-label', ($event.target as HTMLInputElement).value)"
        />
      </label>
      <label class="drawer-field">
        <span>节点类型</span>
        <input :value="props.node.type" readonly />
      </label>

      <div class="workflow-inspector-meta">
        <span>节点 ID {{ props.node.id }}</span>
        <span>坐标 X {{ Math.round(props.node.position.x) }} · Y {{ Math.round(props.node.position.y) }}</span>
      </div>

      <section class="workflow-inspector-vars">
        <div class="workflow-section-caption">
          <strong>节点参数</strong>
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
          <span>条件表达式</span>
          <textarea
            name="node-condition-expression"
            rows="4"
            :value="nodeDataValue('expression')"
            placeholder="例如 nodeOutputs.tool.status == 'paid'"
            @input="
              emit('update-node-data', { key: 'expression', value: ($event.target as HTMLTextAreaElement).value })
            "
          />
        </label>

        <label v-else-if="props.node.type === 'Transform'" class="drawer-field">
          <span>转换模板</span>
          <textarea
            name="node-transform-template"
            rows="4"
            :value="nodeDataValue('template')"
            placeholder="例如 订单 {{input.orderId}}"
            @input="emit('update-node-data', { key: 'template', value: ($event.target as HTMLTextAreaElement).value })"
          />
        </label>

        <label v-else-if="props.node.type === 'Approval'" class="drawer-field">
          <span>审批原因</span>
          <textarea
            name="node-approval-reason"
            rows="4"
            :value="nodeDataValue('reason')"
            placeholder="描述需要人工确认的原因"
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
          <strong>可用变量</strong>
          <small>用于后续节点绑定</small>
        </div>
        <div class="workflow-token-list">
          <span v-for="variable in props.variableRefs" :key="variable" class="workflow-token">{{ variable }}</span>
        </div>
      </section>
    </div>

    <div v-else class="workflow-inspector-empty">
      <i class="fa-solid fa-arrow-pointer" />
      <strong>还没有选中节点</strong>
      <small>点击画布中的节点后，在这里查看和修改属性。</small>
    </div>
  </section>
</template>
