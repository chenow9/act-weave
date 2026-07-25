<script setup lang="ts">
import type { WorkflowGraphNodeType } from "../../types/domain";

const props = defineProps<{
  variableRefs: string[];
}>();

const emit = defineEmits<{
  (event: "add-node", nodeType: WorkflowGraphNodeType): void;
}>();

const emptyVariableRef = "{{input.orderId}}";

const nodeLibrary: Array<{ type: WorkflowGraphNodeType; icon: string; title: string; description: string }> = [
  { type: "Start", icon: "fa-solid fa-play", title: "开始", description: "定义流程输入" },
  { type: "Tool", icon: "fa-solid fa-plug", title: "工具调用", description: "执行已接入能力" },
  { type: "Condition", icon: "fa-solid fa-code-branch", title: "条件判断", description: "根据结果分支" },
  { type: "SubWorkflow", icon: "fa-solid fa-diagram-project", title: "子流程", description: "执行已发布 Workflow" },
  { type: "Transform", icon: "fa-solid fa-shuffle", title: "参数整理", description: "转换上下文变量" },
  { type: "Parallel", icon: "fa-solid fa-grip-lines-vertical", title: "并行分支", description: "声明多条并行分支语义" },
  { type: "ForEach", icon: "fa-solid fa-repeat", title: "批量遍历", description: "按集合逐项执行下游节点" },
  { type: "Approval", icon: "fa-solid fa-clipboard-check", title: "人工确认", description: "等待人工决策" },
  { type: "End", icon: "fa-solid fa-flag-checkered", title: "结束", description: "汇总流程输出" },
];
</script>

<template>
  <aside class="workflow-node-palette">
    <div class="workflow-panel-heading">
      <span>节点库</span>
      <h3>Workflow Blocks</h3>
      <p>保持编排图精简，先放主路径，再补充分支和审批。</p>
    </div>

    <div class="workflow-node-library">
      <button
        v-for="node in nodeLibrary"
        :key="node.type"
        class="workflow-node-library-item"
        type="button"
        @click="emit('add-node', node.type)"
      >
        <i :class="node.icon" />
        <span>
          <strong>{{ node.title }}</strong>
          <small>{{ node.description }}</small>
        </span>
      </button>
    </div>

    <section class="workflow-variable-library">
      <div class="workflow-section-caption">
        <strong>变量引用</strong>
        <small>来自当前草稿图</small>
      </div>
      <div class="workflow-token-list">
        <span v-for="variable in props.variableRefs" :key="variable" class="workflow-token">{{ variable }}</span>
        <span v-if="!props.variableRefs.length" class="workflow-token muted">{{ emptyVariableRef }}</span>
      </div>
    </section>
  </aside>
</template>
