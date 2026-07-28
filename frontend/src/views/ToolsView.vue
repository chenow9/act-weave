<script setup lang="ts">
/** Tools route shell (ZKL-64 item 12). */
import { provide } from "vue";
import ToolsPageBody from "../components/ToolsPageBody.vue";
import { useToolsPage } from "../composables/useToolsPage";
import "./tools-page.css";

const page = useToolsPage();
provide("toolsPage", page);
</script>

<template>
  <ToolsPageBody />
</template>
