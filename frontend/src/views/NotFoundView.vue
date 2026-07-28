<script setup lang="ts">
import { useRouter } from "vue-router";

const router = useRouter();

function goOverview() {
  void router.push({ name: "overview" });
}
</script>

<template>
  <div class="page-grid not-found-page">
    <section class="panel span-12 not-found-panel" role="status">
      <span class="not-found-code">404</span>
      <h2>页面不存在</h2>
      <p>该路径不在当前控制台路由中。请检查链接是否过期，或返回空间总览继续操作。</p>
      <button class="primary-button" type="button" @click="goOverview">返回空间总览</button>
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
