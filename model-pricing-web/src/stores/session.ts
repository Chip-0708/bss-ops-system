import { defineStore } from "pinia";
import { computed, ref } from "vue";
import { PERMISSIONS } from "../domain/permissions";
import { role as legacyRole } from "../session";
import type { Role as LegacyRole } from "../types";
import { usePermissionStore } from "./permission";
import type { LoginResult } from "../api/auth";
import { assertContractId } from "../domain/contractId";

export type PortalType = "internal" | "supplier" | "customer";
export type DevIdentityKey =
  | "MODEL_OPS"
  | "PURCHASING"
  | "RETRO_OP"
  | "PRICING_OP"
  | "FINANCE_OP"
  | "ADMIN_OP"
  | "VIEWER"
  | "SUPPLIER"
  | "CUSTOMER";

export interface CurrentUser {
  id: string;
  name: string;
  roleLabel: string;
  roleCodes?: string[];
  identityKey?: DevIdentityKey;
}

interface DevIdentity {
  user: CurrentUser & { identityKey: DevIdentityKey };
  portal: PortalType;
  permissionCodes: string[];
  legacyRole: LegacyRole;
}

const allViewPermissions = Object.entries(PERMISSIONS)
  .filter(([name]) => name.endsWith("_VIEW"))
  .map(([, code]) => code);

export const DEV_IDENTITIES: Record<DevIdentityKey, DevIdentity> = {
  MODEL_OPS: {
    portal: "internal",
    legacyRole: "MODEL_OPS",
    user: {
      id: "dev-model-ops",
      name: "林悦",
      roleLabel: "模型运营",
      identityKey: "MODEL_OPS",
    },
    permissionCodes: [
      PERMISSIONS.MODEL_VIEW,
      PERMISSIONS.MODEL_EDIT,
      PERMISSIONS.MODEL_APPROVE,
      PERMISSIONS.PRICE_SYNC_VIEW,
      PERMISSIONS.PRICE_SYNC_EDIT,
      PERMISSIONS.PRICE_SYNC_APPROVE,
      PERMISSIONS.SUPPLIER_VIEW,
      PERMISSIONS.SUPPLIER_APPROVE,
      PERMISSIONS.AUDIT_VIEW,
      PERMISSIONS.ALERT_VIEW,
      PERMISSIONS.ALERT_HANDLE,
    ],
  },
  PRICING_OP: {
    portal: "internal",
    legacyRole: "PRICING_OP",
    user: {
      id: "dev-pricing-ops",
      name: "周婷",
      roleLabel: "定价运营",
      roleCodes: ["PRICING_OP"],
      identityKey: "PRICING_OP",
    },
    permissionCodes: [
      PERMISSIONS.MODEL_EDIT,
      PERMISSIONS.MODEL_VIEW,
      PERMISSIONS.PRICE_SYNC_VIEW,
      PERMISSIONS.PRICE_SYNC_APPROVE,
      PERMISSIONS.SUPPLIER_QUOTE_VIEW,
      PERMISSIONS.COST_VIEW,
      PERMISSIONS.COST_EDIT,
      PERMISSIONS.PRICING_POLICY_VIEW,
      PERMISSIONS.PRICING_POLICY_EDIT,
      PERMISSIONS.PRICE_BOOK_VIEW,
      PERMISSIONS.PRICE_BOOK_EDIT,
      PERMISSIONS.PRICE_BOOK_APPROVE,
      PERMISSIONS.CUSTOMER_QUOTE_VIEW,
      PERMISSIONS.CUSTOMER_QUOTE_EDIT,
      PERMISSIONS.CUSTOMER_QUOTE_APPROVE,
      PERMISSIONS.CUSTOMER_VIEW,
      PERMISSIONS.ALERT_VIEW,
      PERMISSIONS.ALERT_HANDLE,
    ],
  },
  FINANCE_OP: {
    portal: "internal",
    legacyRole: "VIEWER",
    user: {
      id: "dev-finance-ops",
      name: "许宁",
      roleLabel: "财务运营",
      roleCodes: ["FINANCE"],
      identityKey: "FINANCE_OP",
    },
    permissionCodes: [
      PERMISSIONS.CUSTOMER_VIEW,
      PERMISSIONS.FINANCE_VIEW,
      PERMISSIONS.FINANCE_EDIT,
      PERMISSIONS.AUDIT_VIEW,
      PERMISSIONS.ALERT_VIEW,
      PERMISSIONS.ALERT_HANDLE,
    ],
  },
  ADMIN_OP: {
    portal: "internal",
    legacyRole: "VIEWER",
    user: {
      id: "dev-admin-ops",
      name: "顾言",
      roleLabel: "系统管理员",
      identityKey: "ADMIN_OP",
    },
    permissionCodes: [
      PERMISSIONS.CUSTOMER_VIEW,
      PERMISSIONS.CUSTOMER_EDIT,
      PERMISSIONS.ORG_PERMISSION_VIEW,
      PERMISSIONS.ORG_PERMISSION_CONFIGURE,
      PERMISSIONS.AUDIT_VIEW,
      PERMISSIONS.ALERT_VIEW,
    ],
  },
  PURCHASING: {
    portal: "internal",
    legacyRole: "VIEWER",
    user: {
      id: "dev-purchasing",
      name: "陈昊",
      roleLabel: "采购审批",
      identityKey: "PURCHASING",
    },
    permissionCodes: [
      PERMISSIONS.SUPPLIER_VIEW,
      PERMISSIONS.SUPPLIER_QUOTE_VIEW,
      PERMISSIONS.SUPPLIER_QUOTE_APPROVE,
      PERMISSIONS.SUPPLIER_QUOTE_EDIT,
      PERMISSIONS.COST_VIEW,
      PERMISSIONS.ALERT_VIEW,
      PERMISSIONS.ALERT_HANDLE,
    ],
  },
  RETRO_OP: {
    portal: "internal",
    legacyRole: "VIEWER",
    user: {
      id: "dev-retro-op",
      name: "特权补录员",
      roleLabel: "报价补录",
      identityKey: "RETRO_OP",
    },
    permissionCodes: [
      PERMISSIONS.SUPPLIER_QUOTE_VIEW,
      PERMISSIONS.SUPPLIER_QUOTE_PRIVILEGE,
    ],
  },
  VIEWER: {
    portal: "internal",
    legacyRole: "VIEWER",
    user: {
      id: "dev-viewer",
      name: "只读观察员",
      roleLabel: "全模块只读",
      identityKey: "VIEWER",
    },
    permissionCodes: allViewPermissions,
  },
  SUPPLIER: {
    portal: "supplier",
    legacyRole: "VIEWER",
    user: {
      id: "dev-supplier",
      name: "云桥科技",
      roleLabel: "供应商操作员",
      identityKey: "SUPPLIER",
    },
    permissionCodes: [],
  },
  CUSTOMER: {
    portal: "customer",
    legacyRole: "VIEWER",
    user: {
      id: "dev-customer",
      name: "蓝海电商",
      roleLabel: "客户操作员",
      identityKey: "CUSTOMER",
    },
    permissionCodes: [],
  },
};

const STORAGE_KEY = "model-pricing-session-v1";
const authEnabled = import.meta.env.VITE_AUTH_ENABLED !== "false";
export const devAuthBypass = import.meta.env.DEV && !authEnabled;
export const authConfigurationBlocked = !import.meta.env.DEV && !authEnabled;

export const useSessionStore = defineStore("session", () => {
  const token = ref<string | null>(null);
  const user = ref<CurrentUser | null>(null);
  const portal = ref<PortalType | null>(null);
  const permissionCodes = ref<string[]>([]);
  const initialized = ref(false);
  const isDevSession = ref(false);
  const isAuthenticated = computed(() => Boolean(user.value));

  function persist() {
    if (!isDevSession.value || !user.value || !portal.value) return;
    sessionStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ identityKey: user.value.identityKey, portal: portal.value }),
    );
  }

  function startDevSession(identityKey: DevIdentityKey) {
    if (!devAuthBypass) return false;
    const identity = DEV_IDENTITIES[identityKey];
    token.value = null;
    user.value = identity.user;
    portal.value = identity.portal;
    permissionCodes.value = [...identity.permissionCodes];
    isDevSession.value = true;
    initialized.value = true;
    // Temporary bridge for the existing model/sync Mock pages during migration.
    legacyRole.value = identity.legacyRole;
    usePermissionStore().rebuild(permissionCodes.value, identity.user.roleCodes);
    persist();
    return true;
  }

  function startDevSessionForPortal(targetPortal: PortalType) {
    const defaults: Record<PortalType, DevIdentityKey> = {
      internal: "MODEL_OPS",
      supplier: "SUPPLIER",
      customer: "CUSTOMER",
    };
    return startDevSession(defaults[targetPortal]);
  }

  function restoreSession() {
    if (initialized.value) return;
    if (devAuthBypass) {
      try {
        const saved = JSON.parse(sessionStorage.getItem(STORAGE_KEY) || "null") as {
          identityKey?: DevIdentityKey;
        } | null;
        if (saved?.identityKey && DEV_IDENTITIES[saved.identityKey]) {
          startDevSession(saved.identityKey);
          return;
        }
      } catch {
        sessionStorage.removeItem(STORAGE_KEY);
      }
      startDevSession("MODEL_OPS");
      return;
    }
    // Bearer credentials stay in memory until the backend supports secure restoration.
    initialized.value = true;
  }

  function clear() {
    token.value = null;
    user.value = null;
    portal.value = null;
    permissionCodes.value = [];
    isDevSession.value = false;
    initialized.value = true;
    sessionStorage.removeItem(STORAGE_KEY);
    usePermissionStore().clear();
  }

  function startSession(result: LoginResult, targetPortal: PortalType, loginID: string) {
    const snapshot = result?.snapshot;
    if (!result?.token || typeof result.token !== "string" || !snapshot ||
        snapshot.portal_type !== targetPortal.toUpperCase() ||
        (snapshot.perms !== null && (!Array.isArray(snapshot.perms) || snapshot.perms.some(p => typeof p !== "string"))) ||
        (snapshot.roles !== null && (!Array.isArray(snapshot.roles) || snapshot.roles.some(r => !r || typeof r.code !== "string")))) {
      throw new Error("登录响应不符合约定，请联系管理员。");
    }
    assertContractId(snapshot.account_id);
    assertContractId(snapshot.operator_id);
    clear();
    token.value = result.token;
    portal.value = targetPortal;
    const roleCodes = snapshot.roles?.map(r => r.code) || [];
    user.value = { id: String(snapshot.account_id), name: loginID, roleCodes,
      roleLabel: roleCodes.join(" / ") || (targetPortal === "internal" ? "内部用户" : targetPortal === "supplier" ? "供应商" : "客户") };
    permissionCodes.value = [...(snapshot.perms || [])];
    legacyRole.value = "VIEWER";
    usePermissionStore().rebuild(permissionCodes.value, roleCodes);
  }

  return {
    token,
    user,
    portal,
    permissionCodes,
    initialized,
    isDevSession,
    isAuthenticated,
    restoreSession,
    startDevSession,
    startDevSessionForPortal,
    clear,
    startSession,
  };
});
