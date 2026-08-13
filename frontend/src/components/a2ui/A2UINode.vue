<script setup lang="ts">
/**
 * One node of the component graph, and the only place that walks it.
 *
 * Children are resolved here rather than inside each container so that the
 * guards below — unknown component, dangling reference, cycle, depth — hold for
 * every component without each one restating them. Which members hold children
 * is a fact read from the catalog, so a new container component needs no change
 * here.
 */
import { computed } from "vue";
import { useI18n } from "vue-i18n";

import A2UIPlaceholder from "./A2UIPlaceholder.vue";
import { useA2UISurface } from "./context";
import { A2UI_CHILD_MEMBERS, A2UI_LIMITS, isA2UIComponentName } from "./generated/catalog.gen";
import { registry } from "./registry";

const props = defineProps<{
  id: string;
  depth: number;
  /** Ids on the path to here, so a cycle becomes a placeholder instead of a hang. */
  ancestors: readonly string[];
}>();

const surface = useA2UISurface();
const { t } = useI18n();

const node = computed(() => surface.value.byId.get(props.id));
const cyclic = computed(() => props.ancestors.includes(props.id));
const tooDeep = computed(() => props.depth > A2UI_LIMITS.maxTreeDepth);

const renderer = computed(() => {
  const name = node.value?.component;
  if (typeof name !== "string" || !isA2UIComponentName(name)) return undefined;
  return registry[name];
});

const childIds = computed(() => {
  const current = node.value;
  if (!current || !isA2UIComponentName(current.component)) return [];
  const members = A2UI_CHILD_MEMBERS[current.component] ?? [];
  const ids: string[] = [];
  for (const { member, list } of members) {
    const value = current[member];
    if (list && Array.isArray(value)) {
      ids.push(...value.filter((entry): entry is string => typeof entry === "string"));
    } else if (!list && typeof value === "string") {
      ids.push(value);
    }
  }
  return ids;
});

const isContainer = computed(() => {
  const current = node.value;
  return !!current && isA2UIComponentName(current.component) && !!A2UI_CHILD_MEMBERS[current.component];
});

const nextAncestors = computed(() => [...props.ancestors, props.id]);
</script>

<template>
  <A2UIPlaceholder v-if="!node" :reason="t('a2ui.missingChild', { id })" />
  <A2UIPlaceholder v-else-if="cyclic" :reason="t('a2ui.cycle', { id })" />
  <A2UIPlaceholder v-else-if="tooDeep" :reason="t('a2ui.tooDeep', { depth: A2UI_LIMITS.maxTreeDepth })" />
  <A2UIPlaceholder v-else-if="!renderer" :reason="t('a2ui.unsupportedComponent', { component: node.component })" />
  <component :is="renderer" v-else :node="node">
    <A2UIPlaceholder v-if="isContainer && !childIds.length" :reason="t('a2ui.missingChildren')" />
    <A2UINode
      v-for="(childId, index) in childIds"
      :key="`${index}-${childId}`"
      :id="childId"
      :depth="depth + 1"
      :ancestors="nextAncestors"
    />
  </component>
</template>
