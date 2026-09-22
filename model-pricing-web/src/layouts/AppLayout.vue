<script setup lang="ts">
import { computed } from "vue";
import { useRoute, useRouter } from "vue-router";
import { usePermissionStore } from "../stores/permission";
import {
  DEV_IDENTITIES,
  useSessionStore,
  type DevIdentityKey,
  type PortalType,
} from "../stores/session";
import { useThemeStore, type ThemeMode } from "../stores/theme";
import { logout } from "../api/auth";
import { ElMessage } from "element-plus";
import { ref } from "vue";

const route = useRoute();
const router = useRouter();
const session = useSessionStore();
const permissions = usePermissionStore();
const theme = useThemeStore();
const loggingOut = ref(false);
async function signOut() {
  if (loggingOut.value) return;
  loggingOut.value = true;
  try {
    if (session.token && session.portal) await logout(session.portal);
    session.clear();
  } catch {
    ElMessage.error("退出未完成，请重试。");
  } finally { loggingOut.value = false; }
}

const portal = computed(() => route.meta.portal as PortalType);
const portalNames: Record<PortalType, string> = {
  internal: "内部运营门户",
  supplier: "供应商门户",
  customer: "客户门户",
};
const portalHomes: Record<PortalType, string> = {
  internal: "/internal/workbench",
  supplier: "/supplier/home",
  customer: "/customer/home",
};
const internalIdentityOptions: DevIdentityKey[] = [
  "MODEL_OPS",
  "PURCHASING",
  "PRICING_OP",
  "FINANCE_OP",
  "ADMIN_OP",
  "VIEWER",
];
const menuItems = computed(() =>
  router
    .getRoutes()
    .filter(
      (item) =>
        item.meta.menu &&
        item.meta.portal === portal.value &&
        (!item.meta.routeKey || permissions.canRoute(item.meta.routeKey)),
    )
    .sort((a, b) => (a.meta.order || 0) - (b.meta.order || 0)),
);

function switchIdentity(value: DevIdentityKey) {
  session.startDevSession(value);
  const currentKey = route.meta.routeKey;
  if (currentKey && !permissions.canRoute(currentKey)) {
    router.push(portalHomes[portal.value]);
  }
}

</script>

<template>
  <div class="app-shell">
    <aside class="app-sidebar">
      <router-link class="app-brand" :to="portalHomes[portal]">
        <span class="app-brand__mark">M</span>
        <span>模型与定价中心<small>MODEL OPERATIONS</small></span>
      </router-link>
      <div class="app-nav__label">{{ portalNames[portal] }}</div>
      <nav class="app-nav" aria-label="业务导航">
        <router-link
          v-for="item in menuItems"
          :key="item.meta.routeKey"
          class="app-nav__item"
          :to="item.path"
        >
          <span class="app-nav__bullet">◇</span>
          <span>{{ item.meta.title }}</span>
        </router-link>
      </nav>
    </aside>

    <div class="app-workspace">
      <header class="app-topbar">
        <div>
          <span class="app-topbar__portal">{{ portalNames[portal] }}</span>
          <span class="app-topbar__divider">/</span>
          <strong>{{ route.meta.title }}</strong>
        </div>
        <div class="app-topbar__actions">
          <el-select
            v-model="theme.mode"
            aria-label="主题模式"
            style="width: 112px"
          >
            <el-option label="跟随系统" value="system" />
            <el-option label="明亮" value="light" />
            <el-option label="深色" value="dark" />
          </el-select>
          <el-select
            v-if="session.isDevSession && portal === 'internal'"
            :model-value="session.user?.identityKey"
            aria-label="开发身份"
            style="width: 190px"
            @update:model-value="switchIdentity($event as DevIdentityKey)"
          >
            <el-option
              v-for="identityKey in internalIdentityOptions"
              :key="identityKey"
              :label="`${DEV_IDENTITIES[identityKey].user.name} · ${DEV_IDENTITIES[identityKey].user.roleLabel}`"
              :value="identityKey"
            />
          </el-select>
          <div class="app-user">
            <strong>{{ session.user?.name }}</strong>
            <small>{{ session.user?.roleLabel }}</small>
          </div>
          <el-button :loading="loggingOut" @click="signOut">退出登录</el-button>
        </div>
      </header>

      <div v-if="session.isDevSession" class="dev-session-banner">
        <b>开发身份 / Mock 权限</b>
        <span>当前未接入真实登录，权限仅用于前端框架验证。</span>
        <span v-if="route.meta.legacyMock">本页业务数据仍来自第一阶段 Legacy Mock。</span>
      </div>

      <main class="app-main">
        <router-view :key="session.user?.id" />
      </main>
    </div>
  </div>
</template>
