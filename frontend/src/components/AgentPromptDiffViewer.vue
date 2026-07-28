<script setup lang="ts">
import { Chunk, MergeView } from "@codemirror/merge";
import { EditorState, Text, type Extension } from "@codemirror/state";
import { EditorView, lineNumbers } from "@codemirror/view";
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";

const props = defineProps<{
  before: string;
  after: string;
  beforeLabel: string;
  afterLabel: string;
  title?: string;
}>();

const mergeHost = ref<HTMLElement | null>(null);
const activeChunkIndex = ref(0);
const editorError = ref("");
let mergeView: MergeView | null = null;

const beforeDoc = computed(() => Text.of(splitDiffLines(props.before)));
const afterDoc = computed(() => Text.of(splitDiffLines(props.after)));
const chunks = computed(() => Chunk.build(beforeDoc.value, afterDoc.value, { scanLimit: 1200, timeout: 750 }));
const changeCount = computed(() => chunks.value.length);
const activeChangeLabel = computed(() => {
  if (changeCount.value === 0) return "无变更";
  return `变更 ${activeChunkIndex.value + 1} / ${changeCount.value}`;
});
const minimapMarks = computed(() => {
  const totalLines = Math.max(1, afterDoc.value.lines);
  return chunks.value.map((chunk, index) => {
    const line = afterDoc.value.lineAt(Math.min(chunk.fromB, afterDoc.value.length)).number;
    const top = Math.max(4, Math.min(94, (line / totalLines) * 100));
    const tone = chunk.fromA === chunk.toA ? "added" : chunk.fromB === chunk.toB ? "removed" : "changed";
    return { key: `${chunk.fromA}-${chunk.fromB}-${index}`, top, tone };
  });
});

const promptDiffTheme = EditorView.theme(
  {
    "&": {
      height: "100%",
      color: "#0f172a",
      backgroundColor: "#ffffff",
    },
    ".cm-content": {
      caretColor: "transparent",
      padding: "4px 0",
    },
    ".cm-scroller": {
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
      fontSize: "12px",
      lineHeight: "1.55",
    },
    ".cm-gutters": {
      color: "#94a3b8",
      backgroundColor: "#f8fafc",
      borderRight: "1px solid #e2e8f0",
    },
    ".cm-line": {
      padding: "0 12px",
    },
    ".cm-activeLine, .cm-activeLineGutter": {
      backgroundColor: "transparent",
    },
    ".cm-selectionBackground, ::selection": {
      backgroundColor: "rgba(13, 148, 136, 0.18)",
    },
    "&.cm-merge-a .cm-changedLine": {
      backgroundColor: "#ffe4e6",
    },
    "&.cm-merge-b .cm-changedLine": {
      backgroundColor: "#dcfce7",
    },
    "&.cm-merge-a .cm-changedText": {
      backgroundColor: "rgba(225, 29, 72, 0.22)",
      borderRadius: "3px",
    },
    "&.cm-merge-b .cm-changedText": {
      backgroundColor: "rgba(22, 163, 74, 0.24)",
      borderRadius: "3px",
    },
    ".cm-collapsedLines": {
      margin: "5px 0",
      color: "#64748b",
      backgroundColor: "#f8fafc",
      borderColor: "#e2e8f0",
      borderRadius: "8px",
      fontSize: "11px",
    },
    ".cm-changeGutter": {
      width: "4px",
      paddingLeft: "0",
    },
    "&.cm-merge-a .cm-changedLineGutter": {
      backgroundColor: "#fb7185",
    },
    "&.cm-merge-b .cm-changedLineGutter": {
      backgroundColor: "#4ade80",
    },
  },
  { dark: false },
);

const readOnlyExtensions = computed<Extension[]>(() => [
  lineNumbers(),
  promptDiffTheme,
  EditorView.lineWrapping,
  EditorView.editable.of(false),
  EditorState.readOnly.of(true),
]);

onMounted(() => {
  buildMergeView();
});

watch(
  () => [props.before, props.after],
  async () => {
    activeChunkIndex.value = 0;
    await nextTick();
    buildMergeView();
  },
);

onBeforeUnmount(() => {
  destroyMergeView();
});

function splitDiffLines(value: string) {
  return value ? value.split(/\r?\n/) : [""];
}

function destroyMergeView() {
  mergeView?.destroy();
  mergeView = null;
}

function buildMergeView() {
  if (!mergeHost.value) return;
  destroyMergeView();
  mergeHost.value.replaceChildren();
  editorError.value = "";
  try {
    mergeView = new MergeView({
      a: {
        doc: props.before,
        extensions: readOnlyExtensions.value,
      },
      b: {
        doc: props.after,
        extensions: readOnlyExtensions.value,
      },
      parent: mergeHost.value,
      orientation: "a-b",
      highlightChanges: true,
      gutter: true,
      collapseUnchanged: {
        margin: 2,
        minSize: 5,
      },
      diffConfig: {
        scanLimit: 1200,
        timeout: 750,
      },
    });
    void nextTick(() => scrollToActiveChunk());
  } catch (error) {
    editorError.value = error instanceof Error ? error.message : "Diff editor 初始化失败。";
  }
}

function moveChunk(delta: number) {
  if (changeCount.value === 0) return;
  activeChunkIndex.value = (activeChunkIndex.value + delta + changeCount.value) % changeCount.value;
  scrollToActiveChunk();
}

function scrollToActiveChunk() {
  if (!mergeView || changeCount.value === 0) return;
  const chunk = chunks.value[activeChunkIndex.value];
  if (!chunk) return;
  const posA = Math.min(chunk.fromA, mergeView.a.state.doc.length);
  const posB = Math.min(chunk.fromB, mergeView.b.state.doc.length);
  mergeView.a.dispatch({
    selection: { anchor: posA },
    effects: EditorView.scrollIntoView(posA, { y: "center" }),
  });
  mergeView.b.dispatch({
    selection: { anchor: posB },
    effects: EditorView.scrollIntoView(posB, { y: "center" }),
  });
}
</script>

<template>
  <section class="agent-prompt-diff-viewer" aria-label="系统提示词变更对比">
    <div class="agent-prompt-diff-toolbar">
      <div>
        <strong>{{ title || "变更对比" }}</strong>
        <small>{{ activeChangeLabel }} · 折叠未变更 · 左右对照</small>
      </div>
      <div class="agent-prompt-diff-nav" aria-label="提示词变更导航">
        <button
          class="agent-prompt-diff-nav-button"
          type="button"
          :disabled="changeCount === 0"
          title="上一处变更"
          aria-label="上一处变更"
          @click="moveChunk(-1)"
        >
          <i class="fa-solid fa-arrow-up" aria-hidden="true" />
          <span>上一处变更</span>
        </button>
        <button
          class="agent-prompt-diff-nav-button"
          type="button"
          :disabled="changeCount === 0"
          title="下一处变更"
          aria-label="下一处变更"
          @click="moveChunk(1)"
        >
          <i class="fa-solid fa-arrow-down" aria-hidden="true" />
          <span>下一处变更</span>
        </button>
      </div>
    </div>

    <div class="agent-prompt-diff-editor-shell">
      <div class="agent-prompt-diff-pane-head">
        <span>{{ beforeLabel }}</span>
        <b>只读</b>
        <span>{{ afterLabel }}</span>
        <b>只读</b>
      </div>
      <div class="agent-prompt-diff-editor-row">
        <div ref="mergeHost" class="agent-prompt-diff-merge-host" />
        <div class="agent-prompt-diff-minimap" aria-hidden="true">
          <span
            v-for="mark in minimapMarks"
            :key="mark.key"
            :class="['agent-prompt-diff-minimap-mark', mark.tone]"
            :style="{ top: `${mark.top}%` }"
          />
        </div>
      </div>
      <p v-if="editorError" class="agent-prompt-diff-editor-error" role="alert">{{ editorError }}</p>
    </div>

    <div class="agent-prompt-diff-accessible">
      <strong>{{ beforeLabel }}</strong>
      <pre>{{ before }}</pre>
      <strong>{{ afterLabel }}</strong>
      <pre>{{ after }}</pre>
    </div>
  </section>
</template>

<style scoped>
.agent-prompt-diff-viewer {
  display: grid;
  gap: 10px;
  min-width: 0;
}

.agent-prompt-diff-toolbar {
  min-height: 46px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 8px 10px;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
}

.agent-prompt-diff-toolbar strong,
.agent-prompt-diff-toolbar small {
  display: block;
  min-width: 0;
}

.agent-prompt-diff-toolbar strong {
  color: #0f172a;
  font-size: 12px;
  font-weight: 800;
}

.agent-prompt-diff-toolbar small {
  margin-top: 2px;
  color: #64748b;
  font-size: 11px;
  font-weight: 700;
}

.agent-prompt-diff-nav {
  display: inline-flex;
  gap: 6px;
}

.agent-prompt-diff-nav-button {
  min-width: 34px;
  height: 34px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 0 10px;
  color: #334155;
  background: #fff;
  border: 1px solid #dbe3ef;
  border-radius: 8px;
  font-size: 11px;
  font-weight: 800;
  cursor: pointer;
}

.agent-prompt-diff-nav-button:hover:not(:disabled) {
  color: #047857;
  border-color: rgba(20, 184, 166, 0.5);
}

.agent-prompt-diff-nav-button:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.agent-prompt-diff-nav-button i {
  font-size: 11px;
}

.agent-prompt-diff-editor-shell {
  overflow: hidden;
  background: #fff;
  border: 1px solid #cbd5e1;
  border-radius: 12px;
}

.agent-prompt-diff-pane-head {
  min-height: 32px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  padding: 0 10px;
  color: #64748b;
  background: #fff;
  border-bottom: 1px solid #e2e8f0;
  font-size: 10px;
  font-weight: 900;
  text-transform: uppercase;
}

.agent-prompt-diff-pane-head span,
.agent-prompt-diff-pane-head b {
  min-width: 0;
}

.agent-prompt-diff-pane-head b {
  color: #94a3b8;
  font-size: 9px;
}

.agent-prompt-diff-editor-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 12px;
  min-height: 270px;
  max-height: 360px;
}

.agent-prompt-diff-merge-host {
  min-width: 0;
  min-height: 270px;
  max-height: 360px;
  overflow: hidden;
}

.agent-prompt-diff-minimap {
  position: relative;
  background: #f8fafc;
  border-left: 1px solid #e2e8f0;
}

.agent-prompt-diff-minimap-mark {
  position: absolute;
  left: 1px;
  right: 1px;
  height: 14px;
  border-radius: 2px;
}

.agent-prompt-diff-minimap-mark.added,
.agent-prompt-diff-minimap-mark.changed {
  background: #86efac;
}

.agent-prompt-diff-minimap-mark.removed {
  background: #fda4af;
}

.agent-prompt-diff-editor-error {
  margin: 0;
  padding: 10px 12px;
  color: #991b1b;
  background: #fef2f2;
  border-top: 1px solid #fecaca;
  font-size: 12px;
  font-weight: 700;
}

.agent-prompt-diff-accessible {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  clip-path: inset(50%);
  white-space: pre-wrap;
}

.agent-prompt-diff-accessible pre {
  margin: 0;
}

.agent-prompt-diff-viewer :deep(.cm-mergeView) {
  height: 100%;
  max-height: 360px;
  overflow: auto;
}

.agent-prompt-diff-viewer :deep(.cm-mergeViewEditors) {
  min-height: 270px;
}

.agent-prompt-diff-viewer :deep(.cm-mergeViewEditor) {
  min-width: 0;
}

.agent-prompt-diff-viewer :deep(.cm-editor) {
  min-height: 270px;
}

.agent-prompt-diff-viewer :deep(.cm-scroller) {
  overflow: auto;
}

.agent-prompt-diff-viewer :deep(.cm-merge-revert) {
  display: none;
}

.agent-prompt-diff-viewer :deep(.cm-insertedLine),
.agent-prompt-diff-viewer :deep(.cm-deletedLine),
.agent-prompt-diff-viewer :deep(ins),
.agent-prompt-diff-viewer :deep(del) {
  text-decoration: none;
}
</style>
