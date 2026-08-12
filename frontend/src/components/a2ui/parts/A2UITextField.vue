<script setup lang="ts">
/**
 * Fields are fillable so a surface feels like a real form, but nothing is ever
 * submitted: the catalog defines no action and AAP advertises actions:false.
 */
import { computed } from "vue";

import { useA2UIValues } from "../context";
import { A2UI_ENUMS, type A2UIComponentNode } from "../generated/catalog.gen";

type Variant = (typeof A2UI_ENUMS.TextField.variant)[number];

// A total record: adding a variant to the catalog fails type-check here rather
// than silently falling back to a plain text box.
const INPUT_TYPES: Record<Variant, string> = {
  shortText: "text",
  longText: "textarea",
  number: "number",
  email: "email",
  tel: "tel",
  date: "date",
  password: "password",
};

const props = defineProps<{ node: A2UIComponentNode }>();
const values = useA2UIValues();

const controlId = computed(() => values.controlId(props.node));
const label = computed(() => values.string(props.node.label));
const placeholder = computed(() => values.string(props.node.placeholder));
const value = computed(() => values.string(props.node.value));
const required = computed(() => props.node.required === true);
const type = computed(() => {
  const variant = props.node.variant;
  const known = (A2UI_ENUMS.TextField.variant as readonly string[]).includes(variant as string);
  return INPUT_TYPES[known ? (variant as Variant) : "shortText"];
});
</script>

<template>
  <div class="a2ui-field">
    <label class="a2ui-label" :for="controlId">
      {{ label }}<span v-if="required" class="a2ui-req" aria-hidden="true">*</span>
    </label>
    <textarea
      v-if="type === 'textarea'"
      :id="controlId"
      class="a2ui-input"
      rows="3"
      :placeholder="placeholder"
      :required="required"
      :value="value"
    />
    <input
      v-else
      :id="controlId"
      class="a2ui-input"
      :type="type"
      :value="value"
      :placeholder="placeholder"
      :required="required"
    />
  </div>
</template>
