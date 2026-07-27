<script setup lang="ts">
import { computed, reactive, ref } from "vue";
import { useRouter } from "vue-router";

import { useAuthStore } from "../stores/auth";

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
    return "新密码至少需要 12 位。";
  }
  if (form.newPassword !== form.confirmPassword) {
    return "两次输入的新密码不一致。";
  }
  if (form.currentPassword === form.newPassword) {
    return "新密码不能与当前密码相同。";
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
        <h1>需要先修改密码</h1>
        <p>管理员已为您设置临时密码。请设置新密码后重新登录，才能继续使用控制台。</p>
      </div>

      <div class="login-left-footer">AUTH: MUST_CHANGE_PASSWORD // SESSION: RESTRICTED // TRACE: AUDIT_ACTIVE</div>
    </section>

    <section class="login-right-panel">
      <div class="login-form-container">
        <div class="login-form-header">
          <h2>修改密码</h2>
          <p>新密码至少 12 位。修改成功后需要使用新密码重新登录。</p>
        </div>

        <div v-if="errorMessage" class="login-feedback-panel error" role="alert">
          <i class="fa-solid fa-triangle-exclamation" aria-hidden="true" />
          <span>{{ errorMessage }}</span>
        </div>

        <form class="login-form" @submit.prevent="submit">
          <label>
            <span>当前密码</span>
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
                :aria-label="showCurrent ? '隐藏密码' : '显示密码'"
                @click="showCurrent = !showCurrent"
              >
                <i :class="showCurrent ? 'fa-regular fa-eye-slash' : 'fa-regular fa-eye'" aria-hidden="true" />
              </button>
            </div>
          </label>

          <label>
            <span>新密码</span>
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
                :aria-label="showNew ? '隐藏密码' : '显示密码'"
                @click="showNew = !showNew"
              >
                <i :class="showNew ? 'fa-regular fa-eye-slash' : 'fa-regular fa-eye'" aria-hidden="true" />
              </button>
            </div>
          </label>

          <label>
            <span>确认新密码</span>
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
                :aria-label="showConfirm ? '隐藏密码' : '显示密码'"
                @click="showConfirm = !showConfirm"
              >
                <i :class="showConfirm ? 'fa-regular fa-eye-slash' : 'fa-regular fa-eye'" aria-hidden="true" />
              </button>
            </div>
          </label>

          <button class="login-primary-button" type="submit" :disabled="submitting || auth.loading">
            {{ submitting || auth.loading ? "提交中" : "修改密码并重新登录" }}
          </button>
        </form>

        <div class="login-security-tip">
          <span aria-hidden="true" />
          修改成功后会话将失效 · 请使用新密码重新登录
        </div>
      </div>
    </section>
  </main>
</template>
