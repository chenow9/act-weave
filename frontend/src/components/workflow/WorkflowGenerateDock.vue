<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import { WORKFLOW_GENERATE_PROMPT_MAX } from "../../composables/workflow-generate-dock";

const props = defineProps<{
  hasWorkspaceContext: boolean;
  prompt: string;
  sheet?: boolean;
}>();

const emit = defineEmits<{
  (event: "close-sheet"): void;
  (event: "update:prompt", value: string): void;
}>();

const { t } = useI18n();

const examples = computed(() => [
  t("workflow.generateExample1"),
  t("workflow.generateExample2"),
  t("workflow.generateExample3"),
]);

const canSubmit = computed(() => {
  const nextPrompt = props.prompt.trim();
  return Boolean(props.hasWorkspaceContext && nextPrompt && props.prompt.length <= WORKFLOW_GENERATE_PROMPT_MAX);
});

function applyExample(example: string) {
  emit("update:prompt", example);
}

function onPromptInput(event: Event) {
  const target = event.target;
  if (target instanceof HTMLTextAreaElement) {
    emit("update:prompt", target.value);
  }
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
      class="workflow-generate-prompt"
      rows="7"
      :value="props.prompt"
      :maxlength="WORKFLOW_GENERATE_PROMPT_MAX"
      :aria-label="t('workflow.generateDockTitle')"
      :placeholder="t('workflow.generatePlaceholder')"
      @input="onPromptInput"
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
      {{ t("workflow.generateCharCount", { n: props.prompt.length }) }}
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
