<script setup lang="ts">
import { computed } from "vue";
import { ElConfigProvider } from "element-plus";
import { useI18n } from "vue-i18n";

import type { AppLocale } from "./i18n/types";
import { elementPlusLocale } from "./services/locale";

const { locale } = useI18n();
const epLocale = computed(() => {
  const app: AppLocale = locale.value === "zh-CN" ? "zh-CN" : "en";
  return elementPlusLocale(app);
});
</script>

<template>
  <ElConfigProvider :locale="epLocale">
    <router-view />
  </ElConfigProvider>
</template>
