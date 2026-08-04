<script setup lang="ts">
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";

import { useWorkspaceStore } from "../stores/workspaces";

withDefaults(
  defineProps<{
    feature: string;
    icon?: string;
    /** When true, empty sits inside ManagementList content chrome (table outline). */
    embeddedInList?: boolean;
  }>(),
  {
    icon: "fa-solid fa-layer-group",
    embeddedInList: false,
  },
);

const { t } = useI18n();
const emit = defineEmits<{ retry: [] }>();
const router = useRouter();
const workspaces = useWorkspaceStore();
const checking = ref(false);
const checkError = ref("");

async function recheckWorkspace() {
  if (checking.value) return;
  checking.value = true;
  checkError.value = "";
  try {
    await workspaces.load();
    if (workspaces.activeWorkspaceId || workspaces.items.length) {
      emit("retry");
      return;
    }
    checkError.value = t("common.wsContextStillEmpty");
  } catch {
    checkError.value = t("common.wsContextCheckFailed");
  } finally {
    checking.value = false;
  }
}
</script>

<template>
  <section
    class="workspace-context-state"
    :class="{ 'workspace-context-state--in-list': embeddedInList }"
    role="status"
    aria-live="polite"
  >
    <div class="workspace-context-state-icon" aria-hidden="true"><i :class="icon" /></div>
    <span class="workspace-context-state-eyebrow">{{ t("common.wsContextEyebrow") }}</span>
    <h2>{{ t("common.wsContextTitle") }}</h2>
    <p>{{ t("common.wsContextBody", { feature }) }}</p>
    <div class="workspace-context-state-actions">
      <button class="primary-button" type="button" @click="router.push('/workspaces')">
        <i class="fa-solid fa-arrow-right" aria-hidden="true" />
        {{ t("common.wsContextGo") }}
      </button>
      <button class="ghost-button" type="button" :disabled="checking" @click="recheckWorkspace">
        <i :class="checking ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-rotate'" aria-hidden="true" />
        {{ checking ? t("common.wsContextChecking") : t("common.wsContextRecheck") }}
      </button>
    </div>
    <div class="workspace-context-state-guidance">
      <span
        ><i class="fa-solid fa-circle-plus" aria-hidden="true" />{{ t("common.wsContextHintCreate") }}</span
      >
      <span
        ><i class="fa-solid fa-user-group" aria-hidden="true" />{{ t("common.wsContextHintJoin") }}</span
      >
    </div>
    <p v-if="checkError" class="workspace-context-state-error" role="alert">{{ checkError }}</p>
  </section>
</template>

<style scoped>
.workspace-context-state {
  min-height: 360px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 48px 28px;
  text-align: center;
  color: #64748b;
}

.workspace-context-state--in-list {
  min-height: 280px;
  width: 100%;
  padding: 40px 24px;
  box-sizing: border-box;
}

.workspace-context-state-icon {
  width: 68px;
  height: 68px;
  display: grid;
  place-items: center;
  border: 1px solid #bfe9df;
  border-radius: 20px;
  background: linear-gradient(145deg, #effcf8, #e6f7f2);
  color: #069b83;
  font-size: 28px;
  box-shadow: 0 14px 32px rgba(13, 148, 120, 0.12);
}

.workspace-context-state-eyebrow {
  margin-top: 22px;
  color: #0f9d88;
  font-size: 12px;
  font-weight: 800;
  letter-spacing: 0.16em;
}

.workspace-context-state h2 {
  margin: 8px 0 0;
  color: #14213d;
  font-size: 24px;
  line-height: 1.3;
}

.workspace-context-state > p:not(.workspace-context-state-error) {
  max-width: 620px;
  margin: 12px 0 0;
  font-size: 14px;
  line-height: 1.75;
}

.workspace-context-state-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 12px;
  margin-top: 24px;
}

.workspace-context-state-actions button {
  min-height: 44px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.workspace-context-state-guidance {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 10px;
  margin-top: 22px;
}

.workspace-context-state-guidance span {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 8px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 999px;
  background: #f8fafc;
  color: #64748b;
  font-size: 12px;
  font-weight: 650;
}

.workspace-context-state-error {
  margin: 16px 0 0;
  color: #b45309;
  font-size: 13px;
  font-weight: 650;
}

@media (max-width: 640px) {
  .workspace-context-state {
    min-height: 320px;
    padding: 38px 18px;
  }

  .workspace-context-state-actions,
  .workspace-context-state-actions button {
    width: 100%;
  }
}
</style>
