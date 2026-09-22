import type { Pinia } from "pinia";
import type { Router } from "vue-router";
import { watch } from "vue";
import {
  authConfigurationBlocked,
  devAuthBypass,
  useSessionStore,
  type PortalType,
} from "../stores/session";
import { usePermissionStore } from "../stores/permission";

const loginPath: Record<PortalType, string> = {
  internal: "/internal/login",
  supplier: "/supplier/login",
  customer: "/customer/login",
};

export function installRouterGuards(router: Router, pinia: Pinia) {
  watch(() => useSessionStore(pinia).isAuthenticated, (authenticated) => {
    const current = router.currentRoute.value;
    if (!authenticated && current.meta.requiresAuth) {
      void router.replace({ path: loginPath[current.meta.portal || "internal"], query: { redirect: current.fullPath } });
    }
  });
  router.beforeEach((to) => {
    const session = useSessionStore(pinia);
    const permissions = usePermissionStore(pinia);
    session.restoreSession();

    if (authConfigurationBlocked && to.path !== "/configuration-error") {
      return "/configuration-error";
    }

    const targetPortal = to.meta.portal as PortalType | undefined;
    if (
      devAuthBypass &&
      targetPortal &&
      to.meta.requiresAuth &&
      session.portal !== targetPortal
    ) {
      session.startDevSessionForPortal(targetPortal);
    }

    if (to.meta.requiresAuth && !session.isAuthenticated) {
      const portal = targetPortal || "internal";
      return {
        path: loginPath[portal],
        query: { redirect: to.fullPath },
      };
    }

    if (to.meta.requiresAuth && targetPortal && session.portal && targetPortal !== session.portal) {
      return "/403";
    }

    const routeKey = to.meta.routeKey as string | undefined;
    if (routeKey && !permissions.canRoute(routeKey)) return "/403";
    return true;
  });
}
