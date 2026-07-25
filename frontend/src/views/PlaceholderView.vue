<script setup lang="ts">
import { computed } from "vue";
import { useRoute } from "vue-router";

import { navItems } from "../config/navigation";

const route = useRoute();
const moduleId = computed(() => String(route.params.moduleId || ""));
const nav = computed(() => navItems.find((item) => item.id === moduleId.value));
</script>

<template>
  <div class="page-grid">
    <header class="page-header span-12">
      <div>
        <span>{{ nav?.badge || "MVP" }}</span>
        <h2>{{ nav?.label || "模块" }}</h2>
        <p>该模块已纳入 MVP 计划，当前 M1 先完成框架、认证、基础导航和空间总览。</p>
      </div>
    </header>

    <section class="panel span-12 placeholder-panel">
      <strong>{{ nav?.label }}</strong>
      <span>后续按实施计划进入 {{ nav?.badge || "对应里程碑" }}。</span>
    </section>
  </div>
</template>
