<script setup lang="ts">
/**
 * Buttons are display-only. The disabled state and the title are the honest
 * signal that pressing one can do nothing: the catalog has no action property,
 * so a surface cannot request a callback.
 */
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import { useA2UIValues } from "../context";
import type { A2UIComponentNode } from "../generated/catalog.gen";

const props = defineProps<{ node: A2UIComponentNode }>();
const values = useA2UIValues();
const { t } = useI18n();

const label = computed(() => values.string(props.node.label));
const classes = computed(() => [
  "a2ui-btn",
  {
    "a2ui-btn-primary": props.node.variant === "primary",
    "a2ui-btn-borderless": props.node.variant === "borderless",
  },
]);
</script>

<template>
  <div class="a2ui-field a2ui-field-action">
    <button type="button" :class="classes" disabled data-a2ui-action :title="t('a2ui.actionDisabled')">
      {{ label }}
    </button>
  </div>
</template>
