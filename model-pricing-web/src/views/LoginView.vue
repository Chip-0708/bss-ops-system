<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { login } from "../api/auth";
import { ApiError } from "../domain/common";
import { useSessionStore, type PortalType } from "../stores/session";

const route = useRoute();
const router = useRouter();
const session = useSessionStore();
const portal = computed(() => route.meta.portal as PortalType);
const names = { internal: "内部用户", supplier: "供应商", customer: "客户" };
const roles: Array<{ portal: PortalType; label: string; hint: string; mark: string }> = [
  { portal: "internal", label: "内部用户", hint: "员工账号", mark: "内" },
  { portal: "supplier", label: "供应商", hint: "供应商账号", mark: "供" },
  { portal: "customer", label: "客户", hint: "客户账号", mark: "客" },
];
const homes = { internal: "/internal/workbench", supplier: "/supplier/home", customer: "/customer/home" };
const account = ref("");
const password = ref("");
const busy = ref(false);
const error = ref("");
watch(portal, () => { password.value = ""; error.value = ""; });

async function submit() {
  if (busy.value) return;
  error.value = "";
  if (!account.value.trim() || !password.value) { error.value = "请输入账号和密码。"; return; }
  busy.value = true;
  const targetPortal = portal.value;
  const loginID = account.value.trim();
  try {
    const result = await login(targetPortal, loginID, password.value);
    if (portal.value !== targetPortal) return;
    session.startSession(result, targetPortal, loginID);
    const redirect = route.query.redirect;
    const destination = typeof redirect === "string" && redirect.startsWith(`/${targetPortal}/`)
      ? router.resolve(redirect) : null;
    await router.replace(destination?.meta.requiresAuth && destination.meta.portal === targetPortal
      ? destination.fullPath : homes[targetPortal]);
  } catch (cause) {
    error.value = cause instanceof ApiError
      ? cause.httpStatus === 401 ? "账号或密码不正确。" : cause.message
      : "登录响应不符合约定，请联系管理员。";
  } finally {
    password.value = "";
    busy.value = false;
  }
}
</script>

<template>
  <main class="login-page">
    <section class="login-card">
      <div class="login-brand"><span class="login-brand__mark">M</span><span>模型与定价中心</span></div>
      <h1>{{ names[portal] }}登录</h1>
      <p class="login-intro">选择账号所属身份，然后登录对应门户。</p>
      <nav class="login-roles" aria-label="选择登录身份">
        <router-link
          v-for="role in roles"
          :key="role.portal"
          class="login-role"
          :class="{ 'is-active': portal === role.portal }"
          :aria-current="portal === role.portal ? 'page' : undefined"
          :to="`/${role.portal}/login`"
        >
          <span class="login-role__mark">{{ role.mark }}</span>
          <span class="login-role__text"><strong>{{ role.label }}</strong><small>{{ role.hint }}</small></span>
        </router-link>
      </nav>
      <form @submit.prevent="submit">
        <label for="login-account">账号</label>
        <el-input id="login-account" v-model="account" autocomplete="username" placeholder="请输入账号" :disabled="busy" />
        <label for="login-password">密码</label>
        <el-input id="login-password" v-model="password" type="password" autocomplete="current-password" placeholder="请输入密码" :disabled="busy" />
        <p v-if="error" role="alert" class="login-error">{{ error }}</p>
        <el-button type="primary" native-type="submit" :loading="busy">登录{{ names[portal] }}门户</el-button>
      </form>
    </section>
  </main>
</template>

<style scoped>
.login-page { min-height: 100vh; display: grid; place-items: center; padding: 28px; background: radial-gradient(circle at 50% 8%, var(--app-hover-bg), transparent 55%); }
.login-card { width: min(100%, 540px); padding: 38px; border: 1px solid var(--app-border); border-radius: 20px; background: var(--app-surface); box-shadow: 0 24px 70px rgba(22, 35, 70, .09); }
.login-brand { display: flex; align-items: center; gap: 10px; color: var(--app-text-secondary); font-size: 13px; font-weight: 700; }
.login-brand__mark { display: grid; width: 32px; height: 32px; place-items: center; border-radius: 9px; background: var(--app-accent); color: var(--app-accent-contrast); font-size: 19px; }
h1 { margin: 26px 0 8px; font-size: 30px; }
.login-intro { margin: 0 0 25px; color: var(--app-text-muted); font-size: 13px; }
.login-roles { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; margin-bottom: 26px; }
.login-role { display: flex; align-items: center; gap: 8px; min-height: 78px; padding: 12px 10px; border: 1px solid var(--app-border); border-radius: 13px; background: var(--app-surface-muted); color: var(--app-text-primary); text-decoration: none; transition: border-color .18s, background-color .18s, transform .18s; }
.login-role:hover { border-color: var(--app-accent); transform: translateY(-2px); }
.login-role:focus-visible { outline: 2px solid var(--app-accent); outline-offset: 2px; }
.login-role.is-active { border-color: var(--app-accent); background: var(--app-hover-bg); box-shadow: inset 0 0 0 1px var(--app-accent); }
.login-role__mark { display: grid; flex: 0 0 30px; height: 30px; place-items: center; border-radius: 9px; background: var(--app-surface); color: var(--app-accent); font-size: 13px; font-weight: 700; }
.login-role__text { display: grid; gap: 3px; min-width: 0; }
.login-role__text strong { white-space: nowrap; font-size: 13px; }
.login-role__text small { color: var(--app-text-muted); font-size: 11px; }
form { display: grid; gap: 10px; }
label { margin-top: 8px; font-size: 13px; font-weight: 650; }
form > .el-button { width: 100%; margin-top: 18px; }
.login-error { color: var(--el-color-danger); margin: 0; }
@media (max-width: 520px) { .login-page { padding: 14px; }.login-card { padding: 24px; }.login-roles { gap: 6px; }.login-role { justify-content: center; padding: 10px 6px; }.login-role__mark { display: none; }.login-role__text { text-align: center; }.login-role__text strong { font-size: 12px; }.login-role__text small { font-size: 10px; } }
</style>
