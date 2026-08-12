<script setup lang="ts">
/**
 * Entry point for one A2UI surface.
 *
 * The surface arrives on its own message field, already validated against the
 * catalog by the server. This component still checks what a display client must
 * check on its own — the catalog it was built for, a root to start from — because
 * a surface may also come from a producer whose catalog is newer than this build.
 */
import { computed, provide } from "vue";
import { useI18n } from "vue-i18n";

import A2UINode from "./A2UINode.vue";
import A2UIPlaceholder from "./A2UIPlaceholder.vue";
import { A2UI_SURFACE, type A2UISurfaceContext } from "./context";
import {
  A2UI_CATALOG_ID,
  A2UI_LIMITS,
  A2UI_ROOT_ID,
  type A2UIComponentNode,
  type A2UISurface as SurfaceObject,
} from "./generated/catalog.gen";

const props = defineProps<{
  surface: unknown;
  /** Distinguishes control ids when several surfaces sit on one page. */
  uid: string;
}>();

const { t } = useI18n();

const surfaceObject = computed<SurfaceObject | undefined>(() => {
  const value = props.surface;
  if (typeof value !== "object" || value === null || Array.isArray(value)) return undefined;
  return value as SurfaceObject;
});

// An unknown catalogId means the surface was written against a contract this
// build does not have, so refusing it as a whole is more honest than drawing the
// components whose names happen to overlap.
const foreignCatalog = computed(() => {
  const declared = surfaceObject.value?.catalogId;
  return typeof declared === "string" && declared !== "" && declared !== A2UI_CATALOG_ID ? declared : undefined;
});

const components = computed<A2UIComponentNode[]>(() => {
  const raw = surfaceObject.value?.components;
  if (!Array.isArray(raw)) return [];
  return raw
    .filter((component): component is A2UIComponentNode => {
      if (typeof component !== "object" || component === null) return false;
      const { id, component: name } = component as Record<string, unknown>;
      return typeof id === "string" && id !== "" && typeof name === "string" && name !== "";
    })
    .slice(0, A2UI_LIMITS.maxComponents);
});

const context = computed<A2UISurfaceContext>(() => {
  const byId = new Map<string, A2UIComponentNode>();
  // First declaration wins, matching the server, which rejects duplicate ids.
  for (const component of components.value) {
    if (!byId.has(component.id)) byId.set(component.id, component);
  }
  return { byId, dataModel: surfaceObject.value?.dataModel, uid: props.uid };
});

provide(A2UI_SURFACE, context);

const hasRoot = computed(() => context.value.byId.has(A2UI_ROOT_ID));
</script>

<template>
  <section class="a2ui-surface" data-a2ui-surface :aria-label="t('a2ui.label')">
    <header class="a2ui-surface-head">
      <span><i class="fa-solid fa-table-cells" aria-hidden="true" /> {{ t("a2ui.label") }}</span>
      <span class="a2ui-surface-meta">{{ t("a2ui.displayOnly") }}</span>
    </header>

    <A2UIPlaceholder v-if="foreignCatalog" :reason="t('a2ui.foreignCatalog', { catalogId: foreignCatalog })" />
    <A2UIPlaceholder v-else-if="!components.length" :reason="t('a2ui.emptySurface')" />
    <A2UIPlaceholder v-else-if="!hasRoot" :reason="t('a2ui.missingRoot', { id: A2UI_ROOT_ID })" />
    <div v-else class="a2ui-stack">
      <A2UINode :id="A2UI_ROOT_ID" :depth="1" :ancestors="[]" />
    </div>
  </section>
</template>
