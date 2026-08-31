<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";

import { useModalFocus } from "../composables/useModalFocus";
import {
  importCapabilityPackage,
  packageErrorMessage,
  previewCapabilityPackage,
  type ConnectionBinding,
  type PackagePreview,
} from "../services/capability-package";
import { useConnectionsStore } from "../stores/connections";
import AppSelect from "./AppSelect.vue";

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
const modalRef = ref<HTMLElement | null>(null);
const fileInputRef = ref<HTMLInputElement | null>(null);
const yamlText = ref("");
const fileName = ref("");
const preview = ref<PackagePreview | null>(null);
const busy = ref<"preview" | "import" | "">("");
const errorMessage = ref("");
const remaps = ref<Record<string, string>>({});

useModalFocus({
  visible: () => props.open,
  modalRef,
  onClose: close,
});

const canImport = computed(() => Boolean(preview.value?.canImport) && !busy.value);
const remapKeys = computed(() =>
  (preview.value?.items || [])
    .filter((item) => item.kind === "tool" && item.action === "blocked" && item.provider)
    .map((item) => `${item.provider}::${item.connection || ""}`),
);

const connectionOptions = computed(() =>
  (connectionsStore.serviceConnections || []).map((item) => ({
    label: `${item.name} (${item.alias || item.id})`,
    value: item.id,
  })),
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

function bindings(): ConnectionBinding[] {
  return Object.entries(remaps.value)
    .filter(([, target]) => target)
    .map(([key, targetConnectionId]) => {
      const [provider, connection] = key.split("::");
      return { provider, connection, targetConnectionId };
    });
}

async function onFile(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = "";
  if (!file) return;
  fileName.value = file.name;
  yamlText.value = await file.text();
  preview.value = null;
  errorMessage.value = "";
  await runPreview();
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
  <div v-if="open" class="modal-backdrop" @click.self="close">
    <section
      ref="modalRef"
      class="modal-card capability-package-dialog"
      role="dialog"
      aria-modal="true"
      :aria-label="t('packages.importTitle')"
    >
      <div class="modal-card-head">
        <div>
          <span>{{ t("packages.importConfig") }}</span>
          <h3>{{ t("packages.importTitle") }}</h3>
        </div>
        <button class="icon-action-button" type="button" :aria-label="t('packages.closeAria')" @click="close">
          <i class="fa-solid fa-xmark" />
        </button>
      </div>
      <p class="capability-package-subtitle">{{ t("packages.importSubtitle") }}</p>
      <p class="capability-package-hint">{{ t("packages.sameSlugHint") }}</p>
      <div class="capability-package-file">
        <input
          ref="fileInputRef"
          class="capability-package-file-input"
          type="file"
          accept=".yaml,.yml,.actweave.yaml,text/yaml,application/yaml"
          @change="onFile"
        />
        <button class="ghost-button" type="button" @click="fileInputRef?.click()">
          <i class="fa-solid fa-file-arrow-up" />
          <span>{{ fileName || t("packages.pickFile") }}</span>
        </button>
        <small>{{ t("packages.fileHint") }}</small>
      </div>
      <p v-if="errorMessage" class="capability-package-error" role="alert">{{ errorMessage }}</p>
      <div v-if="preview" class="capability-package-preview">
        <p :class="preview.canImport ? 'is-ok' : 'is-blocked'">
          {{ preview.canImport ? t("packages.canImport") : t("packages.cannotImport") }}
        </p>
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
              <td>{{ item.reason || item.provider }}</td>
            </tr>
          </tbody>
        </table>
        <div v-if="remapKeys.length && connectionOptions.length" class="capability-package-remap">
          <strong>{{ t("packages.remapConnection") }}</strong>
          <label v-for="key in remapKeys" :key="key" class="capability-package-remap-row">
            <span>{{ key.replace("::", " / ") }}</span>
            <AppSelect
              :model-value="remaps[key] || ''"
              :options="connectionOptions"
              :placeholder="t('packages.targetConnection')"
              @update:model-value="
                remaps[key] = String($event || '');
                void runPreview();
              "
            />
          </label>
        </div>
      </div>
      <div class="modal-card-actions">
        <button class="ghost-button" type="button" @click="close">{{ t("common.cancel") }}</button>
        <button class="ghost-button" type="button" :disabled="busy === 'preview' || !yamlText" @click="runPreview">
          {{ busy === "preview" ? t("packages.previewing") : t("packages.preview") }}
        </button>
        <button class="primary-button" type="button" :disabled="!canImport" @click="applyImport">
          {{ busy === "import" ? t("packages.applying") : t("packages.apply") }}
        </button>
      </div>
    </section>
  </div>
</template>

<style scoped>
.capability-package-dialog {
  width: min(920px, calc(100vw - 32px));
  max-height: calc(100vh - 48px);
  overflow: auto;
}
.capability-package-subtitle,
.capability-package-hint,
.capability-package-file small {
  color: var(--aw-text-muted, #667085);
  margin: 0 0 8px;
}
.capability-package-file {
  display: grid;
  gap: 6px;
  margin-bottom: 12px;
}
.capability-package-file-input {
  display: none;
}
.capability-package-error {
  color: var(--aw-danger, #b42318);
}
.capability-package-preview p.is-ok {
  color: var(--aw-success, #067647);
}
.capability-package-preview p.is-blocked {
  color: var(--aw-danger, #b42318);
}
.capability-package-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.capability-package-table th,
.capability-package-table td {
  text-align: left;
  padding: 6px 8px;
  border-bottom: 1px solid var(--aw-border, #eaecf0);
}
.capability-package-action[data-action="blocked"],
.capability-package-action[data-action="rejected"] {
  color: var(--aw-danger, #b42318);
}
.capability-package-remap {
  display: grid;
  gap: 8px;
  margin-top: 12px;
}
.capability-package-remap-row {
  display: grid;
  gap: 6px;
}
.modal-card-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
}
</style>
