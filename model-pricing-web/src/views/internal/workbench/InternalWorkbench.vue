<script setup lang="ts">
import { formatDateTime } from "../../../domain/date";
import { computed, onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import { workbenchApi, type WorkbenchDTO, type WorkbenchMetricDTO, type WorkbenchTaskDTO } from "../../../modules/workbench/api/workbench";
import { ApiError } from "../../../domain/common";
import { usePermissionStore } from "../../../stores/permission";
import { useSessionStore } from "../../../stores/session";

const router = useRouter();
const permissions = usePermissionStore();
const session = useSessionStore();
const data = ref<WorkbenchDTO>();
const loading = ref(false);
const error = ref("");

const quickLinks = [
  { routeKey: "internal.models", path: "/internal/models", title: "模型管理", description: "模型主数据与状态" },
  { routeKey: "internal.modelApplications", path: "/internal/model-applications", title: "模型申请审批", description: "审核供应商新模型申请" },
  { routeKey: "internal.suppliers", path: "/internal/suppliers", title: "供应商管理", description: "档案、资质与供给" },
  { routeKey: "internal.supplierQuotes", path: "/internal/supplier-quotes", title: "供应商报价", description: "报价查询与审批" },
  { routeKey: "internal.cost", path: "/internal/cost", title: "成本管理", description: "成本基线与比价" },
  { routeKey: "internal.pricingPolicies", path: "/internal/pricing/policies", title: "定价策略", description: "策略规则与版本" },
  { routeKey: "internal.priceBooks", path: "/internal/price-books", title: "价目表", description: "售价草稿与审批" },
  { routeKey: "internal.customers", path: "/internal/customers", title: "客户与报价", description: "客户、报价与合同" },
  { routeKey: "internal.finance", path: "/internal/finance", title: "财务信息", description: "汇率、授信与押金" },
  { routeKey: "internal.alerts", path: "/internal/alerts", title: "告警中心", description: "业务异常与处理" },
  { routeKey: "internal.audit", path: "/internal/audit", title: "审计日志", description: "操作记录与影响" },
];
const accessibleQuickLinks = computed(() => quickLinks.filter((item) => permissions.canRoute(item.routeKey)));
const visibleTasks = computed(() => data.value?.tasks || []);
const errorMessage = (value: unknown) => value instanceof ApiError ? value.message : value instanceof Error ? value.message : "工作台加载失败，请稍后重试。";
const formatTime = (value?: string | null) => formatDateTime(value, "暂缺", false);
const metricClass = (tone: WorkbenchMetricDTO["tone"]) => `metric-${tone.toLowerCase()}`;
const taskType = (level: WorkbenchTaskDTO["level"]) => level === "URGENT" ? "danger" : level === "WARNING" ? "warning" : "info";

async function load() {
  loading.value = true; error.value = "";
  try { data.value = await workbenchApi.get(); }
  catch (value) { data.value = undefined; error.value = errorMessage(value); }
  finally { loading.value = false; }
}
function go(path: string) { void router.push(path); }
onMounted(load);
</script>

<template>
  <section class="page-heading">
    <div><div class="eyebrow">INTERNAL WORKBENCH</div><h1>{{ session.user?.name }}，你好</h1><p>这里汇总当前身份可见的关键指标、待办和业务入口。</p></div>
    <el-button :loading="loading" @click="load">刷新工作台</el-button>
  </section>

  <el-alert v-if="error" :title="error" type="error" show-icon :closable="false"><el-button text @click="load">重新加载</el-button></el-alert>
  <div v-loading="loading" class="workbench-body">
    <template v-if="data">
      <div class="workbench-metrics">
        <div v-for="metric in data.metrics" :key="metric.id" class="workbench-metric" :class="metricClass(metric.tone)"><span>{{ metric.label }}</span><strong>{{ metric.value }}<small>{{ metric.suffix }}</small></strong><p>{{ metric.hint }}</p></div>
      </div>

      <div class="workbench-grid">
        <section class="panel workbench-panel">
          <div class="section-title"><h2>我的待办 <span>{{ visibleTasks.length }}</span></h2><span class="muted">按当前身份返回</span></div>
          <el-empty v-if="!visibleTasks.length" description="当前没有需要处理的待办" :image-size="72" />
          <button v-for="task in visibleTasks" :key="task.id" type="button" class="task-row" @click="go(task.routePath)"><span class="task-count">{{ task.count }}</span><span class="task-copy"><b>{{ task.title }}</b><small>{{ task.module }} · {{ task.description }}</small></span><el-tag :type="taskType(task.level)" size="small">{{ task.level === 'URGENT' ? '优先处理' : task.level === 'WARNING' ? '需关注' : '普通' }}</el-tag><span class="task-arrow">→</span></button>
        </section>

        <section class="panel workbench-panel">
          <div class="section-title"><h2>业务提醒</h2><span class="muted">{{ formatTime(data.generatedAt) }}</span></div>
          <div v-for="notice in data.notices" :key="notice.id" class="notice-row"><span class="notice-dot"></span><div><b>{{ notice.title }}</b><p>{{ notice.description }}</p><small>{{ formatTime(notice.occurredAt) }}</small></div></div>
        </section>
      </div>

      <section class="panel shortcut-panel">
        <div class="section-title"><h2>快捷入口</h2><span class="muted">仅显示当前身份允许进入的模块</span></div>
        <div class="shortcut-grid"><button v-for="item in accessibleQuickLinks" :key="item.routeKey" type="button" class="shortcut-card" @click="go(item.path)"><span class="shortcut-mark">◇</span><span><b>{{ item.title }}</b><small>{{ item.description }}</small></span><span>→</span></button></div>
      </section>
    </template>
    <el-empty v-else-if="!loading && !error" description="当前没有可展示的工作台数据" />
  </div>
</template>

<style scoped>
.workbench-body { min-height: 420px; }.workbench-metrics { display: grid; grid-template-columns: repeat(4, 1fr); gap: 17px; margin-bottom: 22px; }.workbench-metric { padding: 20px 22px; border: 1px solid var(--app-border); border-top: 3px solid var(--app-accent); border-radius: 12px; background: var(--app-surface); }.workbench-metric > span,.workbench-metric p { color: var(--app-text-muted); font-size: 12px; }.workbench-metric strong { display: block; margin: 13px 0 8px; font-size: 29px; }.workbench-metric strong small { margin-left: 7px; color: var(--app-text-muted); font-size: 12px; font-weight: 400; }.workbench-metric p { margin: 0; }.metric-success { border-top-color: var(--app-success); }.metric-warning { border-top-color: var(--app-warning); }.metric-danger { border-top-color: var(--app-danger); }.workbench-grid { display: grid; grid-template-columns: 1.2fr 0.8fr; gap: 20px; margin-bottom: 20px; }.workbench-panel,.shortcut-panel { padding: 22px; }.task-row { display: flex; width: 100%; align-items: center; gap: 14px; padding: 15px 4px; border: 0; border-bottom: 1px solid var(--app-border); background: transparent; color: var(--app-text-primary); text-align: left; }.task-row:hover { background: var(--app-surface-muted); }.task-count { display: grid; width: 34px; height: 34px; place-items: center; border-radius: 9px; background: var(--app-hover-bg); color: var(--app-accent); font-weight: 700; }.task-copy { display: grid; flex: 1; gap: 5px; }.task-copy small,.notice-row p,.notice-row small,.shortcut-card small { color: var(--app-text-muted); }.task-arrow { color: var(--app-text-muted); }.notice-row { display: flex; gap: 12px; padding: 14px 0; border-bottom: 1px solid var(--app-border); }.notice-dot { flex: 0 0 8px; width: 8px; height: 8px; margin-top: 6px; border-radius: 50%; background: var(--app-accent); }.notice-row p { margin: 6px 0; font-size: 12px; line-height: 1.6; }.notice-row small { font-size: 11px; }.shortcut-grid { display: grid; grid-template-columns: repeat(5, 1fr); gap: 12px; }.shortcut-card { display: flex; align-items: center; gap: 10px; padding: 16px; border: 1px solid var(--app-border); border-radius: 9px; background: var(--app-surface); color: var(--app-text-primary); text-align: left; }.shortcut-card:hover { border-color: var(--app-accent); background: var(--app-hover-bg); }.shortcut-card > span:nth-child(2) { display: grid; flex: 1; gap: 5px; }.shortcut-mark { color: var(--app-accent); font-size: 18px; }@media (max-width: 1100px) { .workbench-metrics,.shortcut-grid { grid-template-columns: repeat(2, 1fr); }.workbench-grid { grid-template-columns: 1fr; } }@media (max-width: 700px) { .workbench-metrics,.shortcut-grid { grid-template-columns: 1fr; } }
</style>
