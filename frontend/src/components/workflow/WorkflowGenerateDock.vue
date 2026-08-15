<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";

import { WORKFLOW_GENERATE_PROMPT_MAX } from "../../composables/workflow-generate-dock";

const props = defineProps<{
  hasWorkspaceContext: boolean;
  sheet?: boolean;
}>();

const emit = defineEmits<{
  (event: "close-sheet"): void;
}>();

const { t } = useI18n();
const generatePrompt = ref("");

const examples = computed(() => [
  t("workflow.generateExample1"),
  t("workflow.generateExample2"),
  t("workflow.generateExample3"),
]);

const canSubmit = computed(() => {
  const prompt = generatePrompt.value.trim();
  return Boolean(props.hasWorkspaceContext && prompt && generatePrompt.value.length <= WORKFLOW_GENERATE_PROMPT_MAX);
});

function applyExample(example: string) {
  generatePrompt.value = example;
}

function submitGenerate() {
  if (!canSubmit.value) {
    return;
  }
}
</script>

<template>
  <section class="workflow-generate-dock">
    <div class="workflow-generate-dock-head">
      <div class="workflow-panel-heading">
        <span>{{ t("workflow.generateDockTitle") }}</span>
        <h3>{{ t("workflow.generateDockTitle") }}</h3>
        <p>{{ t("workflow.generateDockHint") }}</p>
      </div>
      <button
        v-if="props.sheet"
        class="ghost-button workflow-generate-sheet-done"
        type="button"
        data-action="close-generate-sheet"
        @click="emit('close-sheet')"
      >
        {{ t("workflow.generateSheetDone") }}
      </button>
    </div>

    <div class="workflow-generate-transcript" aria-live="polite" />

    <textarea
      v-model="generatePrompt"
      class="workflow-generate-prompt"
      rows="7"
      :maxlength="WORKFLOW_GENERATE_PROMPT_MAX"
      :aria-label="t('workflow.generateDockTitle')"
      :placeholder="t('workflow.generatePlaceholder')"
    />

    <div class="workflow-generate-examples">
      <span>{{ t("workflow.generateTryExamples") }}</span>
      <div class="workflow-generate-example-list">
        <button
          v-for="example in examples"
          :key="example"
          class="workflow-generate-example"
          type="button"
          @click="applyExample(example)"
        >
          {{ example }}
        </button>
      </div>
    </div>

    <div class="workflow-generate-char-count">
      {{ t("workflow.generateCharCount", { n: generatePrompt.length }) }}
    </div>

    <footer class="workflow-generate-footer">
      <span class="intent-chip">{{ t("workflow.generateAgentMissing") }}</span>
      <button
        class="primary-button"
        type="button"
        data-action="submit-generate"
        :disabled="!canSubmit"
        @click="submitGenerate"
      >
        {{ t("workflow.generateSubmit") }}
      </button>
    </footer>
  </section>
</template>
