<script setup lang="ts">
import { computed } from "vue";

import { A2UI_ENUMS, type A2UIComponentNode } from "../generated/catalog.gen";

const props = defineProps<{ node: A2UIComponentNode }>();

/**
 * Only a value the catalog defines becomes a class, so the stylesheet needs a
 * rule for exactly this list and a sender cannot name a class of its own.
 */
function enumClass(prefix: string, allowed: readonly string[], value: unknown): string | undefined {
  return typeof value === "string" && allowed.includes(value) ? `${prefix}${value}` : undefined;
}

const classes = computed(() => [
  "a2ui-col",
  enumClass("a2ui-align-", A2UI_ENUMS.Column.align, props.node.align),
  enumClass("a2ui-justify-", A2UI_ENUMS.Column.justify, props.node.justify),
]);
</script>

<template>
  <div :class="classes">
    <slot />
  </div>
</template>
