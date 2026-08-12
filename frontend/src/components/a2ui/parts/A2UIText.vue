<script setup lang="ts">
import { computed } from "vue";

import { useA2UIValues } from "../context";
import type { A2UIComponentNode } from "../generated/catalog.gen";

const props = defineProps<{ node: A2UIComponentNode }>();
const values = useA2UIValues();

const text = computed(() => values.string(props.node.text));
const variant = computed(() => props.node.variant);
</script>

<!-- Interpolated, never v-html: the catalog defines Text as plain text, and
     interpreting it would turn agent output into markup. -->
<template>
  <h3 v-if="variant === 'heading'" class="a2ui-title">{{ text }}</h3>
  <p v-else :class="['a2ui-text', { 'a2ui-text-caption': variant === 'caption' }]">{{ text }}</p>
</template>
