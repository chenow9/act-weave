<script setup lang="ts">
import AppSelect from "./AppSelect.vue";

import type { ToolSchemaNode, ToolSchemaNodeType } from "../types/domain";

const props = withDefaults(
  defineProps<{
    node: ToolSchemaNode;
    depth: number;
    locationEnabled?: boolean;
  }>(),
  {
    locationEnabled: false,
  },
);

const emit = defineEmits<{
  "update:node": [value: ToolSchemaNode];
  remove: [];
}>();

const typeOptions: Array<{ label: string; value: ToolSchemaNodeType }> = [
  { label: "字符串", value: "string" },
  { label: "整数", value: "integer" },
  { label: "数字", value: "number" },
  { label: "布尔值", value: "boolean" },
  { label: "对象", value: "object" },
  { label: "数组", value: "array" },
];
const yesNoOptions = [
  { label: "必填", value: "true" },
  { label: "选填", value: "false" },
];
const locationOptions = [
  { label: "路径参数", value: "Path" },
  { label: "查询参数", value: "Query" },
  { label: "请求体", value: "Body" },
  { label: "请求头", value: "Header" },
];

function asLocation(value: string | number | boolean): string {
  return typeof value === "string" ? value : "Body";
}

function updateField<K extends keyof ToolSchemaNode>(key: K, value: ToolSchemaNode[K]) {
  const nextNode = { ...props.node, [key]: value };
  if (key === "type") {
    if (value === "object") {
      nextNode.children = nextNode.children || [];
      nextNode.item = null;
    } else if (value === "array") {
      nextNode.item = nextNode.item || createChildNode("item");
      nextNode.children = [];
    } else {
      nextNode.children = [];
      nextNode.item = null;
    }
  }
  emit("update:node", nextNode);
}

function createChildNode(name = ""): ToolSchemaNode {
  return {
    id: `schema-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    name,
    type: "string",
    description: "",
    required: true,
    location: props.locationEnabled ? props.node.location : undefined,
    children: [],
    item: null,
    additionalProperties: null,
  };
}

function addChildNode() {
  emit("update:node", {
    ...props.node,
    children: [...(props.node.children || []), createChildNode("")],
  });
}

function updateChildNode(childId: string, childNode: ToolSchemaNode) {
  emit("update:node", {
    ...props.node,
    children: (props.node.children || []).map((child) => (child.id === childId ? childNode : child)),
  });
}

function removeChildNode(childId: string) {
  emit("update:node", {
    ...props.node,
    children: (props.node.children || []).filter((child) => child.id !== childId),
  });
}

function updateItemNode(itemNode: ToolSchemaNode) {
  emit("update:node", {
    ...props.node,
    item: itemNode,
  });
}
</script>

<template>
  <div class="tool-schema-node" :style="{ '--tool-schema-depth': String(depth) }">
    <div class="editable-schema-row tool-schema-node-grid">
      <label v-if="locationEnabled">
        <span>参数位置</span>
        <AppSelect :model-value="node.location || 'Body'" :options="locationOptions" compact @update:model-value="updateField('location', asLocation($event))" />
      </label>
      <label>
        <span>字段名称</span>
        <input :value="node.name" @input="updateField('name', ($event.target as HTMLInputElement).value)" />
      </label>
      <label>
        <span>字段类型</span>
        <AppSelect :model-value="node.type" :options="typeOptions" compact @update:model-value="updateField('type', $event as ToolSchemaNodeType)" />
      </label>
      <label>
        <span>是否必填</span>
        <AppSelect :model-value="node.required ? 'true' : 'false'" :options="yesNoOptions" compact @update:model-value="updateField('required', $event === 'true')" />
      </label>
      <label class="tool-schema-node-description">
        <span>字段说明</span>
        <input :value="node.description" @input="updateField('description', ($event.target as HTMLInputElement).value)" />
      </label>
      <button class="icon-action-button danger inline-delete-button" type="button" title="删除字段" @click="emit('remove')">
        <i class="fa-solid fa-trash" />
      </button>
    </div>

    <div v-if="node.type === 'object'" class="tool-schema-node-children">
      <div class="tool-schema-node-toolbar">
        <strong>子字段</strong>
        <button class="ghost-button" type="button" @click="addChildNode">添加子字段</button>
      </div>
      <ToolSchemaTreeNodeEditor
        v-for="child in node.children || []"
        :key="child.id"
        :node="child"
        :depth="depth + 1"
        :location-enabled="locationEnabled"
        @update:node="updateChildNode(child.id, $event)"
        @remove="removeChildNode(child.id)"
      />
    </div>

    <div v-else-if="node.type === 'array'" class="tool-schema-node-children">
      <div class="tool-schema-node-toolbar">
        <strong>数组元素</strong>
      </div>
      <ToolSchemaTreeNodeEditor
        :node="node.item || createChildNode('item')"
        :depth="depth + 1"
        :location-enabled="false"
        @update:node="updateItemNode"
        @remove="updateItemNode(createChildNode('item'))"
      />
    </div>
  </div>
</template>
