<script setup lang="ts">
import "./login-page.css";
import { computed, reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";

import LoginWeaveMotif from "../components/LoginWeaveMotif.vue";
import { useAuthStore } from "../stores/auth";

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
</script>

<template>
  <main class="login-page">
    <LoginWeaveMotif />

    <div class="login-card">
      <div class="login-logo-area">
        <span class="app-brand-mark" aria-hidden="true"><i class="fa-solid fa-circle-nodes" /></span>
        <span>ACTWEAVE 织行</span>
      </div>

      <div class="login-form-header">
        <h2>登录</h2>
        <p>请输入账户凭证进入控制台</p>
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
          <strong>密码已更新</strong>
          <small>请使用新密码重新登录控制台。</small>
        </span>
      </div>

      <div v-if="loginSuccess" class="login-feedback-panel success" role="status">
        <i class="fa-solid fa-circle-check" aria-hidden="true" />
        <span>
          <strong>登录成功</strong>
          <small>正在进入控制台...</small>
        </span>
      </div>

      <form class="login-form" @submit.prevent="submit">
        <label>
          <span>用户名</span>
          <div class="login-field-shell">
            <i class="login-field-icon fa-regular fa-user" aria-hidden="true" />
            <input
              v-model="form.username"
              autocomplete="username"
              required
              :disabled="auth.loading || loginSuccess"
            />
          </div>
        </label>

        <label>
          <span>密码</span>
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
              :aria-label="showPassword ? '隐藏密码' : '显示密码'"
              @click="showPassword = !showPassword"
            >
              <i :class="showPassword ? 'fa-regular fa-eye-slash' : 'fa-regular fa-eye'" aria-hidden="true" />
            </button>
          </div>
        </label>

        <button class="login-primary-button" type="submit" :disabled="auth.loading">
          {{ auth.loading ? "登录中..." : "登录" }}
        </button>
      </form>
    </div>
  </main>
</template>
