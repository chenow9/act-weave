<script setup lang="ts">
/** Workflow route shell (ZKL-64 item 13). */
import { provide } from "vue";
import WorkflowPageBody from "../components/WorkflowPageBody.vue";
import { useWorkflowPage } from "../composables/useWorkflowPage";
import "./workflow-page.css";

const page = useWorkflowPage();
provide("workflowPage", page);
</script>

<template>
  <WorkflowPageBody />
</template>
