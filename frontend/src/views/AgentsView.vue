<script setup lang="ts">
/** Agents route shell (ZKL-64 item 16). */
import { provide } from "vue";
import AgentsPageBody from "../components/AgentsPageBody.vue";
import { useAgentsPage } from "../composables/useAgentsPage";
import "./agents-page.css";

const page = useAgentsPage();
provide("agentsPage", page);
</script>

<template>
  <AgentsPageBody />
</template>
