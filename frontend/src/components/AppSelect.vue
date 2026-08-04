<script setup lang="ts">
import { ElOption, ElSelect } from "element-plus";
import type { Placement } from "element-plus";
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";

type AppSelectValue = string | number | boolean;

export interface AppSelectOption {
  label: string;
  value: AppSelectValue;
  disabled?: boolean;
}

const { t } = useI18n();

const props = withDefaults(
  defineProps<{
    modelValue: AppSelectValue;
    options: AppSelectOption[];
    placeholder?: string;
    disabled?: boolean;
    compact?: boolean;
    filterable?: boolean;
    /** Keep dropdown width equal to the trigger (avoids wide menus shifting left over the canvas). */
    fitInputWidth?: boolean;
    /** Popper placement; must match Element Plus Placement (not a free-form string). */
    placement?: Placement;
    ariaLabel?: string;
    ariaRequired?: boolean;
    ariaInvalid?: boolean;
    ariaDescribedby?: string;
  }>(),
  {
    placeholder: undefined,
    disabled: false,
    compact: false,
    filterable: false,
    fitInputWidth: true,
    placement: "bottom-start",
    ariaLabel: undefined,
    ariaRequired: false,
    ariaInvalid: false,
    ariaDescribedby: undefined,
  },
);

const resolvedPlaceholder = computed(() => props.placeholder ?? t("common.pleaseSelect"));

const emit = defineEmits<{
  "update:modelValue": [value: AppSelectValue];
}>();
const selectShellRef = ref<HTMLElement | null>(null);

/** Stable reference — inline object literals re-created every render reset el-select display state. */
const selectPopperOptions = {
  strategy: "fixed" as const,
  modifiers: [
    { name: "offset", options: { offset: [0, 4] } },
    {
      name: "preventOverflow",
      options: { boundary: "viewport", padding: 8, altAxis: true },
    },
    {
      name: "flip",
      options: { fallbackPlacements: ["top-start", "bottom-end", "top-end"] },
    },
  ],
};

function updateValue(value: AppSelectValue) {
  emit("update:modelValue", value);
}

/** Empty string is not a valid option value — map to undefined so placeholder can show. */
function normalizeModelValue(value: AppSelectValue) {
  if (value === "" || value === null || value === undefined) return undefined;
  return value;
}

const normalizedValue = computed(() => normalizeModelValue(props.modelValue));

/** Resolve label for the current value (fallback when EP cached option is empty). */
const selectedOptionLabel = computed(() => {
  const value = normalizedValue.value;
  if (value === undefined) return "";
  const match = props.options.find((option) => option.value === value);
  return match?.label?.trim() || String(value);
});

/**
 * Remount select when the matching option first becomes available so EP
 * re-runs setSelected() and paints the label after async catalog load.
 */
const selectRemountKey = computed(() => {
  const value = normalizedValue.value;
  if (value === undefined) return "empty";
  const hasOption = props.options.some((option) => option.value === value);
  return `${String(value)}:${hasOption ? "ready" : "pending"}:${props.options.length}`;
});

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
      :key="selectRemountKey"
      class="app-select"
      :class="{ 'is-compact': compact }"
      :model-value="normalizedValue"
      :placeholder="resolvedPlaceholder"
      :disabled="disabled"
      :filterable="filterable"
      :fit-input-width="fitInputWidth"
      :placement="placement"
      teleported
      :popper-options="selectPopperOptions"
      :aria-label="ariaLabel"
      :aria-required="ariaRequired"
      :aria-invalid="ariaInvalid"
      :aria-describedby="ariaDescribedby"
      popper-class="app-select-popper"
      @update:model-value="updateValue"
    >
      <!-- Explicit label slot — more reliable than EP's z-index:-1 placeholder paint -->
      <template #label="{ label }">
        <span class="app-select-label-text" :title="label || selectedOptionLabel">
          {{ label || selectedOptionLabel }}
        </span>
      </template>
      <el-option
        v-for="option in options"
        :key="String(option.value)"
        :label="option.label"
        :value="option.value"
        :disabled="option.disabled"
        :title="option.label"
      />
    </el-select>
  </span>
</template>
