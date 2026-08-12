import { computed, inject, type ComputedRef, type InjectionKey } from "vue";

import type { A2UIComponentNode } from "./generated/catalog.gen";
import { resolveBoolean, resolveChoiceValues, resolveSeries, resolveString } from "./resolve";

/**
 * What every component of a surface needs and nothing more: the flat component
 * graph and the data model its bindings point into.
 */
export interface A2UISurfaceContext {
  byId: Map<string, A2UIComponentNode>;
  dataModel: unknown;
  /** Distinguishes control ids when several surfaces sit on one page. */
  uid: string;
}

export const A2UI_SURFACE: InjectionKey<ComputedRef<A2UISurfaceContext>> = Symbol("a2ui-surface");

const EMPTY: A2UISurfaceContext = { byId: new Map(), dataModel: undefined, uid: "orphan" };

export function useA2UISurface(): ComputedRef<A2UISurfaceContext> {
  return inject(
    A2UI_SURFACE,
    computed(() => EMPTY),
  );
}

/**
 * Binding-aware reads for a component renderer. A member may be a literal or a
 * pointer into the data model, and a renderer must not have to know which.
 */
export function useA2UIValues() {
  const surface = useA2UISurface();
  return {
    string: (value: unknown) => resolveString(value, surface.value.dataModel),
    boolean: (value: unknown) => resolveBoolean(value, surface.value.dataModel),
    choices: (value: unknown) => resolveChoiceValues(value, surface.value.dataModel),
    series: (value: unknown) => resolveSeries(value, surface.value.dataModel),
    /** A DOM id for a control, unique across surfaces on the page. */
    controlId: (node: A2UIComponentNode) => `a2ui-${surface.value.uid}-${node.id}`,
  };
}
