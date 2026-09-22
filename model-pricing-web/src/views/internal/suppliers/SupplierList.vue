<script setup lang="ts">
import { onMounted, reactive, ref, watch } from "vue";
import { suppliersApi } from "../../../api/suppliers";
import type { SupplierDetailDTO, SupplierSummaryDTO } from "../../../api/suppliers.types";
import { ApiError } from "../../../domain/common";
import { formatDateTime } from "../../../domain/date";
import type { StatusTagType } from "../../../domain/status";

const supplierStatuses = { ACTIVE: { label: "合作中", type: "success" }, INACTIVE: { label: "已停用", type: "info" } } as const;
const qualificationStatuses = { VALID: { label: "有效", type: "success" }, EXPIRING: { label: "即将到期", type: "warning" }, FROZEN: { label: "已冻结", type: "danger" } } as const;
const settlementStatuses = { NORMAL: { label: "正常", type: "success" }, WARNING: { label: "预警", type: "warning" }, FROZEN: { label: "已冻结", type: "danger" } } as const;
function statusDisplay(status: string, known: Record<string, { label: string; type: StatusTagType }>) {
  return known[status] ?? { label: `未知状态（${status || "空值"}）`, type: "info" as const };
}
const supplierStatus = (status: string) => statusDisplay(status, supplierStatuses);
const qualificationStatus = (status: string) => statusDisplay(status, qualificationStatuses);
const settlementStatus = (status: string) => statusDisplay(status, settlementStatuses);
const missing = (value: string | number | null | undefined) => value == null || value === "" ? "无权限或未提供" : value;
const formatTime = (value?: string | null) => formatDateTime(value, "暂缺", false);

const rows = ref<SupplierSummaryDTO[]>([]);
const loading = ref(false);
const error = ref("");
const filters = reactive({ keyword: "", status: "", qualStatus: "" });
const pagination = reactive({ page: 1, size: 10, total: 0 });
const detailOpen = ref(false);
const detailLoading = ref(false);
const detailError = ref("");
const detail = ref<SupplierDetailDTO>();
let listRequest = 0;
let detailRequest = 0;
const errorMessage = (value: unknown) => value instanceof ApiError ? value.message : value instanceof Error ? value.message : "操作失败，请稍后重试。";

async function load() {
  const request = ++listRequest;
  loading.value = true;
  error.value = "";
  try {
    const result = await suppliersApi.list({ page: pagination.page, size: pagination.size,
      keyword: filters.keyword.trim() || undefined, status: filters.status || undefined,
      qual_status: filters.qualStatus || undefined });
    if (request !== listRequest) return;
    rows.value = result.list;
    pagination.total = result.total;
  } catch (value) {
    if (request !== listRequest) return;
    rows.value = [];
    pagination.total = 0;
    error.value = errorMessage(value);
  } finally {
    if (request === listRequest) loading.value = false;
  }
}
function query() { pagination.page = 1; void load(); }
function reset() { Object.assign(filters, { keyword: "", status: "", qualStatus: "" }); query(); }
function changePage(page: number) { pagination.page = page; void load(); }
async function openDetail(row: SupplierSummaryDTO) {
  const request = ++detailRequest;
  detailOpen.value = true;
  detailLoading.value = true;
  detailError.value = "";
  detail.value = undefined;
  try {
    const result = await suppliersApi.get(row.id);
    if (request === detailRequest && detailOpen.value) detail.value = result;
  } catch (value) {
    if (request === detailRequest && detailOpen.value) detailError.value = errorMessage(value);
  } finally {
    if (request === detailRequest) detailLoading.value = false;
  }
}
watch(detailOpen, (open) => {
  if (!open) { detailRequest++; detailLoading.value = false; detail.value = undefined; }
});
onMounted(load);
</script>

<template>
  <section class="page-heading">
    <div><div class="eyebrow">SUPPLIER DIRECTORY</div><h1>供应商管理</h1><p>查询供应商主体档案、资质状态与结算状态。</p></div>
    <el-button :loading="loading" @click="load">刷新档案</el-button>
  </section>

  <div class="stats supplier-stats">
    <div><span>供应商</span><strong>{{ pagination.total }}<small>家</small></strong></div>
    <div><span>本页合作中</span><strong>{{ rows.filter(row => row.status === 'ACTIVE').length }}<small class="green">可合作</small></strong></div>
    <div><span>本页资质临期</span><strong>{{ rows.filter(row => row.qualStatus === 'EXPIRING').length }}<small>需跟进</small></strong></div>
    <div><span>本页在供模型</span><strong>{{ rows.reduce((total, row) => total + row.skuCount, 0) }}<small>服务端结果</small></strong></div>
  </div>

  <section class="panel supplier-panel">
    <div class="section-title"><h2>供应商档案 <span>{{ pagination.total }}</span></h2><span class="muted">只读查询</span></div>
    <div class="filters supplier-filters">
      <el-input v-model="filters.keyword" clearable aria-label="供应商搜索" placeholder="搜索供应商名称" @keyup.enter="query" />
      <el-select v-model="filters.status" clearable aria-label="合作状态筛选" placeholder="全部合作状态"><el-option v-for="(config, status) in supplierStatuses" :key="status" :label="config.label" :value="status" /></el-select>
      <el-select v-model="filters.qualStatus" clearable aria-label="资质状态筛选" placeholder="全部资质状态"><el-option v-for="(config, status) in qualificationStatuses" :key="status" :label="config.label" :value="status" /></el-select>
      <el-button type="primary" @click="query">查询</el-button><el-button text @click="reset">重置</el-button>
    </div>
    <el-alert v-if="error" :title="error" type="error" show-icon :closable="false"><el-button text @click="load">重新加载</el-button></el-alert>
    <div v-loading="loading" class="supplier-list">
      <el-empty v-if="!rows.length && !loading && !error" description="没有符合条件的供应商" />
      <el-table v-else :data="rows" row-key="id">
        <el-table-column label="供应商" min-width="190"><template #default="{ row }"><b>{{ row.legalName }}</b><div class="mono muted">ID {{ row.id }}</div></template></el-table-column>
        <el-table-column label="合作状态" width="115"><template #default="{ row }"><el-tag :type="supplierStatus(row.status).type">{{ supplierStatus(row.status).label }}</el-tag></template></el-table-column>
        <el-table-column label="资质状态" width="125"><template #default="{ row }"><el-tag :type="qualificationStatus(row.qualStatus).type">{{ qualificationStatus(row.qualStatus).label }}</el-tag></template></el-table-column>
        <el-table-column label="结算状态" width="115"><template #default="{ row }"><el-tag :type="settlementStatus(row.settleStatus).type">{{ settlementStatus(row.settleStatus).label }}</el-tag></template></el-table-column>
        <el-table-column label="在供概况" min-width="150"><template #default="{ row }">{{ row.skuCount }} 个模型<div class="muted">{{ row.effectiveQuoteCount }} 份生效报价 · {{ row.expiringSoon }} 份临期</div></template></el-table-column>
        <el-table-column label="归属采购 / 更新时间" min-width="170"><template #default="{ row }">{{ row.ownerProcurementName }}<div class="muted">{{ formatTime(row.updatedAt) }}</div></template></el-table-column>
        <el-table-column label="操作" width="90"><template #default="{ row }"><el-button link type="primary" @click="openDetail(row)">查看详情</el-button></template></el-table-column>
      </el-table>
    </div>
    <div class="catalog-pagination"><el-pagination :current-page="pagination.page" :page-size="pagination.size" :total="pagination.total" layout="total, prev, pager, next" @current-change="changePage" /></div>
  </section>

  <el-drawer v-model="detailOpen" v-loading="detailLoading" title="供应商档案" size="min(920px, 96vw)">
    <el-alert v-if="detailError" :title="detailError" type="error" show-icon :closable="false" />
    <template v-if="detail">
      <div class="supplier-detail-head">
        <div><div class="eyebrow">供应商 ID {{ detail.id }}</div><h2>{{ detail.legalName }}</h2></div>
        <div class="supplier-detail-tags"><el-tag :type="supplierStatus(detail.status).type">{{ supplierStatus(detail.status).label }}</el-tag><el-tag :type="qualificationStatus(detail.qualStatus).type">资质{{ qualificationStatus(detail.qualStatus).label }}</el-tag><el-tag :type="settlementStatus(detail.settleStatus).type">结算{{ settlementStatus(detail.settleStatus).label }}</el-tag></div>
      </div>
      <el-descriptions :column="2" border class="supplier-descriptions">
        <el-descriptions-item label="主体 ID">{{ detail.subjectId }}</el-descriptions-item>
        <el-descriptions-item label="归属采购">{{ detail.ownerProcurementName }}</el-descriptions-item><el-descriptions-item label="结算币种">{{ detail.settlementCurrency }}</el-descriptions-item>
        <el-descriptions-item label="结算方式">{{ missing(detail.settleType) }}</el-descriptions-item><el-descriptions-item label="账期">{{ detail.billingCycle == null ? missing(detail.billingCycle) : `${detail.billingCycle} 天` }}</el-descriptions-item>
        <el-descriptions-item label="最低起充">{{ missing(detail.minRecharge) }}</el-descriptions-item><el-descriptions-item label="后付额度">{{ missing(detail.creditLine) }}</el-descriptions-item>
        <el-descriptions-item label="已用额度">{{ missing(detail.creditUsed) }}</el-descriptions-item><el-descriptions-item label="保证金">{{ missing(detail.depositAmount) }}</el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ formatTime(detail.createdAt) }}</el-descriptions-item><el-descriptions-item label="更新时间">{{ formatTime(detail.updatedAt) }}</el-descriptions-item>
      </el-descriptions>
    </template>
  </el-drawer>
</template>

<style scoped>
.supplier-stats { margin-bottom: 22px; }
.supplier-panel { padding: 22px; }
.supplier-filters { grid-template-columns: minmax(240px, 1fr) 160px 160px auto auto; margin-bottom: 18px; }
.supplier-list { min-height: 280px; }
.supplier-detail-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; margin-bottom: 18px; }
.supplier-detail-head h2 { margin: 8px 0 4px; }
.supplier-detail-tags { display: flex; gap: 8px; }
.supplier-descriptions { margin-top: 16px; }
@media (max-width: 960px) { .supplier-filters { grid-template-columns: 1fr; }.supplier-detail-head { flex-direction: column; } }
</style>
