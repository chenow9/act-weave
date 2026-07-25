<script setup lang="ts">
import { computed, reactive, ref } from "vue";
import { useRouter } from "vue-router";

import { useAuthStore } from "../stores/auth";

const auth = useAuthStore();
const router = useRouter();
const showPassword = ref(false);
const loginSuccess = ref(false);
const loginErrorMessage = computed(() => auth.error);
const form = reactive({
  username: "",
  password: "",
});

async function submit() {
  loginSuccess.value = false;
  await auth.login(form.username, form.password);
  loginSuccess.value = true;
  await router.push({ name: "overview" });
}
</script>

<template>
  <main class="login-page login-split">
    <section class="login-left-panel">
      <div class="login-logo-area">
        <div class="login-governance-logo" aria-hidden="true">
          <span class="login-agent-node node-1" />
          <span class="login-agent-node node-2" />
          <span class="login-agent-node node-3" />
        </div>
        <span>ACTWEAVE 织行</span>
      </div>

      <div class="login-brand-content">
        <h1>把业务动作收敛到可控、可审计的执行链路</h1>
        <p>ActWeave 通过 Workspace、Agent、Workflow DAG 与 Tool Runtime，把真实业务操作放进显式编排、风险确认和全链路审计之中。</p>
      </div>

      <div class="login-left-footer">AUTH: JWT_LOCAL_SESSION // TRACE: AUDIT_ACTIVE // RUNTIME: TOOL_INVOCATION</div>
    </section>

    <section class="login-right-panel">
      <div class="login-form-container">
        <div class="login-form-header">
          <h2>登录 ActWeave</h2>
          <p>请输入已配置的账户凭证登录控制台。</p>
        </div>

        <div v-if="loginErrorMessage" class="login-feedback-panel error" role="alert">
          <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
          <span>{{ loginErrorMessage }}</span>
        </div>

        <div v-if="loginSuccess" class="login-feedback-panel success" role="status">
          <i class="fa-solid fa-circle-check" aria-hidden="true" />
          <span>
            <strong>安全验证通过</strong>
            <small>正在为您加载 Agent 编排控制台工作空间...</small>
          </span>
        </div>

        <form class="login-form" @submit.prevent="submit">
          <label>
            <span>Username</span>
            <div class="login-field-shell">
              <i class="login-field-icon fa-regular fa-user" aria-hidden="true" />
              <input v-model="form.username" autocomplete="username" required :disabled="auth.loading || loginSuccess" />
            </div>
          </label>

          <label>
            <span>Password</span>
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
            {{ auth.loading ? "登录中" : "登录 ActWeave" }}
          </button>
        </form>

        <div class="login-security-tip">
          <span aria-hidden="true" />
          JWT 本地会话 · Workspace 权限隔离 · Execution 审计追踪
        </div>

      </div>
    </section>
  </main>
</template>
