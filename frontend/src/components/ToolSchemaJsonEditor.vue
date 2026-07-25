<script setup lang="ts">
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { EditorView, lineNumbers } from "@codemirror/view";
import { tags } from "@lezer/highlight";
import { jsonc } from "@shopify/lang-jsonc";
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

const props = defineProps<{
  modelValue: string;
  title?: string;
  description?: string;
  parseError?: { message: string; line: number; column: number } | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: string];
  formatDocument: [];
}>();

const editorHost = ref<HTMLElement | null>(null);
let editorView: EditorView | null = null;

const jsonEditorTheme = EditorView.theme(
  {
    "&": {
      color: "#0f172a",
      backgroundColor: "#ffffff",
    },
    ".cm-content": {
      caretColor: "#0f172a",
    },
    ".cm-cursor, .cm-dropCursor": {
      borderLeftColor: "#0f172a",
    },
    ".cm-selectionBackground, ::selection": {
      backgroundColor: "rgba(13, 148, 136, 0.18)",
    },
    ".cm-gutters": {
      color: "#64748b",
      backgroundColor: "#f8fafc",
      borderRight: "1px solid #e2e8f0",
    },
    ".cm-activeLine": {
      backgroundColor: "rgba(13, 148, 136, 0.06)",
    },
    ".cm-activeLineGutter": {
      backgroundColor: "rgba(13, 148, 136, 0.08)",
    },
  },
  { dark: false },
);

const jsonHighlightStyle = HighlightStyle.define([
  { tag: tags.propertyName, color: "#0f766e" },
  { tag: tags.string, color: "#be123c" },
  { tag: tags.number, color: "#b45309" },
  { tag: tags.bool, color: "#2563eb" },
  { tag: tags.null, color: "#64748b" },
  { tag: tags.brace, color: "#334155" },
  { tag: tags.squareBracket, color: "#334155" },
  { tag: tags.separator, color: "#64748b" },
]);

const errorText = computed(() => {
  if (!props.parseError) return "";
  return `${props.parseError.message}（第 ${props.parseError.line} 行，第 ${props.parseError.column} 列）`;
});

function syncEditorText(nextValue: string) {
  if (!editorView) return;
  const currentValue = editorView.state.doc.toString();
  if (currentValue === nextValue) return;
  editorView.dispatch({
    changes: {
      from: 0,
      to: currentValue.length,
      insert: nextValue,
    },
  });
}

onMounted(() => {
  editorView = new EditorView({
    doc: props.modelValue,
    extensions: [
      jsonc(),
      lineNumbers(),
      jsonEditorTheme,
      syntaxHighlighting(jsonHighlightStyle),
      EditorView.lineWrapping,
      EditorView.updateListener.of((update) => {
        if (update.docChanged) {
          emit("update:modelValue", update.state.doc.toString());
        }
      }),
    ],
    parent: editorHost.value || undefined,
  });
});

watch(
  () => props.modelValue,
  (value) => {
    syncEditorText(value);
  },
);

onBeforeUnmount(() => {
  editorView?.destroy();
});
</script>

<template>
  <div class="tool-contract-editor-shell tool-contract-editor-shell-ide">
    <div class="tool-contract-editor-caption">
      <div class="tool-contract-editor-dots">
        <span />
        <span />
        <span />
      </div>
      <div class="tool-contract-editor-caption-main">
        <span class="tool-contract-editor-filename">payload_schema.jsonc</span>
        <button class="tool-contract-editor-caption-action" type="button" @click="emit('formatDocument')">格式化</button>
      </div>
    </div>
    <div ref="editorHost" class="tool-contract-code-editor" />
    <div v-if="parseError" class="tool-contract-parse-error">{{ errorText }}</div>
    <div class="tool-contract-editor-footer">
      <span>提示：支持单行双斜杠 <code>//</code> 或多行注释 <code>/* */</code></span>
      <span>状态：实时解析同步中</span>
    </div>
  </div>
</template>
