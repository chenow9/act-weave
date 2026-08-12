<script setup lang="ts">
import { computed } from "vue";

import { A2UI_ENUMS, type A2UIComponentNode } from "../generated/catalog.gen";

const props = defineProps<{ node: A2UIComponentNode }>();

function enumClass(prefix: string, allowed: readonly string[], value: unknown): string | undefined {
  return typeof value === "string" && allowed.includes(value) ? `${prefix}${value}` : undefined;
}

const classes = computed(() => [
  "a2ui-row",
  enumClass("a2ui-align-", A2UI_ENUMS.Row.align, props.node.align),
  enumClass("a2ui-justify-", A2UI_ENUMS.Row.justify, props.node.justify),
]);
</script>

<template>
  <div :class="classes">
    <slot />
  </div>
</template>
