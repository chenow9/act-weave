<script setup lang="ts">
import { computed } from "vue";

import { useA2UIValues } from "../context";
import type { A2UIComponentNode } from "../generated/catalog.gen";

const props = defineProps<{ node: A2UIComponentNode }>();
const values = useA2UIValues();

const title = computed(() => values.string(props.node.title));
</script>

<template>
  <div class="a2ui-panel">
    <div v-if="title" class="a2ui-panel-title">{{ title }}</div>
    <slot />
  </div>
</template>
