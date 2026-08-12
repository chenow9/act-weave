<script setup lang="ts">
/**
 * Graceful degradation, made visible. Every reason a renderer cannot draw
 * something ends up here: a component from a newer catalog, a dangling child
 * reference, a cycle, or a tree deeper than the contract allows.
 */
defineProps<{ reason: string }>();
</script>

<template>
  <p class="a2ui-placeholder" data-a2ui-placeholder role="note">
    <i class="fa-solid fa-circle-info" aria-hidden="true" />
    <span>{{ reason }}</span>
  </p>
</template>
