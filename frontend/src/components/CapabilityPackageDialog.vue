<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";

import {
  importCapabilityPackage,
  packageErrorMessage,
  previewCapabilityPackage,
  type ConnectionBinding,
  type PackagePreview,
} from "../services/capability-package";
import { useConnectionsStore } from "../stores/connections";
import { useProvidersStore } from "../stores/providers";
import AppSelect from "./AppSelect.vue";
import ManagementDialog from "./ManagementDialog.vue";

const props = defineProps<{
  open: boolean;
  workspaceId: string;
}>();

const emit = defineEmits<{
  (event: "update:open", value: boolean): void;
  (event: "imported"): void;
}>();

const { t } = useI18n();
const connectionsStore = useConnectionsStore();
const providersStore = useProvidersStore();
const fileInputRef = ref<HTMLInputElement | null>(null);
const yamlText = ref("");
const fileName = ref("");
const preview = ref<PackagePreview | null>(null);
const busy = ref<"preview" | "import" | "">("");
const errorMessage = ref("");
const remaps = ref<Record<string, string>>({});
const dragging = ref(false);
let dragDepth = 0;

const canImport = computed(() => Boolean(preview.value?.canImport) && !busy.value);
const remapKeys = computed(() => {
  const keys: string[] = [];
  const seen = new Set<string>();
  for (const item of preview.value?.items || []) {
    if (item.kind !== "tool" || item.action !== "blocked") continue;
    const key = `${item.provider || ""}::${item.connection || ""}`;
    if (seen.has(key)) continue;
    seen.add(key);
    keys.push(key);
  }
  return keys;
});

const connectionOptions = computed(() =>
  (connectionsStore.serviceConnections || []).map((item) => {
    const providerName = providersStore.providers.find((provider) => provider.id === item.providerId)?.name;
    const name = item.name || item.alias || item.id;
    return {
      label: providerName ? `${providerName} / ${name}` : name,
      value: item.id,
    };
  }),
);

watch(
  () => props.open,
  (open) => {
    if (!open) return;
    yamlText.value = "";
    fileName.value = "";
    preview.value = null;
    errorMessage.value = "";
    remaps.value = {};
    busy.value = "";
    dragging.value = false;
    dragDepth = 0;
    if (props.workspaceId) {
      void connectionsStore.loadServiceConnectionCatalog();
    }
  },
);

function close() {
  emit("update:open", false);
}

function actionLabel(action: string) {
  if (action === "create") return t("packages.actionCreate");
  if (action === "update-draft") return t("packages.actionUpdateDraft");
  if (action === "blocked") return t("packages.actionBlocked");
  if (action === "rejected") return t("packages.actionRejected");
  return action;
}

function kindLabel(kind: string) {
  return kind === "workflow" ? t("packages.kindWorkflow") : t("packages.kindTool");
}

function itemDetail(item: PackagePreview["items"][number]) {
  return reasonLabel(item.reason) || item.provider || "";
}

function reasonLabel(reason: string | undefined) {
  if (!reason) return "";
  if (reason.startsWith("provider ") && reason.includes("was not found")) {
    return t("packages.reasonProviderMissing");
  }
  if (reason.startsWith("connection alias ") && reason.includes("matches more than one")) {
    return t("packages.reasonConnectionAmbiguous");
  }
  if (reason.startsWith("connection alias ") && reason.includes("was not found")) {
    return t("packages.reasonConnectionMissing");
  }
  if (reason === "connection binding target was not found") {
    return t("packages.reasonBindingMissing");
  }
  if (reason === "slug already exists on a different provider") {
    return t("packages.reasonSlugProviderConflict");
  }
  if (reason === "connection alias is required") {
    return t("packages.reasonConnectionRequired");
  }
  if (reason.includes("was not found in this workspace")) {
    return t("packages.reasonMissingInWorkspace", { detail: reason });
  }
  return reason;
}

function bindings(): ConnectionBinding[] {
  return Object.entries(remaps.value)
    .filter(([, target]) => target)
    .map(([key, targetConnectionId]) => {
      const [provider, connection] = key.split("::");
      return { provider, connection, targetConnectionId };
    });
}

function isYamlFile(file: File) {
  const name = file.name.toLowerCase();
  return name.endsWith(".yaml") || name.endsWith(".yml") || /ya?ml/i.test(file.type);
}

async function loadFile(file: File) {
  if (!isYamlFile(file)) {
    errorMessage.value = t("packages.invalidFile");
    return;
  }
  fileName.value = file.name;
  yamlText.value = await file.text();
  preview.value = null;
  errorMessage.value = "";
  await runPreview();
}

async function onFile(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = "";
  if (!file) return;
  await loadFile(file);
}

function onDragEnter(event: DragEvent) {
  event.preventDefault();
  dragDepth += 1;
  dragging.value = true;
}

function onDragOver(event: DragEvent) {
  event.preventDefault();
  if (event.dataTransfer) {
    event.dataTransfer.dropEffect = "copy";
  }
}

function onDragLeave(event: DragEvent) {
  event.preventDefault();
  dragDepth = Math.max(0, dragDepth - 1);
  if (dragDepth === 0) {
    dragging.value = false;
  }
}

function onDrop(event: DragEvent) {
  event.preventDefault();
  dragDepth = 0;
  dragging.value = false;
  const file = event.dataTransfer?.files?.[0];
  if (file) {
    void loadFile(file);
  }
}

async function runPreview() {
  if (!yamlText.value.trim()) {
    errorMessage.value = t("packages.noFile");
    return;
  }
  busy.value = "preview";
  errorMessage.value = "";
  try {
    preview.value = await previewCapabilityPackage(props.workspaceId, yamlText.value, bindings());
  } catch (error) {
    preview.value = null;
    errorMessage.value = packageErrorMessage(error, t("packages.previewFailed", { error: String(error) }));
  } finally {
    busy.value = "";
  }
}

async function applyImport() {
  if (!yamlText.value.trim() || !preview.value?.canImport) return;
  busy.value = "import";
  errorMessage.value = "";
  try {
    await importCapabilityPackage(props.workspaceId, yamlText.value, bindings());
    emit("imported");
    close();
  } catch (error) {
    errorMessage.value = packageErrorMessage(error, t("packages.importFailed", { error: String(error) }));
  } finally {
    busy.value = "";
  }
}
</script>

<template>
  <ManagementDialog
    :open="open"
    :title="t('packages.importTitle')"
    :eyebrow="t('packages.importConfig')"
    :description="t('packages.importSubtitle')"
    icon="fa-solid fa-file-arrow-up"
    :size="preview ? 'lg' : 'md'"
    :card-class="preview ? 'capability-package-dialog is-preview' : 'capability-package-dialog'"
    :close-aria-label="t('packages.closeAria')"
    :aria-label="t('packages.importTitle')"
    @close="close"
  >
    <input
      ref="fileInputRef"
      class="capability-package-file-input"
      type="file"
      accept=".yaml,.yml,.actweave.yaml,text/yaml,application/yaml"
      @change="onFile"
    />
    <button
      class="capability-package-dropzone"
      type="button"
      :class="{ 'is-dragging': dragging, 'has-file': Boolean(fileName) }"
      data-testid="capability-package-dropzone"
      @click="fileInputRef?.click()"
      @dragenter="onDragEnter"
      @dragover="onDragOver"
      @dragleave="onDragLeave"
      @drop="onDrop"
    >
      <i class="fa-solid fa-file-arrow-up" aria-hidden="true" />
      <strong>{{ fileName || t("packages.pickFile") }}</strong>
      <span>{{ fileName ? t("packages.reselect") : t("packages.dropHint") }}</span>
    </button>
    <p class="capability-package-note">{{ t("packages.sameSlugHint") }}</p>
    <p v-if="errorMessage" class="capability-package-error" role="alert">{{ errorMessage }}</p>
    <div v-if="preview" class="capability-package-preview">
      <p :class="preview.canImport ? 'is-ok' : 'is-blocked'">
        {{ preview.canImport ? t("packages.canImport") : t("packages.cannotImport") }}
      </p>
      <div class="capability-package-table-wrap">
        <table class="capability-package-table">
          <thead>
            <tr>
              <th>{{ t("packages.colKind") }}</th>
              <th>{{ t("packages.colSlug") }}</th>
              <th>{{ t("packages.colName") }}</th>
              <th>{{ t("packages.colAction") }}</th>
              <th>{{ t("packages.colDetail") }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in preview.items" :key="`${item.kind}:${item.slug}`">
              <td>{{ kindLabel(item.kind) }}</td>
              <td>
                <code>{{ item.slug }}</code>
              </td>
              <td>{{ item.name }}</td>
              <td>
                <span class="capability-package-action" :data-action="item.action">{{ actionLabel(item.action) }}</span>
              </td>
              <td>{{ itemDetail(item) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="remapKeys.length" class="capability-package-remap">
        <strong>{{ t("packages.remapConnection") }}</strong>
        <p class="capability-package-note">{{ t("packages.remapHint") }}</p>
        <p v-if="!connectionOptions.length" class="capability-package-error">{{ t("packages.remapNeedConnection") }}</p>
        <label v-for="key in remapKeys" :key="key" class="capability-package-remap-row">
          <span>{{ key.replace("::", " / ") }}</span>
          <AppSelect
            :model-value="remaps[key] || ''"
            :options="connectionOptions"
            :placeholder="t('packages.targetConnection')"
            :disabled="!connectionOptions.length"
            @update:model-value="
              remaps[key] = String($event || '');
              void runPreview();
            "
          />
        </label>
      </div>
    </div>
    <template #footer>
      <button class="ghost-button" type="button" @click="close">{{ t("common.cancel") }}</button>
      <button class="ghost-button" type="button" :disabled="busy === 'preview' || !yamlText" @click="runPreview">
        {{ busy === "preview" ? t("packages.previewing") : t("packages.preview") }}
      </button>
      <button class="primary-button" type="button" :disabled="!canImport" @click="applyImport">
        {{ busy === "import" ? t("packages.applying") : t("packages.apply") }}
      </button>
    </template>
  </ManagementDialog>
</template>

<style scoped>
:deep(.capability-package-dialog.is-preview) {
  width: min(800px, calc(100vw - 48px));
}

.capability-package-file-input {
  display: none;
}

.capability-package-dropzone {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  width: 100%;
  min-height: 148px;
  padding: 24px 20px;
  border: 1px dashed #cbd5e1;
  border-radius: 12px;
  background: #f8fafc;
  color: #334155;
  cursor: pointer;
  text-align: center;
  transition:
    background-color 0.16s ease,
    border-color 0.16s ease,
    color 0.16s ease;
}

.capability-package-dropzone:hover,
.capability-package-dropzone:focus-visible,
.capability-package-dropzone.is-dragging {
  border-color: #0d9488;
  background: #f0fdfa;
  color: #0f172a;
  outline: none;
}

.capability-package-dropzone.has-file {
  min-height: 96px;
  padding: 16px 20px;
  border-style: solid;
  border-color: #99f6e4;
  background: #f0fdfa;
}

.capability-package-dropzone i {
  display: grid;
  width: 40px;
  height: 40px;
  place-items: center;
  border-radius: 10px;
  background: #ecfdf5;
  color: #059669;
  font-size: 16px;
}

.capability-package-dropzone strong {
  font-size: 14px;
  font-weight: 700;
  line-height: 1.3;
}

.capability-package-dropzone span {
  max-width: 28em;
  color: #64748b;
  font-size: 13px;
  font-weight: 500;
  line-height: 1.5;
}

.capability-package-note {
  margin: 0;
  color: #64748b;
  font-size: 13px;
  line-height: 1.55;
}

.capability-package-error {
  margin: 0;
  color: var(--aw-danger, #b42318);
  font-size: 13px;
  line-height: 1.5;
}

.capability-package-preview {
  display: grid;
  gap: 12px;
}

.capability-package-preview p {
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
}

.capability-package-preview p.is-ok {
  color: var(--aw-success, #067647);
}

.capability-package-preview p.is-blocked {
  color: var(--aw-danger, #b42318);
}

.capability-package-table-wrap {
  overflow: auto;
  border: 1px solid var(--aw-border, #eaecf0);
  border-radius: 10px;
}

.capability-package-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

.capability-package-table th,
.capability-package-table td {
  text-align: left;
  padding: 10px 12px;
  border-bottom: 1px solid var(--aw-border, #eaecf0);
  vertical-align: top;
}

.capability-package-table th {
  color: #64748b;
  font-size: 12px;
  font-weight: 600;
  background: #f8fafc;
}

.capability-package-table tr:last-child td {
  border-bottom: 0;
}

.capability-package-action[data-action="blocked"],
.capability-package-action[data-action="rejected"] {
  color: var(--aw-danger, #b42318);
}

.capability-package-remap {
  display: grid;
  gap: 8px;
}

.capability-package-remap-row {
  display: grid;
  gap: 6px;
}
</style>
