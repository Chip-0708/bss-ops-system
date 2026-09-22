import type { RouteRecordRaw } from "vue-router";
import AppLayout from "../layouts/AppLayout.vue";
import CustomerPortal from "../views/customer/CustomerPortal.vue";

const page = (
  path: string,
  routeKey: string,
  title: string,
  description: string,
  order: number,
): RouteRecordRaw => ({
  path,
  component: CustomerPortal,
  meta: {
    requiresAuth: true,
    portal: "customer",
    routeKey,
    title,
    description,
    menu: true,
    order,
  },
});

export const customerRoutes: RouteRecordRaw = {
  path: "/customer",
  component: AppLayout,
  meta: { requiresAuth: true, portal: "customer" },
  children: [
    { path: "", redirect: "/customer/home" },
    page("home", "customer.home", "客户首页", "可用价格、余额和通知摘要。", 10),
    page("price-book", "customer.priceBook", "我的价目表", "当前生效价目表和模型售价。", 20),
    page("quotes", "customer.quotes", "我的报价", "历史报价、状态和接受结果。", 30),
    page("contracts", "customer.contracts", "我的合同", "合同价格快照和有效期。", 40),
    page("billing", "customer.billing", "账单与余额", "账单、余额、授信和押金只读信息。", 50),
    page("notifications", "customer.notifications", "通知中心", "涨价、退役及业务通知。", 60),
  ],
};
