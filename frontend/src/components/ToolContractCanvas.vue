<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";

import ToolContractHybridEditor from "./ToolContractHybridEditor.vue";
import ToolFlatContractEditor from "./ToolFlatContractEditor.vue";
import ToolSchemaInspectorTree from "./ToolSchemaInspectorTree.vue";
import type { ToolErrorMapping, ToolRequestParam, ToolSchemaNode } from "../types/domain";
import { buildBodyContractFromRequestParams, buildResponseContractFromFields } from "../utils/tool-schema-json";

const { t } = useI18n();

const props = withDefaults(
  defineProps<{
    mode: "view" | "edit";
    requestParams?: ToolRequestParam[];
    responseFields?: { name: string; type: string; description: string; schema?: ToolSchemaNode }[];
    requestContract?: ToolSchemaNode[];
    responseContract?: ToolSchemaNode[];
    errorMappings: ToolErrorMapping[];
    path?: string;
    embedded?: boolean;
  }>(),
  {
    requestParams: () => [],
    responseFields: () => [],
    requestContract: () => [],
    responseContract: () => [],
    path: "",
    embedded: false,
  },
);

const emit = defineEmits<{
  "update:requestContract": [value: ToolSchemaNode[]];
  "update:responseContract": [value: ToolSchemaNode[]];
  "update:errorMappings": [value: ToolErrorMapping[]];
}>();

type TransportLocation = "Path" | "Query" | "Header";

const viewSplit = computed(() => buildBodyContractFromRequestParams(props.requestParams || []));
const viewResponseNodes = computed(() => buildResponseContractFromFields(props.responseFields || []));

const transportNodes = computed(() =>
  (props.requestContract || []).filter((node) => ["Path", "Query", "Header"].includes(node.location || "")),
);
const bodyNodes = computed(() =>
  (props.requestContract || []).filter((node) => !["Path", "Query", "Header"].includes(node.location || "")),
);

const viewTransportByLocation = computed(() => ({
  Path: viewSplit.value.transportParams.filter((param) => param.location === "Path"),
  Query: viewSplit.value.transportParams.filter((param) => param.location === "Query"),
  Header: viewSplit.value.transportParams.filter((param) => param.location === "Header"),
}));

const hasAnyContract = computed(() => {
  if (props.mode === "view") {
    return Boolean(
      viewSplit.value.transportParams.length ||
      viewSplit.value.bodyNodes.length ||
      viewResponseNodes.value.length ||
      props.errorMappings.length,
    );
  }
  return Boolean(
    transportNodes.value.length ||
    bodyNodes.value.length ||
    props.responseContract.length ||
    props.errorMappings.length,
  );
});

function nodesForLocation(location: TransportLocation) {
  return transportNodes.value.filter((node) => node.location === location);
}

function setLocationNodes(location: TransportLocation, next: ToolSchemaNode[]) {
  emit("update:requestContract", [
    ...transportNodes.value.filter((node) => node.location !== location),
    ...next.map((node) => ({ ...node, location })),
    ...bodyNodes.value,
  ]);
}

function setBodyNodes(next: ToolSchemaNode[]) {
  emit("update:requestContract", [
    ...transportNodes.value,
    ...next.map((node) => ({ ...node, location: node.location || "Body" })),
  ]);
}

function setResponseNodes(next: ToolSchemaNode[]) {
  emit("update:responseContract", next);
}

function addErrorMapping() {
  emit("update:errorMappings", [...props.errorMappings, { protocolStatus: "", errorCode: "", agentAdvice: "" }]);
}

function removeErrorMapping(index: number) {
  emit(
    "update:errorMappings",
    props.errorMappings.filter((_, itemIndex) => itemIndex !== index),
  );
}

function updateErrorMapping(index: number, key: keyof ToolErrorMapping, value: string) {
  emit(
    "update:errorMappings",
    props.errorMappings.map((mapping, itemIndex) => (itemIndex === index ? { ...mapping, [key]: value } : mapping)),
  );
}

const bodyExpandAll = ref(false);
const responseExpandAll = ref(false);

function createBlankNode(location: ToolSchemaNode["location"]): ToolSchemaNode {
  return {
    id: `schema-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    name: "",
    type: "string",
    required: false,
    description: "",
    location,
    valueSource: "UserInput",
    children: [],
    item: null,
    additionalProperties: null,
  };
}

function addLocationField(location: TransportLocation) {
  setLocationNodes(location, [...nodesForLocation(location), createBlankNode(location)]);
}

function addBodyField() {
  setBodyNodes([...bodyNodes.value, createBlankNode("Body")]);
}

function addResponseField() {
  setResponseNodes([...props.responseContract, createBlankNode("Response")]);
}

function typeLabel(type: string) {
  const labels: Record<string, string> = {
    string: t("tools.typeString"),
    integer: t("tools.typeInteger"),
    number: t("tools.typeNumber"),
    boolean: t("tools.typeBoolean"),
    object: t("tools.typeObject"),
    array: t("tools.typeArray"),
  };
  return labels[type] || type || t("tools.typeString");
}

function hasNestedFields(nodes: ToolSchemaNode[]): boolean {
  return nodes.some(
    (node) => node.type === "object" || node.type === "array" || (node.children && node.children.length),
  );
}

const sections = computed(() => [
  {
    id: "path" as const,
    title: t("tools.sectionPath"),
    hint: t("tools.flatDescPath"),
    count: props.mode === "view" ? viewTransportByLocation.value.Path.length : nodesForLocation("Path").length,
    location: "Path" as const,
  },
  {
    id: "query" as const,
    title: t("tools.sectionQuery"),
    hint: t("tools.flatDescQuery"),
    count: props.mode === "view" ? viewTransportByLocation.value.Query.length : nodesForLocation("Query").length,
    location: "Query" as const,
  },
  {
    id: "header" as const,
    title: t("tools.sectionHeader"),
    hint: t("tools.flatDescHeader"),
    count: props.mode === "view" ? viewTransportByLocation.value.Header.length : nodesForLocation("Header").length,
    location: "Header" as const,
  },
]);
</script>

<template>
  <section
    class="tool-contract-canvas"
    :class="[mode, { embedded, empty: mode === 'view' && !hasAnyContract }]"
    :aria-label="t('tools.contract')"
  >
    <header v-if="!embedded" class="tool-contract-canvas-head">
      <div>
        <strong>{{ t("tools.contractTitle") }}</strong>
        <span>{{ t("tools.contractSubtitle") }}</span>
      </div>
      <small v-if="path" class="mono">{{ path }}</small>
    </header>

    <div v-if="mode === 'view' && !hasAnyContract" class="tool-contract-empty">
      <strong>{{ t("tools.contractEmpty") }}</strong>
      <span>{{ t("tools.contractEmptyHint") }}</span>
      <div v-if="$slots['empty-action']" class="tool-contract-empty-action">
        <slot name="empty-action" />
      </div>
    </div>

    <template v-else>
      <article
        v-for="section in sections"
        :key="section.id"
        v-show="mode === 'edit' || section.count > 0"
        class="tool-contract-section"
        :class="{ collapsed: mode === 'edit' && section.count === 0 }"
      >
        <header>
          <strong>{{ section.title }}</strong>
          <small>{{ t("tools.fieldCount", { n: section.count }) }}</small>
        </header>
        <ul v-if="mode === 'view'" class="tool-contract-params">
          <li v-for="param in viewTransportByLocation[section.location]" :key="`${section.location}-${param.name}`">
            <code>{{ param.name }}</code>
            <span class="tool-contract-type" :data-type="param.type || 'string'">{{ typeLabel(param.type) }}</span>
            <span class="tool-contract-req" :class="{ on: param.required }">{{
              param.required ? t("common.required") : t("common.optional")
            }}</span>
            <span class="tool-contract-desc" :class="{ empty: !param.description }">{{
              param.description || "—"
            }}</span>
          </li>
        </ul>
        <button
          v-else-if="section.count === 0"
          class="tool-contract-add"
          type="button"
          @click="addLocationField(section.location)"
        >
          <i class="fa-solid fa-plus" />
          {{
            section.location === "Path"
              ? t("tools.addPathParams")
              : section.location === "Query"
                ? t("tools.addQueryParams")
                : t("tools.addHeaderParams")
          }}
        </button>
        <ToolFlatContractEditor
          v-else
          :model-value="nodesForLocation(section.location)"
          :location="section.location"
          @update:model-value="setLocationNodes(section.location, $event)"
        />
      </article>

      <article
        v-show="mode === 'edit' || (mode === 'view' && viewSplit.bodyNodes.length)"
        class="tool-contract-section"
        :class="{ collapsed: mode === 'edit' && !bodyNodes.length }"
      >
        <header>
          <strong>{{ t("tools.sectionBody") }}</strong>
          <small>{{
            t("tools.fieldCount", { n: mode === "view" ? viewSplit.bodyNodes.length : bodyNodes.length })
          }}</small>
          <button
            v-if="mode === 'view' && hasNestedFields(viewSplit.bodyNodes)"
            type="button"
            @click="bodyExpandAll = !bodyExpandAll"
          >
            {{ bodyExpandAll ? t("tools.collapseNested") : t("tools.expandAllFields") }}
          </button>
        </header>
        <p v-if="mode === 'edit' && bodyNodes.length">{{ t("tools.requestBodyDesc") }}</p>
        <ToolSchemaInspectorTree
          v-if="mode === 'view'"
          :key="`body-${bodyExpandAll}`"
          :nodes="viewSplit.bodyNodes"
          root-label="Body"
          :expand-all="bodyExpandAll"
          :empty-text="t('tools.noRequestBody')"
        />
        <button v-else-if="!bodyNodes.length" class="tool-contract-add" type="button" @click="addBodyField">
          <i class="fa-solid fa-plus" /> {{ t("tools.addRequestBody") }}
        </button>
        <ToolContractHybridEditor
          v-else
          :model-value="bodyNodes"
          :title="t('tools.requestBodyTitle')"
          :description="t('tools.requestBodyDesc')"
          root-label="Body"
          compact
          @update:model-value="setBodyNodes"
        />
      </article>

      <article
        v-show="mode === 'edit' || (mode === 'view' && viewResponseNodes.length)"
        class="tool-contract-section"
        :class="{ collapsed: mode === 'edit' && !responseContract.length }"
      >
        <header>
          <strong>{{ t("tools.sectionResponse") }}</strong>
          <small>{{
            t("tools.fieldCount", { n: mode === "view" ? viewResponseNodes.length : responseContract.length })
          }}</small>
          <button
            v-if="mode === 'view' && hasNestedFields(viewResponseNodes)"
            type="button"
            @click="responseExpandAll = !responseExpandAll"
          >
            {{ responseExpandAll ? t("tools.collapseNested") : t("tools.expandAllFields") }}
          </button>
        </header>
        <p v-if="mode === 'edit' && responseContract.length">{{ t("tools.successResponseDesc") }}</p>
        <ToolSchemaInspectorTree
          v-if="mode === 'view'"
          :key="`response-${responseExpandAll}`"
          :nodes="viewResponseNodes"
          root-label="Response"
          :expand-all="responseExpandAll"
          :empty-text="t('tools.noResponseStructure')"
        />
        <button v-else-if="!responseContract.length" class="tool-contract-add" type="button" @click="addResponseField">
          <i class="fa-solid fa-plus" /> {{ t("tools.addResponseFields") }}
        </button>
        <ToolContractHybridEditor
          v-else
          :model-value="responseContract"
          :title="t('tools.successResponseTitle')"
          :description="t('tools.successResponseDesc')"
          root-label="Response"
          compact
          @update:model-value="setResponseNodes"
        />
      </article>

      <article v-show="mode === 'edit' || errorMappings.length" class="tool-contract-section">
        <header>
          <strong>{{ t("tools.sectionErrors") }}</strong>
          <small>{{ t("tools.fieldCount", { n: errorMappings.length }) }}</small>
          <button v-if="mode === 'edit'" type="button" @click="addErrorMapping">
            <i class="fa-solid fa-plus" /> {{ t("tools.addMapping") }}
          </button>
        </header>
        <p v-if="mode === 'edit'">{{ t("tools.errorMappingDesc") }}</p>
        <div v-if="errorMappings.length" class="tool-error-mapping-table">
          <div class="tool-error-mapping-row tool-error-mapping-header">
            <span>HTTP Status</span>
            <span>Error Code</span>
            <span>{{ t("tools.agentAdvice") }}</span>
            <span v-if="mode === 'edit'">{{ t("tools.actions") }}</span>
          </div>
          <div
            v-for="(mapping, index) in errorMappings"
            :key="`${index}-${mapping.errorCode}`"
            class="tool-error-mapping-row"
          >
            <template v-if="mode === 'view'">
              <span>{{ mapping.protocolStatus || "—" }}</span>
              <span class="mono">{{ mapping.errorCode || "—" }}</span>
              <span>{{ mapping.agentAdvice || "—" }}</span>
            </template>
            <template v-else>
              <input
                :value="mapping.protocolStatus"
                inputmode="numeric"
                aria-label="HTTP Status"
                placeholder="409"
                @input="updateErrorMapping(index, 'protocolStatus', ($event.target as HTMLInputElement).value)"
              />
              <input
                :value="mapping.errorCode"
                class="mono"
                aria-label="Error Code"
                placeholder="STATE_LOCKED"
                @input="updateErrorMapping(index, 'errorCode', ($event.target as HTMLInputElement).value)"
              />
              <input
                :value="mapping.agentAdvice"
                :aria-label="t('tools.agentAdvice')"
                :placeholder="t('tools.agentAdvicePlaceholder')"
                @input="updateErrorMapping(index, 'agentAdvice', ($event.target as HTMLInputElement).value)"
              />
              <button
                class="tool-flat-delete"
                type="button"
                :aria-label="t('tools.deleteErrorMappingAria', { name: mapping.errorCode || index + 1 })"
                @click="removeErrorMapping(index)"
              >
                <i class="fa-solid fa-xmark" />
              </button>
            </template>
          </div>
        </div>
        <div v-else class="tool-schema-empty">{{ t("tools.noErrorMappings") }}</div>
      </article>
    </template>
  </section>
</template>

<style scoped>
.tool-contract-canvas {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 16px;
}
.tool-contract-canvas-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 16px;
}
.tool-contract-canvas-head strong,
.tool-contract-canvas-head span {
  display: block;
}
.tool-contract-canvas-head strong {
  color: #0f172a;
  font-size: 16px;
  font-weight: 700;
}
.tool-contract-canvas-head span {
  margin-top: 4px;
  color: #64748b;
  font-size: 13px;
  line-height: 1.5;
}
.tool-contract-canvas-head .mono {
  color: #64748b;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
}
.tool-contract-canvas.embedded {
  gap: 10px;
}
.tool-contract-empty {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 16px;
  padding: 14px 16px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
}
.tool-contract-empty strong {
  color: #0f172a;
  font-size: 13px;
  font-weight: 700;
}
.tool-contract-empty span {
  flex: 1 1 16rem;
  max-width: 40rem;
  color: #64748b;
  font-size: 13px;
  line-height: 1.5;
}
.tool-contract-empty-action {
  margin-left: auto;
}
.tool-contract-section {
  min-width: 0;
  padding: 14px 16px 16px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
}
.tool-contract-section.collapsed {
  padding-bottom: 14px;
}
.tool-contract-params {
  display: grid;
  gap: 0;
  margin: 10px 0 0;
  padding: 0;
  list-style: none;
  border: 1px solid #eef2f7;
  border-radius: 10px;
  overflow: hidden;
}
.tool-contract-params li {
  display: grid;
  grid-template-columns: minmax(6.5rem, 14rem) 5.5rem 3.5rem minmax(0, 1fr);
  gap: 10px 12px;
  align-items: center;
  padding: 8px 12px;
  border-top: 1px solid #f1f5f9;
}
.tool-contract-params li:first-child {
  border-top: 0;
}
.tool-contract-params code {
  min-width: 0;
  color: #0f172a;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12.5px;
  font-weight: 700;
}
.tool-contract-type,
.tool-contract-req {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 22px;
  padding: 0 8px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 600;
  white-space: nowrap;
}
.tool-contract-type {
  border: 1px solid #e2e8f0;
  background: #f8fafc;
  color: #475569;
}
.tool-contract-req {
  border: 1px solid #e2e8f0;
  background: #f8fafc;
  color: #94a3b8;
}
.tool-contract-req.on {
  border-color: #fed7aa;
  background: #fff7ed;
  color: #c2410c;
  font-weight: 700;
}
.tool-contract-desc {
  min-width: 0;
  color: #475569;
  font-size: 12px;
  line-height: 1.45;
}
.tool-contract-desc.empty {
  color: #cbd5e1;
}
.tool-contract-add {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 36px;
  margin-top: 10px;
  padding: 0 12px;
  border: 1px dashed #dbe3ee;
  border-radius: 8px;
  background: #fff;
  color: #475569;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
}
.tool-contract-add:hover {
  border-color: rgb(13 148 136 / 0.45);
  color: #0f766e;
}
@media (max-width: 720px) {
  .tool-contract-empty {
    flex-direction: column;
    align-items: flex-start;
  }
  .tool-contract-empty-action {
    margin-left: 0;
  }
  .tool-contract-params li {
    grid-template-columns: minmax(0, 1fr) auto auto;
    grid-template-areas:
      "name type req"
      "desc desc desc";
  }
  .tool-contract-params code {
    grid-area: name;
  }
  .tool-contract-desc {
    grid-area: desc;
  }
}
.tool-contract-section > header {
  display: flex;
  align-items: center;
  gap: 10px;
}
.tool-contract-section > header strong {
  color: #0f172a;
  font-size: 14px;
  font-weight: 700;
}
.tool-contract-section > header small {
  color: #94a3b8;
  font-size: 12px;
}
.tool-contract-section > header button {
  margin-left: auto;
  min-height: 32px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0 10px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  color: #475569;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
}
.tool-contract-section > p {
  margin: 6px 0 14px;
  color: #64748b;
  font-size: 12px;
  line-height: 1.5;
}
.tool-error-mapping-table {
  overflow: hidden;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
}
.tool-error-mapping-row {
  display: grid;
  grid-template-columns: 120px 160px minmax(0, 1fr) 40px;
  gap: 8px;
  align-items: center;
  padding: 8px 10px;
  border-bottom: 1px solid #f1f5f9;
}
.tool-error-mapping-row:last-child {
  border-bottom: 0;
}
.tool-error-mapping-header {
  background: #f8fafc;
  color: #64748b;
  font-size: 11px;
  font-weight: 700;
}
.tool-error-mapping-row input {
  width: 100%;
  min-height: 36px;
  padding: 6px 8px;
  border: 1px solid transparent;
  border-radius: 6px;
  background: transparent;
  color: #0f172a;
  font: inherit;
  font-size: 13px;
}
.tool-error-mapping-row input:focus {
  outline: 0;
  border-color: rgb(13 148 136 / 0.55);
  background: #fff;
}
.tool-flat-delete {
  border: 0;
  background: transparent;
  color: #94a3b8;
  cursor: pointer;
}
.tool-schema-empty {
  padding: 28px 0;
  color: #94a3b8;
  font-size: 12px;
  text-align: center;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
</style>
