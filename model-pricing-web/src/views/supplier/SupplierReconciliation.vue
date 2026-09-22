<script setup lang="ts">
import { formatDateTime } from "../../domain/date";
import { onMounted, reactive, ref } from "vue";
import { supplierReconciliationApi } from "../../api/supplierReconciliation";
import type { SupplierReconciliationDetailDTO, SupplierReconciliationSummaryDTO } from "../../api/supplierReconciliation.types";
import { ApiError } from "../../domain/common";
import { RECONCILIATION_STATUS, type ReconciliationStatus } from "../../domain/status";

const rows = ref<SupplierReconciliationSummaryDTO[]>([]);
const loading = ref(false);
const error = ref("");
const filters = reactive({ search: "", status: "" as ReconciliationStatus | "", period: "" });
const pagination = reactive({ page: 1, size: 10, total: 0 });
const detailOpen = ref(false);
const detailLoading = ref(false);
const detailError = ref("");
const detail = ref<SupplierReconciliationDetailDTO>();
const invoiceLabels = { NOT_REQUIRED: "无需开票", PENDING: "待收票", RECEIVED: "已收票" };

const errorMessage = (value: unknown) => value instanceof ApiError ? value.message : "操作失败，请稍后重试。";
const statusConfig = (status: ReconciliationStatus) => RECONCILIATION_STATUS[status];
const formatTime = (value?: string | null) => formatDateTime(value, "暂缺", false);

async function load() {
  loading.value = true; error.value = "";
  try {
    const result = await supplierReconciliationApi.list({ page: pagination.page, size: pagination.size, search: filters.search.trim() || undefined, status: filters.status || undefined, period: filters.period || undefined });
    rows.value = result.list; pagination.total = result.total;
  } catch (value) {
    rows.value = []; pagination.total = 0; error.value = errorMessage(value);
  } finally { loading.value = false; }
}
function query() { pagination.page = 1; void load(); }
function reset() { filters.search = ""; filters.status = ""; filters.period = ""; query(); }
function changePage(page: number) { pagination.page = page; void load(); }
async function openDetail(row: SupplierReconciliationSummaryDTO) {
  detailOpen.value = true; detailLoading.value = true; detailError.value = ""; detail.value = undefined;
  try { detail.value = await supplierReconciliationApi.get(row.id); }
  catch (value) { detailError.value = errorMessage(value); }
  finally { detailLoading.value = false; }
}
onMounted(load);
</script>

<template>
  <section class="page-heading"><div><div class="eyebrow">RECONCILIATION</div><h1>结算与对账</h1><p>查看本供应商的对账周期、应付金额、差异和结算状态。</p></div><el-button :loading="loading" @click="load">刷新对账单</el-button></section>
  <div class="stats reconciliation-stats"><div><span>对账单</span><strong>{{ pagination.total }}<small>个周期</small></strong></div><div><span>本页核对中</span><strong>{{ rows.filter((row) => ['PENDING','CHECKING'].includes(row.status)).length }}<small>后端状态</small></strong></div><div><span>本页有差异</span><strong>{{ rows.filter((row) => row.status === 'DISPUTED').length }}<small>需联系运营</small></strong></div><div><span>本页已结算</span><strong>{{ rows.filter((row) => row.status === 'SETTLED').length }}<small class="green">财务结果</small></strong></div></div>
  <section class="panel reconciliation-panel">
    <div class="section-title"><h2>对账单 <span>{{ pagination.total }}</span></h2><span class="muted">本阶段只读</span></div>
    <div class="filters reconciliation-filters"><el-input v-model="filters.search" clearable aria-label="对账单搜索" placeholder="搜索对账单编号" @keyup.enter="query" /><el-date-picker v-model="filters.period" type="month" value-format="YYYY-MM" aria-label="对账周期" placeholder="全部周期" /><el-select v-model="filters.status" clearable aria-label="对账状态" placeholder="全部状态"><el-option v-for="(config, status) in RECONCILIATION_STATUS" :key="status" :label="config.label" :value="status" /></el-select><el-button type="primary" @click="query">查询</el-button><el-button text @click="reset">重置</el-button></div>
    <el-alert v-if="error" :title="error" type="error" show-icon :closable="false"><el-button text @click="load">重新加载</el-button></el-alert>
    <div v-loading="loading" class="reconciliation-list"><el-empty v-if="!rows.length && !loading && !error" description="没有符合条件的对账单" /><el-table v-else :data="rows" row-key="id"><el-table-column label="对账单 / 周期" min-width="210"><template #default="{ row }"><b>{{ row.statementNo }}</b><div class="mono muted">{{ row.period }}</div></template></el-table-column><el-table-column label="用量金额" min-width="160"><template #default="{ row }"><span class="mono">{{ row.usageAmount }} {{ row.currency }}</span></template></el-table-column><el-table-column label="调整 / 税额" min-width="170"><template #default="{ row }"><span class="mono">{{ row.adjustmentAmount }}</span><div class="muted">税额 {{ row.taxAmount }}</div></template></el-table-column><el-table-column label="应付金额" min-width="175"><template #default="{ row }"><b class="mono">{{ row.payableAmount }} {{ row.currency }}</b></template></el-table-column><el-table-column label="状态" width="115"><template #default="{ row }"><el-tag :type="statusConfig(row.status).type">{{ statusConfig(row.status).label }}</el-tag></template></el-table-column><el-table-column label="生成 / 到期" min-width="180"><template #default="{ row }">{{ formatTime(row.generatedAt) }}<div class="muted">到期 {{ formatTime(row.dueAt) }}</div></template></el-table-column><el-table-column label="操作" width="90"><template #default="{ row }"><el-button link type="primary" @click="openDetail(row)">查看详情</el-button></template></el-table-column></el-table></div>
    <div class="catalog-pagination"><el-pagination :current-page="pagination.page" :page-size="pagination.size" :total="pagination.total" layout="total, prev, pager, next" @current-change="changePage" /></div>
  </section>
  <el-drawer v-model="detailOpen" v-loading="detailLoading" title="对账单详情" size="min(980px, 96vw)"><el-alert v-if="detailError" :title="detailError" type="error" show-icon :closable="false" /><template v-if="detail"><div class="reconciliation-detail-head"><div><div class="eyebrow">{{ detail.statementNo }}</div><h2>{{ detail.period }} 结算周期</h2><p class="muted">生成于 {{ formatTime(detail.generatedAt) }}</p></div><el-tag :type="statusConfig(detail.status).type">{{ statusConfig(detail.status).label }}</el-tag></div><el-alert title="页面不会对字符串金额进行浮点重算；合计、调整、税额与结算状态均以后端和财务系统为准。" type="info" show-icon :closable="false" /><el-descriptions :column="3" border class="reconciliation-descriptions"><el-descriptions-item label="用量金额">{{ detail.usageAmount }} {{ detail.currency }}</el-descriptions-item><el-descriptions-item label="调整金额">{{ detail.adjustmentAmount }} {{ detail.currency }}</el-descriptions-item><el-descriptions-item label="应付金额"><b>{{ detail.payableAmount }} {{ detail.currency }}</b></el-descriptions-item><el-descriptions-item label="结算账户">{{ detail.settlementAccountMasked }}</el-descriptions-item><el-descriptions-item label="票据状态">{{ invoiceLabels[detail.invoiceStatus] }}</el-descriptions-item><el-descriptions-item label="到期时间">{{ formatTime(detail.dueAt) }}</el-descriptions-item></el-descriptions><h3>对账明细</h3><el-empty v-if="!detail.lines.length" description="服务端未返回逐 SKU 明细" :image-size="70" /><el-table v-else :data="detail.lines" border><el-table-column label="SKU / 组件" min-width="210"><template #default="{ row }"><b>{{ row.skuName }}</b><div class="mono muted">{{ row.skuCode }} · {{ row.component }}</div></template></el-table-column><el-table-column label="用量" min-width="150"><template #default="{ row }"><span class="mono">{{ row.usageQuantity }}</span><div class="muted">{{ row.unit }}</div></template></el-table-column><el-table-column label="单价" min-width="145"><template #default="{ row }"><span class="mono">{{ row.unitPrice }} {{ detail.currency }}</span></template></el-table-column><el-table-column label="金额 / 调整" min-width="190"><template #default="{ row }"><span class="mono">{{ row.amount }}</span><div class="muted">调整 {{ row.adjustmentAmount }}</div></template></el-table-column><el-table-column prop="note" label="说明" min-width="190" /></el-table><h3>结算说明</h3><ul class="reconciliation-notes"><li v-for="note in detail.notes" :key="note">{{ note }}</li></ul></template></el-drawer>
</template>

<style scoped>
.reconciliation-stats { margin-bottom: 22px; }.reconciliation-panel { padding: 22px; }.reconciliation-filters { grid-template-columns: minmax(230px, 1fr) 160px 160px auto auto; margin-bottom: 18px; }.reconciliation-list { min-height: 280px; }.reconciliation-detail-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; margin-bottom: 18px; }.reconciliation-detail-head h2 { margin: 8px 0 4px; }.reconciliation-descriptions { margin-top: 18px; }.reconciliation-notes { padding: 14px 18px 14px 34px; border-radius: 8px; background: var(--app-surface-muted); line-height: 1.8; }@media (max-width: 900px) { .reconciliation-filters { grid-template-columns: 1fr; }.reconciliation-detail-head { flex-direction: column; } }
</style>
