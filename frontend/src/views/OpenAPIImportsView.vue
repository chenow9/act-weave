<script setup lang="ts">
/** OpenAPI imports route shell (ZKL-64 item 17). */
import { provide } from "vue";
import OpenAPIImportsPageBody from "../components/OpenAPIImportsPageBody.vue";
import { useOpenAPIImportsPage } from "../composables/useOpenAPIImportsPage";
import "./openapi-imports-page.css";

const page = useOpenAPIImportsPage();
provide("openapiImportsPage", page);
</script>

<template>
  <OpenAPIImportsPageBody />
</template>
