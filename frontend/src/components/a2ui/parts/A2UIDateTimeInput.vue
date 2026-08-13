<script setup lang="ts">
import { computed } from "vue";

import { useA2UIValues } from "../context";
import { A2UI_ENUMS, type A2UIComponentNode } from "../generated/catalog.gen";

type Mode = (typeof A2UI_ENUMS.DateTimeInput.mode)[number];

const INPUT_TYPES: Record<Mode, string> = {
  date: "date",
  time: "time",
  datetime: "datetime-local",
};

const props = defineProps<{ node: A2UIComponentNode }>();
const values = useA2UIValues();

const controlId = computed(() => values.controlId(props.node));
const label = computed(() => values.string(props.node.label));
const value = computed(() => values.string(props.node.value));
const type = computed(() => {
  const mode = props.node.mode;
  const known = (A2UI_ENUMS.DateTimeInput.mode as readonly string[]).includes(mode as string);
  return INPUT_TYPES[known ? (mode as Mode) : "date"];
});
</script>

<template>
  <div class="a2ui-field">
    <label class="a2ui-label" :for="controlId">{{ label }}</label>
    <input :id="controlId" class="a2ui-input" :type="type" :value="value" />
  </div>
</template>
