<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";

const { t } = useI18n();
const router = useRouter();

function goOverview() {
  void router.push({ name: "overview" });
}
</script>

<template>
  <div class="page-grid not-found-page">
    <section class="panel span-12 not-found-panel" role="status">
      <span class="not-found-code">404</span>
      <h2>{{ t("common.notFound") }}</h2>
      <p>{{ t("common.notFoundBody") }}</p>
      <button class="primary-button" type="button" @click="goOverview">{{ t("common.backOverview") }}</button>
    </section>
  </div>
</template>

<style scoped>
.not-found-page {
  min-height: 50vh;
  align-items: center;
}
.not-found-panel {
  display: grid;
  gap: 12px;
  justify-items: start;
  padding: 32px;
}
.not-found-code {
  font-size: 2rem;
  font-weight: 700;
  color: var(--aw-muted, #64748b);
}
</style>
