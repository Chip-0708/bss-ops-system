import { defineStore } from "pinia";
import { ref } from "vue";
import { ROUTE_REQUIREMENTS } from "../domain/permissions";

export const usePermissionStore = defineStore("permission", () => {
  const actionPermissionSet = ref<Set<string>>(new Set());
  const routePermissionSet = ref<Set<string>>(new Set());

  function rebuild(permissionCodes: string[], roleCodes: string[] = []) {
    actionPermissionSet.value = new Set(permissionCodes);
    routePermissionSet.value = new Set(
      Object.entries(ROUTE_REQUIREMENTS)
        .filter(
          ([routeKey, requiredPermission]) =>
            requiredPermission === null ||
            actionPermissionSet.value.has(requiredPermission) ||
            (routeKey === "internal.priceBooks" && roleCodes.includes("FINANCE")),
        )
        .map(([routeKey]) => routeKey),
    );
  }

  function clear() {
    actionPermissionSet.value = new Set();
    routePermissionSet.value = new Set();
  }

  const canAction = (permission: string) =>
    actionPermissionSet.value.has(permission);
  const canRoute = (routeKey: string) => routePermissionSet.value.has(routeKey);

  return {
    actionPermissionSet,
    routePermissionSet,
    rebuild,
    clear,
    canAction,
    canRoute,
  };
});
