<script setup lang="ts">
/** Smart DAG route shell (ZKL-64 item 14). */
import { provide } from "vue";
import SmartDagPageBody from "../components/SmartDagPageBody.vue";
import { useSmartDagPage } from "../composables/useSmartDagPage";
import "./smart-dag-page.css";

const page = useSmartDagPage();
provide("smartDagPage", page);
</script>

<template>
  <SmartDagPageBody />
</template>
