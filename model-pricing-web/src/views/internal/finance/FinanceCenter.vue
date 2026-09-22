<script setup lang="ts">
import { formatDateTime } from "../../../domain/date";
import { onMounted, reactive, ref } from "vue";
import { financeApi } from "../../../api/finance";
import type { FinanceAccountDetailDTO, FinanceAccountSummaryDTO, FxRateDTO } from "../../../api/finance.types";
import { ApiError } from "../../../domain/common";
import { FINANCE_ACCOUNT_STATUS, FX_RATE_STATUS, type FinanceAccountStatus, type FxRateStatus } from "../../../domain/status";

const activeTab = ref("fx");
const message = (value: unknown) => value instanceof ApiError ? value.message : value instanceof Error ? value.message : "操作失败，请稍后重试。";
const formatTime = (value?: string | null) => formatDateTime(value, "暂缺", false);
const fxStatus = (status: FxRateStatus) => FX_RATE_STATUS[status];
const accountStatus = (status: FinanceAccountStatus) => FINANCE_ACCOUNT_STATUS[status];
const transactionType = (type: "CREDIT_USAGE" | "CREDIT_RELEASE" | "DEPOSIT_IN" | "DEPOSIT_OUT") => ({ CREDIT_USAGE: "授信占用", CREDIT_RELEASE: "授信释放", DEPOSIT_IN: "押金入账", DEPOSIT_OUT: "押金退回" })[type];

const fxRows = ref<FxRateDTO[]>([]);
const fxLoading = ref(false);
const fxError = ref("");
const fxPagination = reactive({ page: 1, size: 5, total: 0 });
const fxFilters = reactive({ search: "", month: "", status: "" as FxRateStatus | "" });

async function loadFxRates() {
  fxLoading.value = true; fxError.value = "";
  try { const result = await financeApi.listFxRates({ page: fxPagination.page, size: fxPagination.size, search: fxFilters.search.trim() || undefined, month: fxFilters.month || undefined, status: fxFilters.status || undefined }); fxRows.value = result.list; fxPagination.total = result.total; }
  catch (value) { fxRows.value = []; fxPagination.total = 0; fxError.value = message(value); }
  finally { fxLoading.value = false; }
}
function queryFx() { fxPagination.page = 1; void loadFxRates(); }
function resetFx() { Object.assign(fxFilters, { search: "", month: "", status: "" }); queryFx(); }
function changeFxPage(page: number) { fxPagination.page = page; void loadFxRates(); }

const accountRows = ref<FinanceAccountSummaryDTO[]>([]);
const accountLoading = ref(false);
const accountError = ref("");
const accountPagination = reactive({ page: 1, size: 5, total: 0 });
const accountFilters = reactive({ search: "", status: "" as FinanceAccountStatus | "" });
const detailOpen = ref(false);
const detailLoading = ref(false);
const detailError = ref("");
const detail = ref<FinanceAccountDetailDTO>();

async function loadAccounts() {
  accountLoading.value = true; accountError.value = "";
  try { const result = await financeApi.listAccounts({ page: accountPagination.page, size: accountPagination.size, search: accountFilters.search.trim() || undefined, status: accountFilters.status || undefined }); accountRows.value = result.list; accountPagination.total = result.total; }
  catch (value) { accountRows.value = []; accountPagination.total = 0; accountError.value = message(value); }
  finally { accountLoading.value = false; }
}
function queryAccounts() { accountPagination.page = 1; void loadAccounts(); }
function resetAccounts() { Object.assign(accountFilters, { search: "", status: "" }); queryAccounts(); }
function changeAccountPage(page: number) { accountPagination.page = page; void loadAccounts(); }
async function openAccount(row: FinanceAccountSummaryDTO) { detailOpen.value = true; detailLoading.value = true; detailError.value = ""; detail.value = undefined; try { detail.value = await financeApi.getAccount(row.customerId); } catch (value) { detailError.value = message(value); } finally { detailLoading.value = false; } }

onMounted(() => { void Promise.all([loadFxRates(), loadAccounts()]); });
</script>

<template>
  <section class="page-heading"><div><div class="eyebrow">FINANCE CENTER</div><h1>财务信息</h1><p>查询月度汇率快照、客户授信、押金和结算信息。</p></div><el-tag type="info" effect="plain">只读基础版</el-tag></section>
  <el-alert title="汇率锁定、授信调整和押金登记属于写操作，本阶段不开放；页面展示后端已保存结果。" type="info" show-icon :closable="false" />

  <el-tabs v-model="activeTab" class="finance-tabs">
    <el-tab-pane label="汇率快照" name="fx">
      <div class="stats finance-stats"><div><span>汇率记录</span><strong>{{ fxPagination.total }}<small>条</small></strong></div><div><span>本页已锁定</span><strong>{{ fxRows.filter((row) => row.status === 'LOCKED').length }}<small class="green">可用于成本</small></strong></div><div><span>本页待锁定</span><strong>{{ fxRows.filter((row) => row.status === 'PENDING_LOCK').length }}<small>等待财务操作</small></strong></div><div><span>币种对</span><strong>{{ new Set(fxRows.map((row) => `${row.baseCurrency}/${row.quoteCurrency}`)).size }}<small>本页</small></strong></div></div>
      <section class="panel finance-panel"><div class="section-title"><h2>月度汇率 <span>{{ fxPagination.total }}</span></h2><el-button :loading="fxLoading" @click="loadFxRates">刷新</el-button></div><div class="filters fx-filters"><el-input v-model="fxFilters.search" clearable aria-label="汇率搜索" placeholder="搜索币种或来源" @keyup.enter="queryFx" /><el-date-picker v-model="fxFilters.month" type="month" value-format="YYYY-MM" aria-label="汇率月份筛选" placeholder="全部月份" /><el-select v-model="fxFilters.status" clearable aria-label="汇率状态筛选" placeholder="全部状态"><el-option v-for="(config, status) in FX_RATE_STATUS" :key="status" :label="config.label" :value="status" /></el-select><el-button type="primary" @click="queryFx">查询</el-button><el-button text @click="resetFx">重置</el-button></div><el-alert v-if="fxError" :title="fxError" type="error" show-icon :closable="false"><el-button text @click="loadFxRates">重新加载</el-button></el-alert><div v-loading="fxLoading" class="finance-list"><el-empty v-if="!fxRows.length && !fxLoading && !fxError" description="没有符合条件的汇率快照" /><el-table v-else :data="fxRows" row-key="id"><el-table-column prop="month" label="月份" width="105" /><el-table-column label="币种对" min-width="130"><template #default="{ row }"><b class="mono">{{ row.baseCurrency }}/{{ row.quoteCurrency }}</b></template></el-table-column><el-table-column label="锁定汇率" min-width="145"><template #default="{ row }"><span class="mono">{{ row.rate }}</span></template></el-table-column><el-table-column prop="source" label="来源" min-width="165" /><el-table-column label="状态" width="105"><template #default="{ row }"><el-tag :type="fxStatus(row.status).type">{{ fxStatus(row.status).label }}</el-tag></template></el-table-column><el-table-column label="锁定信息" min-width="185"><template #default="{ row }">{{ row.lockedBy || '尚未锁定' }}<div class="muted">{{ formatTime(row.lockedAt || row.updatedAt) }}</div></template></el-table-column></el-table></div><div class="catalog-pagination"><el-pagination :current-page="fxPagination.page" :page-size="fxPagination.size" :total="fxPagination.total" layout="total, prev, pager, next" @current-change="changeFxPage" /></div></section>
    </el-tab-pane>

    <el-tab-pane label="授信与押金" name="accounts">
      <div class="stats finance-stats"><div><span>客户账户</span><strong>{{ accountPagination.total }}<small>个</small></strong></div><div><span>本页正常</span><strong>{{ accountRows.filter((row) => row.status === 'NORMAL').length }}<small class="green">正常结算</small></strong></div><div><span>本页需关注</span><strong>{{ accountRows.filter((row) => row.status === 'WARNING').length }}<small>授信预警</small></strong></div><div><span>本页冻结</span><strong>{{ accountRows.filter((row) => row.status === 'FROZEN').length }}<small>服务端状态</small></strong></div></div>
      <section class="panel finance-panel"><div class="section-title"><h2>客户财务账户 <span>{{ accountPagination.total }}</span></h2><el-button :loading="accountLoading" @click="loadAccounts">刷新</el-button></div><div class="filters account-filters"><el-input v-model="accountFilters.search" clearable aria-label="财务账户搜索" placeholder="搜索客户名称或编码" @keyup.enter="queryAccounts" /><el-select v-model="accountFilters.status" clearable aria-label="财务账户状态筛选" placeholder="全部状态"><el-option v-for="(config, status) in FINANCE_ACCOUNT_STATUS" :key="status" :label="config.label" :value="status" /></el-select><el-button type="primary" @click="queryAccounts">查询</el-button><el-button text @click="resetAccounts">重置</el-button></div><el-alert v-if="accountError" :title="accountError" type="error" show-icon :closable="false"><el-button text @click="loadAccounts">重新加载</el-button></el-alert><div v-loading="accountLoading" class="finance-list"><el-empty v-if="!accountRows.length && !accountLoading && !accountError" description="没有符合条件的客户财务账户" /><el-table v-else :data="accountRows" row-key="customerId"><el-table-column label="客户" min-width="185"><template #default="{ row }"><b>{{ row.customerName }}</b><div class="mono muted">{{ row.customerCode }} · {{ row.levelCode }}</div></template></el-table-column><el-table-column label="授信额度 / 已用" min-width="185"><template #default="{ row }"><span class="mono">{{ row.creditLimit }} / {{ row.usedCredit }}</span><div class="muted">可用 {{ row.availableCredit }} {{ row.currency }}</div></template></el-table-column><el-table-column label="押金余额" min-width="135"><template #default="{ row }"><span class="mono">{{ row.depositBalance }}</span> {{ row.currency }}</template></el-table-column><el-table-column prop="paymentTerms" label="结算条件" min-width="130" /><el-table-column label="状态" width="105"><template #default="{ row }"><el-tag :type="accountStatus(row.status).type">{{ accountStatus(row.status).label }}</el-tag></template></el-table-column><el-table-column label="操作" width="95"><template #default="{ row }"><el-button link type="primary" @click="openAccount(row)">账户详情</el-button></template></el-table-column></el-table></div><div class="catalog-pagination"><el-pagination :current-page="accountPagination.page" :page-size="accountPagination.size" :total="accountPagination.total" layout="total, prev, pager, next" @current-change="changeAccountPage" /></div></section>
    </el-tab-pane>
  </el-tabs>

  <el-drawer v-model="detailOpen" v-loading="detailLoading" title="客户财务账户" size="min(820px, 96vw)"><el-alert v-if="detailError" :title="detailError" type="error" show-icon :closable="false" /><template v-if="detail"><div class="finance-detail-head"><div><div class="eyebrow">{{ detail.customerCode }}</div><h2>{{ detail.customerName }}</h2><p class="muted">{{ detail.billingEntity }}</p></div><el-tag :type="accountStatus(detail.status).type">{{ accountStatus(detail.status).label }}</el-tag></div><el-alert v-if="detail.status === 'WARNING'" :title="`授信占用已达到服务端预警条件（阈值 ${detail.warningThresholdRate}%）。`" type="warning" show-icon :closable="false" /><el-alert v-if="detail.status === 'FROZEN'" title="账户已由服务端冻结，页面不能解除或修改额度。" type="error" show-icon :closable="false" /><div class="finance-balance-grid"><div><span>授信额度</span><strong class="mono">{{ detail.creditLimit }}</strong><small>{{ detail.currency }}</small></div><div><span>已用授信</span><strong class="mono">{{ detail.usedCredit }}</strong><small>{{ detail.currency }}</small></div><div><span>可用授信</span><strong class="mono">{{ detail.availableCredit }}</strong><small>{{ detail.currency }}</small></div><div><span>押金余额</span><strong class="mono">{{ detail.depositBalance }}</strong><small>{{ detail.currency }}</small></div></div><el-descriptions :column="2" border><el-descriptions-item label="开票名称">{{ detail.invoiceTitle }}</el-descriptions-item><el-descriptions-item label="税号">{{ detail.taxNoMasked }}</el-descriptions-item><el-descriptions-item label="结算条件">{{ detail.paymentTerms }}</el-descriptions-item><el-descriptions-item label="结算周期">{{ detail.settlementCycle }}</el-descriptions-item></el-descriptions><h3>最近变动</h3><el-empty v-if="!detail.recentTransactions.length" description="暂无近期财务变动" :image-size="64" /><el-table v-else :data="detail.recentTransactions" border><el-table-column label="时间" min-width="155"><template #default="{ row }">{{ formatTime(row.occurredAt) }}</template></el-table-column><el-table-column label="类型" width="105"><template #default="{ row }">{{ transactionType(row.type) }}</template></el-table-column><el-table-column label="金额" min-width="135"><template #default="{ row }"><span class="mono">{{ row.amount }}</span> {{ row.currency }}</template></el-table-column><el-table-column prop="referenceNo" label="关联单号" min-width="155" /><el-table-column prop="note" label="说明" min-width="160" /></el-table><h3 v-if="detail.notes.length">业务提示</h3><ul v-if="detail.notes.length" class="finance-notes"><li v-for="note in detail.notes" :key="note">{{ note }}</li></ul></template></el-drawer>
</template>

<style scoped>
.finance-tabs { margin-top: 18px; }.finance-stats { margin: 18px 0 22px; }.finance-panel { padding: 22px; }.fx-filters { grid-template-columns: minmax(220px, 1fr) 160px 150px auto auto; margin-bottom: 18px; }.account-filters { grid-template-columns: minmax(260px, 1fr) 170px auto auto; margin-bottom: 18px; }.finance-list { min-height: 280px; }.finance-detail-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; margin-bottom: 18px; }.finance-detail-head h2 { margin: 8px 0 4px; }.finance-balance-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; margin: 18px 0; }.finance-balance-grid > div { padding: 15px; border: 1px solid var(--app-border); border-radius: 9px; background: var(--app-surface-muted); }.finance-balance-grid span,.finance-balance-grid small { display: block; color: var(--app-text-muted); }.finance-balance-grid strong { display: block; margin: 8px 0; font-size: 18px; }.finance-notes { padding: 14px 18px 14px 34px; border-radius: 8px; background: var(--app-surface-muted); line-height: 1.9; }@media (max-width: 980px) { .fx-filters,.account-filters,.finance-balance-grid { grid-template-columns: 1fr; }.finance-detail-head { flex-direction: column; } }
</style>
