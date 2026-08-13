<script setup lang="ts">
import { computed } from "vue";

import { useA2UIValues } from "../context";
import type { A2UIComponentNode } from "../generated/catalog.gen";

const props = defineProps<{ node: A2UIComponentNode }>();
const values = useA2UIValues();

const controlId = computed(() => values.controlId(props.node));
const label = computed(() => values.string(props.node.label));
const checked = computed(() => values.boolean(props.node.value));
</script>

<template>
  <div class="a2ui-field a2ui-field-check">
    <label class="a2ui-check-label" :for="controlId">
      <input :id="controlId" type="checkbox" :checked="checked" />
      <span>{{ label }}</span>
    </label>
  </div>
</template>
