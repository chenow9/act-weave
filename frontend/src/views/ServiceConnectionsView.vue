<script setup lang="ts">
/** Service Connections route shell (ZKL-64 item 11). */
import { provide } from "vue";

import ServiceConnectionsPageBody from "../components/ServiceConnectionsPageBody.vue";
import { useServiceConnectionsPage } from "../composables/useServiceConnectionsPage";
import "./service-connections-page.css";

const page = useServiceConnectionsPage();
provide("scp", page);
</script>

<template>
  <ServiceConnectionsPageBody />
</template>
