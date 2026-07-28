<script setup lang="ts">
/** Connection form footer actions (ZKL-64 item 11). */
import { useServiceConnectionsPageContext } from "../composables/useServiceConnectionsPageContext";

const scp = useServiceConnectionsPageContext();
const {
  savingConnection,
  savingAndVerifyingConnection,
  formSubmitting,
  saveButtonText,
  saveAndVerifyButtonText,
  saveConnection,
  saveAndVerifyConnection,
} = scp;
</script>

<template>
  <footer class="connection-form-actions">
    <button
      data-testid="connection-save-draft"
      class="connection-secondary-button"
      type="button"
      :disabled="formSubmitting"
      :aria-busy="savingConnection ? 'true' : 'false'"
      @click.stop="saveConnection"
    >
      <i v-if="savingConnection" class="fa-solid fa-spinner fa-spin" />{{ saveButtonText }}
    </button>
    <button
      data-testid="connection-save-verify"
      class="connection-primary-button compact"
      type="button"
      :disabled="formSubmitting"
      :aria-busy="savingAndVerifyingConnection ? 'true' : 'false'"
      @click.stop="saveAndVerifyConnection"
    >
      <i :class="savingAndVerifyingConnection ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-circle-check'" />{{
        saveAndVerifyButtonText
      }}
    </button>
  </footer>
</template>
