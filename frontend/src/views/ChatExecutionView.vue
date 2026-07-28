<script setup lang="ts">
/** Chat execution route shell (ZKL-64 item 15). */
import { provide } from "vue";
import ChatExecutionPageBody from "../components/ChatExecutionPageBody.vue";
import { useChatExecutionPage } from "../composables/useChatExecutionPage";
import "./chat-execution-page.css";

const page = useChatExecutionPage();
provide("chatExecutionPage", page);
</script>

<template>
  <ChatExecutionPageBody />
</template>
