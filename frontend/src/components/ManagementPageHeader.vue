<script setup lang="ts">
withDefaults(
  defineProps<{
    title: string;
    description: string;
    icon: string;
    eyebrow?: string;
  }>(),
  {
    eyebrow: "",
  },
);
</script>

<template>
  <header class="management-page-header">
    <div class="management-page-title">
      <span class="management-page-icon" aria-hidden="true"><i :class="icon" /></span>
      <div>
        <span v-if="eyebrow" class="management-page-eyebrow">{{ eyebrow }}</span>
        <h1>{{ title }}</h1>
        <p>{{ description }}</p>
      </div>
    </div>
    <div v-if="$slots.actions" class="management-page-actions page-actions page-header-actions">
      <slot name="actions" />
    </div>
  </header>
</template>

<style scoped>
.management-page-header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 20px;
  min-width: 0;
  margin: 0;
}

.management-page-title {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 12px;
}

.management-page-title > div {
  min-width: 0;
}

.management-page-icon {
  display: grid;
  width: 39px;
  height: 39px;
  flex: 0 0 39px;
  place-items: center;
  border-radius: 12px;
  background: var(--aw-green-soft, #eaf8f2);
  color: var(--aw-green-ink, #087653);
  font-size: 15px;
}

.management-page-eyebrow {
  display: block;
  margin-bottom: 3px;
  color: var(--aw-subtle, #96a19d);
  font-size: 9px;
  font-weight: 800;
  letter-spacing: 0.04em;
}

.management-page-header h1 {
  margin: 0;
  color: var(--aw-ink, #18211e);
  font-size: 23px;
  font-weight: 760;
  line-height: 1.18;
  letter-spacing: -0.028em;
}

.management-page-header p {
  margin: 5px 0 0;
  color: var(--aw-muted, #74817c);
  font-size: 12px;
  line-height: 1.45;
}

.management-page-actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

@media (max-width: 700px) {
  .management-page-header {
    align-items: flex-start;
    flex-direction: column;
  }

  .management-page-actions {
    width: 100%;
  }

  .management-page-actions :deep(.primary-button) {
    flex: 1;
  }
}
</style>
