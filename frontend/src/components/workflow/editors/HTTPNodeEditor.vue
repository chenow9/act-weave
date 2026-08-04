<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";

import AppSelect from "../../AppSelect.vue";
import type { WorkflowGraphNode } from "../../../types/domain";

const { t } = useI18n();

type MappingValue = { kind: "ref"; path: string } | { kind: "literal"; value: unknown };

const props = defineProps<{
  node: WorkflowGraphNode;
  variableRefs: string[];
}>();

const emit = defineEmits<{
  (event: "update-node-data", payload: { key: string; value: unknown }): void;
}>();

const methodOptions = ["GET", "POST", "PUT", "PATCH", "DELETE"].map((value) => ({ label: value, value }));
const variableOptions = computed(() =>
  props.variableRefs
    .map((value) => value.replace(/^\{\{/, "").replace(/\}\}$/, ""))
    .filter(Boolean)
    .map((value) => ({ label: value, value })),
);
const inputMode = ref<"mapping" | "json">("mapping");
const mappingDraft = ref<Array<{ key: string; kind: MappingValue["kind"]; path: string; value: string }>>([]);
const rawInputText = ref("{}");

watch(
  () => [props.node.data.input, props.node.data.inputMapping] as const,
  ([rawInput, rawMapping]) => {
    inputMode.value = rawInput && typeof rawInput === "object" && !Array.isArray(rawInput) ? "json" : "mapping";
    rawInputText.value = JSON.stringify(
      rawInput && typeof rawInput === "object" && !Array.isArray(rawInput) ? rawInput : {},
      null,
      2,
    );
    const nextDraft = normalizeMappingDraft(rawMapping);
    mappingDraft.value = nextDraft.length ? nextDraft : [{ key: "value", kind: "ref", path: "", value: "" }];
  },
  { immediate: true, deep: true },
);

function nodeString(key: string) {
  const value = props.node.data[key];
  return typeof value === "string" ? value : "";
}

function mappingEntries() {
  return mappingDraft.value;
}

function mappingValue(key: string): MappingValue | undefined {
  const row = mappingDraft.value.find((entry) => entry.key === key);
  if (!row) return undefined;
  return row.kind === "ref" ? { kind: "ref", path: row.path } : { kind: "literal", value: row.value };
}

function mappingKind(key: string) {
  return mappingValue(key)?.kind || "ref";
}

function mappingLiteralValue(key: string) {
  const value = mappingValue(key);
  if (!value || value.kind !== "literal") {
    return "";
  }
  return typeof value.value === "string" ? value.value : value.value == null ? "" : JSON.stringify(value.value);
}

function mappingRefValue(key: string) {
  const value = mappingValue(key);
  return value?.kind === "ref" ? value.path : "";
}

function updateInputMode(mode: string) {
  inputMode.value = mode === "json" ? "json" : "mapping";
  if (mode === "json") {
    emit("update-node-data", {
      key: "__merge",
      value: {
        input: currentRawInput(),
        inputMapping: undefined,
      },
    });
    return;
  }
  emit("update-node-data", {
    key: "__merge",
    value: {
      input: undefined,
      inputMapping: normalizeInputMapping(),
    },
  });
}

function currentRawInput() {
  const rawInput = props.node.data.input;
  return rawInput && typeof rawInput === "object" && !Array.isArray(rawInput) ? rawInput : {};
}

function updateRawInput(value: string) {
  rawInputText.value = value;
  try {
    const parsed = JSON.parse(value);
    emit("update-node-data", {
      key: "__merge",
      value: {
        input: parsed,
        inputMapping: undefined,
      },
    });
  } catch {
    // Ignore invalid JSON drafts while typing.
  }
}

function renameMappingKey(index: number, nextKey: string) {
  const previousKey = mappingDraft.value[index]?.key;
  if (!previousKey || !nextKey || previousKey === nextKey) {
    return;
  }
  mappingDraft.value = mappingDraft.value.map((entry, currentIndex) =>
    currentIndex === index
      ? {
          ...entry,
          key: nextKey,
        }
      : entry,
  );
  emitInputMapping();
}

function setMappingKind(key: string, kind: MappingValue["kind"]) {
  mappingDraft.value = mappingDraft.value.map((entry) =>
    entry.key === key
      ? {
          ...entry,
          kind,
          path: kind === "ref" ? entry.path : "",
          value: kind === "literal" ? entry.value : "",
        }
      : entry,
  );
  emitInputMapping();
}

function setRefMapping(key: string, path: string) {
  mappingDraft.value = mappingDraft.value.map((entry) => (entry.key === key ? { ...entry, kind: "ref", path } : entry));
  emitInputMapping();
}

function setLiteralMapping(key: string, value: string) {
  mappingDraft.value = mappingDraft.value.map((entry) =>
    entry.key === key ? { ...entry, kind: "literal", value } : entry,
  );
  emitInputMapping();
}

function addMappingRow() {
  let index = mappingDraft.value.length + 1;
  let key = `field${index}`;
  const existingKeys = new Set(mappingDraft.value.map((entry) => entry.key));
  while (existingKeys.has(key)) {
    index += 1;
    key = `field${index}`;
  }
  mappingDraft.value = [...mappingDraft.value, { key, kind: "ref", path: "", value: "" }];
  emitInputMapping();
}

function removeMappingRow(key: string) {
  mappingDraft.value = mappingDraft.value.filter((entry) => entry.key !== key);
  if (!mappingDraft.value.length) {
    mappingDraft.value = [{ key: "value", kind: "ref", path: "", value: "" }];
  }
  emitInputMapping();
}

function emitInputMapping() {
  emit("update-node-data", {
    key: "__merge",
    value: {
      inputMapping: normalizeInputMapping(),
    },
  });
}

function normalizeInputMapping() {
  const entries: Array<[string, MappingValue]> = [];
  for (const entry of mappingDraft.value) {
    if (!entry.key.trim()) {
      continue;
    }
    if (entry.kind === "ref") {
      if (entry.path.trim()) {
        entries.push([entry.key, { kind: "ref", path: entry.path }]);
      }
      continue;
    }
    entries.push([entry.key, { kind: "literal", value: entry.value }]);
  }
  return Object.fromEntries(entries);
}

function normalizeMappingDraft(rawMapping: unknown) {
  if (!rawMapping || typeof rawMapping !== "object" || Array.isArray(rawMapping)) {
    return [];
  }
  return Object.entries(rawMapping as Record<string, unknown>).map(([key, value]) => {
    if (!value || typeof value !== "object" || Array.isArray(value)) {
      return { key, kind: "ref" as const, path: "", value: "" };
    }
    const record = value as Record<string, unknown>;
    if (record.kind === "literal") {
      return {
        key,
        kind: "literal" as const,
        path: "",
        value:
          typeof record.value === "string" ? record.value : record.value == null ? "" : JSON.stringify(record.value),
      };
    }
    return {
      key,
      kind: "ref" as const,
      path: typeof record.path === "string" ? record.path : "",
      value: "",
    };
  });
}
</script>

<template>
  <section class="workflow-http-node-editor">
    <label class="drawer-field">
      <span>{{ t("workflow.httpMethod") }}</span>
      <AppSelect
        :model-value="nodeString('method')"
        :options="methodOptions"
        :placeholder="t('workflow.selectHttpMethod')"
        @update:model-value="emit('update-node-data', { key: 'method', value: String($event) })"
      />
    </label>

    <label class="drawer-field">
      <span>{{ t("workflow.httpEndpoint") }}</span>
      <input
        name="node-http-endpoint"
        :value="nodeString('endpoint')"
        placeholder="https://api.example.com/orders"
        @input="emit('update-node-data', { key: 'endpoint', value: ($event.target as HTMLInputElement).value })"
      />
    </label>

    <section class="workflow-inspector-vars">
      <div class="workflow-section-caption">
        <strong>{{ t("workflow.inputBinding") }}</strong>
        <small>{{ t("workflow.inputBindingHint") }}</small>
      </div>
      <div class="workflow-tool-mapping-mode" role="group" :aria-label="t('workflow.httpInputModeAria')">
        <button
          type="button"
          data-action="http-input-mode-mapping"
          :class="{ active: inputMode === 'mapping' }"
          @click="updateInputMode('mapping')"
        >
          {{ t("workflow.variableMapping") }}
        </button>
        <button
          type="button"
          data-action="http-input-mode-json"
          :class="{ active: inputMode === 'json' }"
          @click="updateInputMode('json')"
        >
          {{ t("workflow.jsonInput") }}
        </button>
      </div>

      <div v-if="inputMode === 'mapping'" class="workflow-tool-param-list workflow-advanced-input-list">
        <article
          v-for="(entry, index) in mappingEntries()"
          :key="`${entry.key}-${index}`"
          class="workflow-tool-param-row"
          :data-entry-key="entry.key"
        >
          <label class="drawer-field">
            <span>{{ t("workflow.inputField") }}</span>
            <input
              :name="`http-input-key-${index}`"
              :value="entry.key"
              :placeholder="t('workflow.fieldKeyPh')"
              @input="renameMappingKey(index, ($event.target as HTMLInputElement).value)"
            />
          </label>
          <div class="workflow-tool-mapping-mode" role="group" :aria-label="t('workflow.mappingModeAria')">
            <button
              type="button"
              :class="{ active: mappingKind(entry.key) === 'ref' }"
              @click="setMappingKind(entry.key, 'ref')"
            >
              {{ t("workflow.mapVariable") }}
            </button>
            <button
              type="button"
              :class="{ active: mappingKind(entry.key) === 'literal' }"
              @click="setMappingKind(entry.key, 'literal')"
            >
              {{ t("workflow.mapLiteral") }}
            </button>
          </div>
          <AppSelect
            v-if="mappingKind(entry.key) === 'ref'"
            class="workflow-advanced-input-select"
            :model-value="mappingRefValue(entry.key)"
            :options="variableOptions"
            :placeholder="t('workflow.selectVariable')"
            @update:model-value="setRefMapping(entry.key, String($event))"
          />
          <input
            v-else
            :name="`http-input-literal-${index}`"
            :value="mappingLiteralValue(entry.key)"
            :placeholder="t('workflow.enterLiteral')"
            @input="setLiteralMapping(entry.key, ($event.target as HTMLInputElement).value)"
          />
          <button type="button" class="ghost-button" @click="removeMappingRow(entry.key)">
            {{ t("workflow.deleteFieldSimple") }}
          </button>
        </article>
        <button type="button" class="ghost-button" @click="addMappingRow">{{ t("workflow.addFieldSimple") }}</button>
      </div>

      <label v-else class="drawer-field">
        <span>{{ t("workflow.jsonInput") }}</span>
        <textarea
          name="node-http-input-json"
          rows="6"
          :value="rawInputText"
          spellcheck="false"
          @input="updateRawInput(($event.target as HTMLTextAreaElement).value)"
        />
      </label>
    </section>

    <section class="workflow-inspector-vars workflow-schema-preview">
      <div class="workflow-section-caption">
        <strong>{{ t("workflow.runtimeSemantics") }}</strong>
        <small>HTTP runtime summary</small>
      </div>
      <p>{{ t("workflow.httpRuntimeHint") }}</p>
    </section>
  </section>
</template>
