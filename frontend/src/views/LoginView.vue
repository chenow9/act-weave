<script setup lang="ts">
import "./login-page.css";
import { computed, reactive, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";

import LoginWeaveMotif from "../components/LoginWeaveMotif.vue";
import type { AppLocale } from "../i18n/types";
import { setLocale } from "../services/locale";
import { useAuthStore } from "../stores/auth";

const { t, locale } = useI18n();
const auth = useAuthStore();
const router = useRouter();
const route = useRoute();
const showPassword = ref(false);
const loginSuccess = ref(false);
const loginErrorMessage = computed(() => auth.error);
const passwordChangedNotice = computed(() => route.query.passwordChanged === "1");
const form = reactive({
  username: "",
  password: "",
});
const currentLocale = computed<AppLocale>(() => (locale.value === "zh-CN" ? "zh-CN" : "en"));

async function submit() {
  loginSuccess.value = false;
  await auth.login(form.username, form.password);
  loginSuccess.value = true;
  if (auth.mustChangePassword) {
    await router.push({ name: "change-password" });
    return;
  }
  await router.push({ name: "overview" });
}

async function switchLanguage(next: AppLocale) {
  await setLocale(next, { syncServer: false });
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

      <div class="login-lang" data-testid="login-language-switcher" role="group" :aria-label="t('common.language')">
        <button
          type="button"
          data-testid="login-lang-zh-CN"
          :class="{ active: currentLocale === 'zh-CN' }"
          :aria-pressed="currentLocale === 'zh-CN'"
          @click="switchLanguage('zh-CN')"
        >
          {{ t("common.languageZh") }}
        </button>
        <button
          type="button"
          data-testid="login-lang-en"
          :class="{ active: currentLocale === 'en' }"
          :aria-pressed="currentLocale === 'en'"
          @click="switchLanguage('en')"
        >
          {{ t("common.languageEn") }}
        </button>
      </div>

      <div class="login-form-header">
        <h2>{{ t("auth.signIn") }}</h2>
        <p>{{ t("auth.signInSubtitle") }}</p>
      </div>

      <div v-if="loginErrorMessage" class="login-feedback-panel error" role="alert">
        <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
        <span>{{ loginErrorMessage }}</span>
      </div>

      <div
        v-if="passwordChangedNotice && !loginErrorMessage && !loginSuccess"
        class="login-feedback-panel success"
        role="status"
      >
        <i class="fa-solid fa-circle-check" aria-hidden="true" />
        <span>
          <strong>{{ t("auth.passwordUpdated") }}</strong>
          <small>{{ t("auth.passwordUpdatedHint") }}</small>
        </span>
      </div>

      <div v-if="loginSuccess" class="login-feedback-panel success" role="status">
        <i class="fa-solid fa-circle-check" aria-hidden="true" />
        <span>
          <strong>{{ t("auth.signInSuccess") }}</strong>
          <small>{{ t("auth.signInSuccessHint") }}</small>
        </span>
      </div>

      <form class="login-form" @submit.prevent="submit">
        <label>
          <span>{{ t("auth.username") }}</span>
          <div class="login-field-shell">
            <i class="login-field-icon fa-regular fa-user" aria-hidden="true" />
            <input v-model="form.username" autocomplete="username" required :disabled="auth.loading || loginSuccess" />
          </div>
        </label>

        <label>
          <span>{{ t("auth.password") }}</span>
          <div class="login-field-shell">
            <i class="login-field-icon fa-solid fa-lock" aria-hidden="true" />
            <input
              v-model="form.password"
              autocomplete="current-password"
              required
              :type="showPassword ? 'text' : 'password'"
              :disabled="auth.loading || loginSuccess"
            />
            <button
              class="login-password-toggle"
              type="button"
              :aria-label="showPassword ? t('auth.hidePassword') : t('auth.showPassword')"
              @click="showPassword = !showPassword"
            >
              <i :class="showPassword ? 'fa-regular fa-eye-slash' : 'fa-regular fa-eye'" aria-hidden="true" />
            </button>
          </div>
        </label>

        <button class="login-primary-button" type="submit" :disabled="auth.loading" data-testid="login-submit">
          {{ auth.loading ? t("auth.signInLoading") : t("auth.signIn") }}
        </button>
      </form>
    </div>
  </main>
</template>

<style scoped>
/* Compact segmented control on the login card — uses shell tokens */
.login-lang {
  display: inline-flex;
  align-self: flex-end;
  margin: 0.15rem 0 0.85rem auto;
  padding: 3px;
  border: 1px solid var(--aw-line, #e2e8f0);
  border-radius: 999px;
  background: color-mix(in srgb, var(--aw-bg, #f8fafc) 88%, #fff);
  gap: 2px;
}
.login-lang button {
  min-width: 4.25rem;
  min-height: 28px;
  padding: 0 0.75rem;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: var(--aw-muted, #64748b);
  font-size: 0.75rem;
  font-weight: 650;
  letter-spacing: 0.01em;
  cursor: pointer;
  transition:
    background 0.15s ease,
    color 0.15s ease,
    box-shadow 0.15s ease;
}
.login-lang button:hover {
  color: var(--aw-ink, #0f172a);
}
.login-lang button.active {
  background: #fff;
  color: var(--aw-green-ink, #0b7a55);
  box-shadow:
    0 1px 2px rgba(15, 23, 42, 0.08),
    0 0 0 1px color-mix(in srgb, var(--aw-green, #0f9f6e) 18%, transparent);
}
.login-lang button:focus-visible {
  outline: 2px solid color-mix(in srgb, var(--aw-green, #0f9f6e) 55%, transparent);
  outline-offset: 1px;
}
</style>
