import type { RouteRecordRaw } from "vue-router";
import AppLayout from "../layouts/AppLayout.vue";
import Models from "../Models.vue";
import ModelApplicationReview from "../views/internal/models/ModelApplicationReview.vue";
import Sync from "../Sync.vue";
import ModulePlaceholderView from "../views/ModulePlaceholderView.vue";
import SupplierQuoteList from "../views/internal/supplierQuotes/SupplierQuoteList.vue";
import AlertList from "../views/internal/alerts/AlertList.vue";
import AuditLogList from "../views/internal/audit/AuditLogList.vue";
import CurrentCostBaselineList from "../views/internal/cost/CurrentCostBaselineList.vue";
import PriceBookList from "../views/internal/pricing/PriceBookList.vue";
import PricingPolicyList from "../views/internal/pricing/PricingPolicyList.vue";
import SupplierList from "../views/internal/suppliers/SupplierList.vue";
import CustomerWorkspace from "../views/internal/customers/CustomerWorkspace.vue";
import FinanceCenter from "../views/internal/finance/FinanceCenter.vue";
import InternalWorkbench from "../views/internal/workbench/InternalWorkbench.vue";
import OrgPermissionCenter from "../views/internal/orgPermissions/OrgPermissionCenter.vue";
import IntegrationCenter from "../views/internal/integration/IntegrationCenter.vue";
import { ROUTE_REQUIREMENTS } from "../domain/permissions";

const page = (
  path: string,
  routeKey: string,
  title: string,
  description: string,
  order: number,
): RouteRecordRaw => ({
  path,
  component: ModulePlaceholderView,
  meta: {
    requiresAuth: true,
    portal: "internal",
    routeKey,
    requiredPermission: ROUTE_REQUIREMENTS[routeKey],
    title,
    description,
    menu: true,
    order,
  },
});

export const internalRoutes: RouteRecordRaw = {
  path: "/internal",
  component: AppLayout,
  meta: { requiresAuth: true, portal: "internal" },
  children: [
    { path: "", redirect: "/internal/workbench" },
    {
      path: "workbench",
      component: InternalWorkbench,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.workbench",
        requiredPermission: ROUTE_REQUIREMENTS["internal.workbench"],
        title: "工作台",
        menu: true,
        order: 10,
      },
    },
    {
      path: "models",
      component: Models,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.models",
        requiredPermission: ROUTE_REQUIREMENTS["internal.models"],
        title: "模型管理",
        menu: true,
        order: 20,
      },
    },
    {
      path: "model-applications",
      component: ModelApplicationReview,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.modelApplications",
        requiredPermission: ROUTE_REQUIREMENTS["internal.modelApplications"],
        title: "模型申请审批",
        menu: true,
        order: 25,
      },
    },
    {
      path: "official-prices",
      component: Sync,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.officialPrices",
        requiredPermission: ROUTE_REQUIREMENTS["internal.officialPrices"],
        title: "官方价格同步",
        menu: true,
        order: 30,
      },
    },
    {
      path: "suppliers",
      component: SupplierList,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.suppliers",
        requiredPermission: ROUTE_REQUIREMENTS["internal.suppliers"],
        title: "供应商管理",
        menu: true,
        order: 40,
      },
    },
    {
      path: "supplier-quotes",
      component: SupplierQuoteList,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.supplierQuotes",
        requiredPermission: ROUTE_REQUIREMENTS["internal.supplierQuotes"],
        title: "供应商报价",
        menu: true,
        order: 50,
      },
    },
    {
      path: "cost",
      component: CurrentCostBaselineList,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.cost",
        requiredPermission: ROUTE_REQUIREMENTS["internal.cost"],
        title: "成本管理",
        menu: true,
        order: 60,
      },
    },
    {
      path: "pricing/policies",
      component: PricingPolicyList,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.pricingPolicies",
        requiredPermission: ROUTE_REQUIREMENTS["internal.pricingPolicies"],
        title: "定价策略",
        menu: true,
        order: 70,
      },
    },
    {
      path: "price-books",
      component: PriceBookList,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.priceBooks",
        requiredPermission: ROUTE_REQUIREMENTS["internal.priceBooks"],
        title: "价目表",
        menu: true,
        order: 80,
      },
    },
    {
      path: "customers",
      component: CustomerWorkspace,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.customers",
        requiredPermission: ROUTE_REQUIREMENTS["internal.customers"],
        title: "客户与报价",
        menu: true,
        order: 90,
      },
    },
    {
      path: "finance",
      component: FinanceCenter,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.finance",
        requiredPermission: ROUTE_REQUIREMENTS["internal.finance"],
        title: "财务信息",
        menu: true,
        order: 100,
      },
    },
    {
      path: "alerts",
      component: AlertList,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.alerts",
        requiredPermission: ROUTE_REQUIREMENTS["internal.alerts"],
        title: "告警中心",
        menu: true,
        order: 110,
      },
    },
    {
      path: "audit",
      component: AuditLogList,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.audit",
        requiredPermission: ROUTE_REQUIREMENTS["internal.audit"],
        title: "审计日志",
        menu: true,
        order: 120,
      },
    },
    {
      path: "org-permissions",
      component: OrgPermissionCenter,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.orgPermissions",
        requiredPermission: ROUTE_REQUIREMENTS["internal.orgPermissions"],
        title: "组织与权限",
        menu: true,
        order: 130,
      },
    },
    {
      path: "integration",
      component: IntegrationCenter,
      meta: {
        requiresAuth: true,
        portal: "internal",
        routeKey: "internal.integration",
        requiredPermission: ROUTE_REQUIREMENTS["internal.integration"],
        title: "系统对接",
        menu: true,
        order: 140,
      },
    },
  ],
};
