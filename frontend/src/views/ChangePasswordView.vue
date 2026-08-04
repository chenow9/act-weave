<script setup lang="ts">
import "./change-password-page.css";
import "./login-page.css";
import { computed, reactive, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";

import LoginWeaveMotif from "../components/LoginWeaveMotif.vue";
import { useAuthStore } from "../stores/auth";

const { t } = useI18n();
const auth = useAuthStore();
const router = useRouter();
const showCurrent = ref(false);
const showNew = ref(false);
const showConfirm = ref(false);
const submitting = ref(false);
const localError = ref("");
const form = reactive({
  currentPassword: "",
  newPassword: "",
  confirmPassword: "",
});

const errorMessage = computed(() => localError.value || auth.error);

function validate(): string {
  if (form.newPassword.length < 12) {
    return t("auth.passwordMinLength");
  }
  if (form.newPassword !== form.confirmPassword) {
    return t("auth.passwordMismatch");
  }
  if (form.currentPassword === form.newPassword) {
    return t("auth.passwordSameAsCurrent");
  }
  return "";
}

async function submit() {
  if (submitting.value || auth.loading) {
    return;
  }
  localError.value = "";
  const validationError = validate();
  if (validationError) {
    localError.value = validationError;
    return;
  }
  submitting.value = true;
  try {
    await auth.changePassword(form.currentPassword, form.newPassword);
    form.currentPassword = "";
    form.newPassword = "";
    form.confirmPassword = "";
    await router.push({ name: "login", query: { passwordChanged: "1" } });
  } catch {
    // auth.error already set by the store; do not retain passwords on failure.
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <main class="login-page">
    <LoginWeaveMotif />

    <div class="login-card">
      <div class="login-logo-area">
        <span class="app-brand-mark" aria-hidden="true"><i class="fa-solid fa-circle-nodes" /></span>
        <span>{{ t("common.appTitle") }}</span>
      </div>

      <div class="login-form-header">
        <h2>{{ t("auth.changePassword") }}</h2>
        <p>{{ t("auth.changePasswordSubtitle") }}</p>
      </div>

      <div v-if="errorMessage" class="login-feedback-panel error" role="alert">
        <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
        <span>{{ errorMessage }}</span>
      </div>

      <form class="login-form" @submit.prevent="submit">
        <label>
          <span>{{ t("auth.currentPassword") }}</span>
          <div class="login-field-shell">
            <i class="login-field-icon fa-solid fa-lock" aria-hidden="true" />
            <input
              v-model="form.currentPassword"
              autocomplete="current-password"
              required
              :type="showCurrent ? 'text' : 'password'"
              :disabled="submitting || auth.loading"
            />
            <button
              class="login-password-toggle"
              type="button"
              :aria-label="showCurrent ? t('auth.hidePassword') : t('auth.showPassword')"
              @click="showCurrent = !showCurrent"
            >
              <i :class="showCurrent ? 'fa-regular fa-eye-slash' : 'fa-regular fa-eye'" aria-hidden="true" />
            </button>
          </div>
        </label>

        <label>
          <span>{{ t("auth.newPassword") }}</span>
          <div class="login-field-shell">
            <i class="login-field-icon fa-solid fa-key" aria-hidden="true" />
            <input
              v-model="form.newPassword"
              autocomplete="new-password"
              required
              minlength="12"
              :type="showNew ? 'text' : 'password'"
              :disabled="submitting || auth.loading"
            />
            <button
              class="login-password-toggle"
              type="button"
              :aria-label="showNew ? t('auth.hidePassword') : t('auth.showPassword')"
              @click="showNew = !showNew"
            >
              <i :class="showNew ? 'fa-regular fa-eye-slash' : 'fa-regular fa-eye'" aria-hidden="true" />
            </button>
          </div>
        </label>

        <label>
          <span>{{ t("auth.confirmNewPassword") }}</span>
          <div class="login-field-shell">
            <i class="login-field-icon fa-solid fa-key" aria-hidden="true" />
            <input
              v-model="form.confirmPassword"
              autocomplete="new-password"
              required
              minlength="12"
              :type="showConfirm ? 'text' : 'password'"
              :disabled="submitting || auth.loading"
            />
            <button
              class="login-password-toggle"
              type="button"
              :aria-label="showConfirm ? t('auth.hidePassword') : t('auth.showPassword')"
              @click="showConfirm = !showConfirm"
            >
              <i :class="showConfirm ? 'fa-regular fa-eye-slash' : 'fa-regular fa-eye'" aria-hidden="true" />
            </button>
          </div>
        </label>

        <button class="login-primary-button" type="submit" :disabled="submitting || auth.loading">
          {{ submitting || auth.loading ? t("auth.changingPassword") : t("auth.submitChangePassword") }}
        </button>
      </form>
    </div>
  </main>
</template>
