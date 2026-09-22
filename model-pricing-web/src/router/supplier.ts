import type { RouteRecordRaw } from "vue-router";
import AppLayout from "../layouts/AppLayout.vue";
import SupplierHome from "../views/supplier/SupplierHome.vue";
import SupplierQuoteList from "../views/supplier/SupplierQuoteList.vue";
import SupplierQuoteNew from "../views/supplier/SupplierQuoteNew.vue";
import SupplierQuoteImport from "../views/supplier/SupplierQuoteImport.vue";
import SupplierModelApplications from "../views/supplier/SupplierModelApplications.vue";
import SupplierProfile from "../views/supplier/SupplierProfile.vue";
import SupplierQualifications from "../views/supplier/SupplierQualifications.vue";
import SupplierReconciliation from "../views/supplier/SupplierReconciliation.vue";

export const supplierRoutes: RouteRecordRaw = {
  path: "/supplier",
  component: AppLayout,
  meta: { requiresAuth: true, portal: "supplier" },
  children: [
    { path: "", redirect: "/supplier/home" },
    { path: "quotes/:id/renew", component: SupplierQuoteNew, meta: { requiresAuth: true, portal: "supplier", routeKey: "supplier.quoteNew", title: "续报新版本", menu: false } },
    {
      path: "home",
      component: SupplierHome,
      meta: { requiresAuth: true, portal: "supplier", routeKey: "supplier.home", title: "供应商首页", menu: true, order: 10 },
    },
    {
      path: "quotes",
      component: SupplierQuoteList,
      meta: { requiresAuth: true, portal: "supplier", routeKey: "supplier.quotes", title: "报价历史", menu: true, order: 20 },
    },
    {
      path: "quotes/new",
      component: SupplierQuoteNew,
      meta: { requiresAuth: true, portal: "supplier", routeKey: "supplier.quoteNew", title: "新建报价", menu: true, order: 30 },
    },
    {
      path: "quotes/import",
      component: SupplierQuoteImport,
      meta: { requiresAuth: true, portal: "supplier", routeKey: "supplier.quoteImport", title: "批量导入", menu: true, order: 40 },
    },
    {
      path: "model-applications",
      component: SupplierModelApplications,
      meta: { requiresAuth: true, portal: "supplier", routeKey: "supplier.modelApplications", title: "新模型申请", menu: true, order: 50 },
    },
    {
      path: "profile",
      component: SupplierProfile,
      meta: { requiresAuth: true, portal: "supplier", routeKey: "supplier.profile", title: "主体档案", menu: true, order: 60 },
    },
    {
      path: "qualifications",
      component: SupplierQualifications,
      meta: { requiresAuth: true, portal: "supplier", routeKey: "supplier.qualifications", title: "资质管理", menu: true, order: 70 },
    },
    {
      path: "reconciliation",
      component: SupplierReconciliation,
      meta: { requiresAuth: true, portal: "supplier", routeKey: "supplier.reconciliation", title: "结算与对账", menu: true, order: 80 },
    },
  ],
};
