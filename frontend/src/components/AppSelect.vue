<script setup lang="ts">
import { nextTick, onMounted, ref, watch } from "vue";

type AppSelectValue = string | number | boolean;

export interface AppSelectOption {
  label: string;
  value: AppSelectValue;
  disabled?: boolean;
}

const props = withDefaults(defineProps<{
  modelValue: AppSelectValue;
  options: AppSelectOption[];
  placeholder?: string;
  disabled?: boolean;
  compact?: boolean;
  filterable?: boolean;
  ariaLabel?: string;
  ariaRequired?: boolean;
  ariaInvalid?: boolean;
  ariaDescribedby?: string;
}>(), {
  placeholder: "请选择",
  disabled: false,
  compact: false,
  filterable: false,
  ariaLabel: undefined,
  ariaRequired: false,
  ariaInvalid: false,
  ariaDescribedby: undefined,
});

const emit = defineEmits<{
  "update:modelValue": [value: AppSelectValue];
}>();
const selectShellRef = ref<HTMLElement | null>(null);

function updateValue(value: AppSelectValue) {
  emit("update:modelValue", value);
}

async function syncComboboxAria() {
  await nextTick();
  const input = selectShellRef.value?.querySelector("input[role='combobox']");
  if (!input) return;
  if (props.ariaLabel) {
    input.setAttribute("aria-label", props.ariaLabel);
  }
  if (props.ariaRequired) {
    input.setAttribute("aria-required", "true");
  } else {
    input.removeAttribute("aria-required");
  }
  if (props.ariaInvalid) {
    input.setAttribute("aria-invalid", "true");
  } else {
    input.removeAttribute("aria-invalid");
  }
  if (props.ariaDescribedby) {
    input.setAttribute("aria-describedby", props.ariaDescribedby);
  } else {
    input.removeAttribute("aria-describedby");
  }
}

onMounted(() => {
  void syncComboboxAria();
});

watch(
  () => [props.ariaLabel, props.ariaRequired, props.ariaInvalid, props.ariaDescribedby, props.modelValue],
  () => {
    void syncComboboxAria();
  },
);
</script>

<template>
  <span ref="selectShellRef" class="app-select-accessibility-shell">
    <el-select
      class="app-select"
      :class="{ 'is-compact': compact }"
      :model-value="modelValue"
      :placeholder="placeholder"
      :disabled="disabled"
      :filterable="filterable"
      :aria-label="ariaLabel"
      :aria-required="ariaRequired"
      :aria-invalid="ariaInvalid"
      :aria-describedby="ariaDescribedby"
      popper-class="app-select-popper"
      @update:model-value="updateValue"
    >
      <el-option
        v-for="option in options"
        :key="option.value"
        :label="option.label"
        :value="option.value"
        :disabled="option.disabled"
      />
    </el-select>
  </span>
</template>
