<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import { useA2UIValues } from "../context";
import type { A2UIComponentNode } from "../generated/catalog.gen";

const props = defineProps<{ node: A2UIComponentNode }>();
const values = useA2UIValues();
const { t } = useI18n();

const controlId = computed(() => values.controlId(props.node));
const label = computed(() => values.string(props.node.label));
const multiple = computed(() => props.node.multiple === true);
const selected = computed(() => new Set(values.choices(props.node.value)));

// Malformed options are dropped rather than rendered blank, so the list never
// offers a choice the sender did not describe.
const options = computed(() => {
  const raw = Array.isArray(props.node.options) ? props.node.options : [];
  return raw.flatMap((option) => {
    if (typeof option !== "object" || option === null) return [];
    const { value, label: optionLabel } = option as Record<string, unknown>;
    if (typeof value !== "string" || typeof optionLabel !== "string") return [];
    return [{ value, label: optionLabel }];
  });
});
</script>

<template>
  <div class="a2ui-field">
    <label class="a2ui-label" :for="controlId">{{ label }}</label>
    <select :id="controlId" class="a2ui-input" :multiple="multiple" :size="multiple ? 4 : undefined">
      <option v-if="!multiple" value="">{{ t("a2ui.choosePlaceholder") }}</option>
      <option v-if="!options.length" value="" disabled>{{ t("a2ui.noOptions") }}</option>
      <option
        v-for="option in options"
        :key="option.value"
        :value="option.value"
        :selected="selected.has(option.value)"
      >
        {{ option.label }}
      </option>
    </select>
  </div>
</template>
