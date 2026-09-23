<script setup lang="ts">
import { formatDateTime } from "../../../domain/date";
import { computed, onMounted, reactive, ref } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { officialPricesApi } from "../../../api/officialPrices";
import { changeRequestsApi } from "../../../api/changeRequests";
import type { ChangeRequestDetailDTO, ChangeRequestItemDTO, ChangeRequestType } from "../../../api/changeRequests.types";
import { modelsApi } from "../../../api/models";
import type { ModelSkuContractDTO } from "../../../api/models.types";
import type {
  StagingItemInput,
  StagingPriceDTO,
  SyncJobDTO,
  SyncJobType,
} from "../../../api/officialPrices.types";
import { QUOTE_COMPONENTS } from "../../../api/quoteContract.types";
import type { QuoteComponentType } from "../../../api/quoteContract.types";
import { usePermissionStore } from "../../../stores/permission";
import { PERMISSIONS } from "../../../domain/permissions";
import { ApiError } from "../../../domain/common";
import { isDecimalAmount } from "../../../domain/money";
import {
  SYNC_JOB_STATUS,
  STAGING_DIFF_STATUS,
  type SyncJobStatus,
  type StagingDiffStatus,
} from "../../../domain/status";

type TabName = "jobs" | "staging" | "confirmed";
const permission = usePermissionStore();
const canEdit = computed(() => permission.canAction(PERMISSIONS.PRICE_SYNC_EDIT));
const canReadSkus = computed(() => permission.canAction(PERMISSIONS.MODEL_VIEW));
const skuOptions = ref<ModelSkuContractDTO[]>([]);
const selectedSkuOptions = reactive(new Map<string, ModelSkuContractDTO>());
const skuKeyword = ref(""), skuLoading = ref(false), skuError = ref(""), skuPage = ref(1), skuTotal = ref(0);
let skuSequence = 0;
async function searchSkus(keyword = "", append = false) {
  if (!canReadSkus.value) return;
  const sequence = ++skuSequence;
  skuLoading.value = true; skuError.value = "";
  const page = append ? skuPage.value + 1 : 1;
  try {
    const result = await modelsApi.list({ view: "sku", keyword: keyword.trim() || undefined, page, size: 20 });
    if (sequence !== skuSequence) return;
    const list = result.list.filter((row): row is ModelSkuContractDTO => "sku_code" in row);
    skuOptions.value = append ? [...skuOptions.value, ...list] : list;
    skuPage.value = page; skuTotal.value = result.total;
  } catch (error) { if (sequence === skuSequence) skuError.value = errorMessage(error); }
  finally { if (sequence === skuSequence) skuLoading.value = false; }
}
function skuLabel(id: string) {
  const sku = selectedSkuOptions.get(id) || skuOptions.value.find(row => String(row.id) === id);
  return sku ? `${sku.vendor_name} / ${sku.family_name} · ${sku.sku_code} · ID ${id}` : `SKU ID ${id}`;
}
function rememberSku(id: string) {
  const sku = skuOptions.value.find(row => String(row.id) === id);
  if (sku) selectedSkuOptions.set(id, sku);
}

const tab = ref<TabName>("jobs");
const loading = ref(false);
const error = ref("");
const submitting = ref(false);

const jobs = ref<SyncJobDTO[]>([]);
const jobTotal = ref(0);
const jobPage = reactive({ page: 1, size: 10 });
const staging = ref<StagingPriceDTO[]>([]);
const stagingTotal = ref(0);
const stagingPage = reactive({ page: 1, size: 10 });
let jobsSequence = 0;
let stagingSequence = 0;
let changeSequence = 0;
let loadSequence = 0;
const stagingJobFilter = ref<number | undefined>(undefined);
const selectedStaging = ref<StagingPriceDTO[]>([]);
const changeRows = ref<ChangeRequestItemDTO[]>([]);
const changeTotal = ref(0);
const changePage = reactive({ page: 1, size: 10 });
const changeType = ref<Extract<ChangeRequestType, "PRICE_UP" | "PRICE_DOWN">>("PRICE_UP");
const changeDetail = ref<ChangeRequestDetailDTO>();
const changeDetailOpen = ref(false);
const changeDetailLoading = ref(false);

const errorMessage = (value: unknown) =>
  value instanceof ApiError ? value.message : value instanceof Error ? value.message : "官方价格数据加载失败，请稍后重试。";
const formatTime = (value?: string | null) => formatDateTime(value, "暂缺", true);
const jobStatus = (status: SyncJobStatus) => SYNC_JOB_STATUS[status] ?? { label: status, type: "info" as const };
const diffStatus = (status: StagingDiffStatus) => STAGING_DIFF_STATUS[status] ?? { label: status, type: "info" as const };
const confirmableStaging = computed(() =>
  selectedStaging.value.filter((row) => !row.processed && row.sku_id !== null && row.diff_status !== "UNMATCHED"),
);

async function loadJobs() {
  const sequence = ++jobsSequence;
  const result = await officialPricesApi.listJobs({ page: jobPage.page, size: jobPage.size });
  if (sequence !== jobsSequence) return;
  jobs.value = result.list;
  jobTotal.value = result.total;
}
async function loadStaging() {
  const sequence = ++stagingSequence;
  const result = await officialPricesApi.listStaging({ page: stagingPage.page, size: stagingPage.size, sync_job_id: stagingJobFilter.value });
  if (sequence !== stagingSequence) return;
  staging.value = result.list;
  stagingTotal.value = result.total;
}
async function loadChanges() {
  const sequence = ++changeSequence;
  const result = await changeRequestsApi.list({ type: changeType.value, page: changePage.page, size: changePage.size });
  if (sequence !== changeSequence) return;
  changeRows.value = result.list; changeTotal.value = result.total;
}
async function openChangeDetail(id: string) {
  changeDetailOpen.value = true; changeDetailLoading.value = true; error.value = "";
  try { changeDetail.value = await changeRequestsApi.get(id); }
  catch (value) { changeDetail.value = undefined; error.value = errorMessage(value); }
  finally { changeDetailLoading.value = false; }
}
async function load() {
  const sequence = ++loadSequence;
  const target = tab.value;
  loading.value = true;
  error.value = "";
  try {
    if (target === "jobs") await loadJobs();
    else if (target === "staging") await loadStaging();
    else await loadChanges();
  } catch (value) {
    if (sequence !== loadSequence) return;
    error.value = errorMessage(value);
    if (target === "jobs") { jobs.value = []; jobTotal.value = 0; }
    else if (target === "staging") { staging.value = []; stagingTotal.value = 0; }
    else { changeRows.value = []; changeTotal.value = 0; }
  } finally {
    if (sequence === loadSequence) loading.value = false;
  }
}
function switchTab() { error.value = ""; void load(); }
function changeJobPage(page: number) { jobPage.page = page; void loadWrap(loadJobs); }
function changeStagingPage(page: number) { stagingPage.page = page; void loadWrap(loadStaging); }
function changeRequestPage(page: number) { changePage.page = page; void loadWrap(loadChanges); }
function queryChanges() { changePage.page = 1; void loadWrap(loadChanges); }
async function loadWrap(fn: () => Promise<void>) {
  const sequence = ++loadSequence;
  loading.value = true; error.value = "";
  try { await fn(); } catch (value) { if (sequence === loadSequence) error.value = errorMessage(value); }
  finally { if (sequence === loadSequence) loading.value = false; }
}

// —— 新建采集批次 ——
const jobOpen = ref(false);
const jobForm = reactive<{ job_type: SyncJobType; source: string; sku_ids: string; selectedIds: string[] }>({ job_type: "SYNC_PRICES", source: "", sku_ids: "", selectedIds: [] });
const createdJob = ref<{ id: string; skus: string[] }>();
const jobError = ref("");
function openJobForm() {
  Object.assign(jobForm, { job_type: "SYNC_PRICES", source: "", sku_ids: "", selectedIds: [] });
  jobError.value = ""; jobOpen.value = true;
}
async function submitJob() {
  if (submitting.value) return;
  jobError.value = "";
  const source = jobForm.source.trim();
  if (!source || source.length > 32) { jobError.value = "请填写来源标识（1~32 字符）。"; return; }
  const skuIds: number[] = [];
  for (const part of (canReadSkus.value ? jobForm.selectedIds : jobForm.sku_ids.split(/[,，\s]+/).filter(Boolean))) {
    const id = Number(part);
    if (!Number.isSafeInteger(id) || id <= 0) { jobError.value = `SKU ID「${part}」不是正整数。`; return; }
    skuIds.push(id);
  }
  submitting.value = true;
  try {
    const result = await officialPricesApi.createJob({ job_type: jobForm.job_type, source, sku_ids: skuIds.length ? skuIds : undefined });
    createdJob.value = { id: String(result.id), skus: skuIds.map(id => skuLabel(String(id))) };
    stagingForm.sync_job_id = String(result.id);
    jobOpen.value = false;
    ElMessage.success("采集批次已创建，可在暂存区录入采集结果。");
    jobPage.page = 1; await loadWrap(loadJobs);
  } catch (value) { jobError.value = errorMessage(value); }
  finally { submitting.value = false; }
}

// —— 录入暂存价格 ——
interface StagingRowForm { sku_id: string; raw_sku_code: string; currency: string; components: Array<{ component_type: QuoteComponentType; unit_price: string }>; }
const stagingOpen = ref(false);
const stagingForm = reactive<{ sync_job_id: string; rows: StagingRowForm[] }>({ sync_job_id: "", rows: [] });
const stagingError = ref("");
const stagingSnapshot = ref<string[]>([]);
function blankStagingRow(): StagingRowForm { return { sku_id: "", raw_sku_code: "", currency: "USD", components: [{ component_type: "input", unit_price: "" }] }; }
function openStagingForm() {
  stagingForm.sync_job_id = stagingJobFilter.value ? String(stagingJobFilter.value) : createdJob.value?.id || "";
  stagingForm.rows = [blankStagingRow()];
  stagingError.value = ""; stagingOpen.value = true;
}
function addStagingRow() { stagingForm.rows.push(blankStagingRow()); }
function removeStagingRow(index: number) { stagingForm.rows.splice(index, 1); }
function addComponent(row: StagingRowForm) { row.components.push({ component_type: "output", unit_price: "" }); }
function removeComponent(row: StagingRowForm, index: number) { row.components.splice(index, 1); }
async function submitStaging() {
  if (submitting.value) return;
  stagingError.value = "";
  const jobId = Number(stagingForm.sync_job_id);
  if (!Number.isSafeInteger(jobId) || jobId <= 0) { stagingError.value = "请填写有效的采集批次 ID。"; return; }
  if (!stagingForm.rows.length) { stagingError.value = "请至少录入一行。"; return; }
  const items: StagingItemInput[] = [];
  for (const [i, row] of stagingForm.rows.entries()) {
    const skuId = row.sku_id.trim();
    const rawCode = row.raw_sku_code.trim();
    if (!skuId && !rawCode) { stagingError.value = `第 ${i + 1} 行：SKU ID 与 SKU 编码至少填一个。`; return; }
    const currency = row.currency.trim().toUpperCase();
    if (currency.length !== 3) { stagingError.value = `第 ${i + 1} 行：币种必须是 3 位币种码。`; return; }
    const payload: Partial<Record<QuoteComponentType, string>> = {};
    if (!row.components.length) { stagingError.value = `第 ${i + 1} 行：至少填写一个组件价格。`; return; }
    for (const c of row.components) {
      if (!isDecimalAmount(c.unit_price, true)) { stagingError.value = `第 ${i + 1} 行「${c.component_type}」价格须为非负数（最多 8 位小数）。`; return; }
      if (payload[c.component_type] !== undefined) { stagingError.value = `第 ${i + 1} 行组件「${c.component_type}」重复。`; return; }
      payload[c.component_type] = c.unit_price.trim();
    }
    const item: StagingItemInput = { currency, payload };
    if (skuId) {
      const id = Number(skuId);
      if (!Number.isSafeInteger(id) || id <= 0) { stagingError.value = `第 ${i + 1} 行：SKU ID 不是正整数。`; return; }
      item.sku_id = id;
    } else {
      item.raw_sku_code = rawCode;
    }
    items.push(item);
  }
  submitting.value = true;
  try {
    const result = await officialPricesApi.createStaging({ sync_job_id: jobId, items });
    stagingSnapshot.value = stagingForm.rows.map(row => row.sku_id ? skuLabel(row.sku_id) : `SKU code ${row.raw_sku_code}`);
    stagingOpen.value = false;
    ElMessage.success(`已录入 ${result.created_count} 条暂存价格。`);
    tab.value = "staging"; stagingJobFilter.value = jobId; stagingPage.page = 1;
    await loadWrap(loadStaging);
  } catch (value) { stagingError.value = errorMessage(value); }
  finally { submitting.value = false; }
}

// —— 确认建单 ——
const confirmOpen = ref(false);
const confirmEffectiveTime = ref("");
const confirmError = ref("");
function openConfirm() {
  if (!confirmableStaging.value.length) { ElMessage.warning("请先勾选未确认且已匹配 SKU 的暂存行。"); return; }
  confirmEffectiveTime.value = "";
  confirmError.value = ""; confirmOpen.value = true;
}
async function submitConfirm() {
  if (submitting.value) return;
  confirmError.value = "";
  const rows = confirmableStaging.value;
  const jobIds = new Set(rows.map((row) => String(row.sync_job_id)));
  if (jobIds.size !== 1) { confirmError.value = "只能对同一采集批次的暂存行一起确认。"; return; }
  if (!confirmEffectiveTime.value) { confirmError.value = "请选择生效时间（不得晚于当前时间）。"; return; }
  if (!Number.isFinite(Date.parse(confirmEffectiveTime.value)) || Date.parse(confirmEffectiveTime.value) > Date.now()) { confirmError.value = "请选择有效且不晚于当前时间的生效时间。"; return; }
  const syncJobId = Number(rows[0].sync_job_id);
  const stagingIds = rows.map((row) => Number(row.id));
  const effectiveTime = new Date(confirmEffectiveTime.value).toISOString();
  submitting.value = true;
  try {
    await ElMessageBox.confirm(`确认对 ${rows.length} 条暂存价格建单并进入审批？涨价将走两级审批，降价一级。`, "确认建单", { confirmButtonText: "确认建单", cancelButtonText: "取消" });
    const result = await officialPricesApi.confirmStaging({ sync_job_id: syncJobId, staging_ids: stagingIds, effective_time: effectiveTime });
    confirmOpen.value = false; selectedStaging.value = [];
    ElMessage.success(`已生成变更单 #${result.change_request_id}（${result.step_count} 级审批）。`);
    await loadWrap(loadStaging);
  } catch (value) { if (value !== "cancel" && value !== "close") confirmError.value = errorMessage(value); }
  finally { submitting.value = false; }
}

onMounted(load);
</script>

<template>
  <section class="page-heading">
    <div><div class="eyebrow">OFFICIAL PRICE SYNC</div><h1>官方价格同步</h1><p>创建采集批次、录入采集结果并核对差异，勾选暂存价格确认建单进入审批。</p></div>
    <el-button :loading="loading" @click="load">刷新数据</el-button>
  </section>
  <el-alert type="info" :closable="false" title="本页对接后端采集批次 / 暂存区 / 确认建单接口。确认后返回变更单号，审批与激活由后端负责；变更单查询接口暂未提供，本页仅回显本次建单结果。" />

  <section class="panel sync-panel">
    <el-tabs v-model="tab" @tab-change="switchTab">
      <el-tab-pane label="采集批次" name="jobs" />
      <el-tab-pane label="暂存区 · 差异核对" name="staging" />
      <el-tab-pane label="变更单进度" name="confirmed" />
    </el-tabs>

    <el-alert v-if="error" :title="error" type="error" show-icon :closable="false"><el-button text @click="load">重新加载</el-button></el-alert>

    <!-- 采集批次 -->
    <template v-if="tab === 'jobs'">
      <div class="sync-toolbar"><el-button v-if="canEdit" type="primary" @click="openJobForm">新建采集批次</el-button></div>
      <el-alert v-if="createdJob" type="success" :closable="false" :title="`采集批次 ID：${createdJob.id}`" :description="`本次选择：${createdJob.skus.join('；') || '未指定 SKU'}。可直接用此 ID 录入暂存价格。`" />
      <div v-loading="loading" class="sync-list">
        <el-empty v-if="!jobs.length && !loading && !error" description="暂无采集批次" />
        <el-table v-else :data="jobs" row-key="id">
          <el-table-column label="批次" min-width="180"><template #default="{ row }"><b>{{ row.source }}</b><div class="mono muted">#{{ row.id }} · {{ row.job_type }}</div></template></el-table-column>
          <el-table-column label="状态" width="110"><template #default="{ row }"><el-tag :type="jobStatus(row.status).type">{{ jobStatus(row.status).label }}</el-tag></template></el-table-column>
          <el-table-column label="录入条数" width="110"><template #default="{ row }">{{ row.item_count ?? '—' }}</template></el-table-column>
          <el-table-column label="创建时间" min-width="180"><template #default="{ row }">{{ formatTime(row.started_at) }}</template></el-table-column>
          <el-table-column label="错误" min-width="140"><template #default="{ row }">{{ row.error_msg || '—' }}</template></el-table-column>
        </el-table>
      </div>
      <div class="catalog-pagination"><el-pagination :current-page="jobPage.page" :page-size="jobPage.size" :total="jobTotal" layout="total, prev, pager, next" @current-change="changeJobPage" /></div>
    </template>

    <!-- 暂存区 -->
    <template v-else-if="tab === 'staging'">
      <div class="sync-toolbar">
        <el-input v-model.number="stagingJobFilter" clearable placeholder="按采集批次 ID 过滤" style="width: 200px" @clear="() => { stagingPage.page = 1; loadWrap(loadStaging); }" @keyup.enter="() => { stagingPage.page = 1; loadWrap(loadStaging); }" />
        <el-button @click="() => { stagingPage.page = 1; loadWrap(loadStaging); }">查询</el-button>
        <el-button v-if="canEdit" type="primary" @click="openStagingForm">录入暂存价格</el-button>
        <el-button v-if="canEdit" :disabled="!confirmableStaging.length" @click="openConfirm">确认建单（已选 {{ confirmableStaging.length }}）</el-button>
      </div>
      <el-alert v-if="stagingSnapshot.length" type="success" :closable="false" :title="`本次暂存录入：${stagingSnapshot.join('；')}`" />
      <div v-loading="loading" class="sync-list">
        <el-empty v-if="!staging.length && !loading && !error" description="暂无暂存价格" />
        <el-table v-else :data="staging" row-key="id" @selection-change="(rows: StagingPriceDTO[]) => (selectedStaging = rows)">
          <el-table-column type="selection" width="45" :selectable="(row: StagingPriceDTO) => canEdit && !row.processed && row.sku_id !== null && row.diff_status !== 'UNMATCHED'" />
          <el-table-column label="SKU" min-width="160"><template #default="{ row }"><span class="mono">{{ row.sku_id ?? row.raw_sku_code ?? '未匹配' }}</span><div class="mono muted">{{ row.currency }} · #{{ row.id }}</div></template></el-table-column>
          <el-table-column label="差异" width="105"><template #default="{ row }"><el-tag :type="diffStatus(row.diff_status).type">{{ diffStatus(row.diff_status).label }}</el-tag></template></el-table-column>
          <el-table-column label="录入价格 / 差异明细" min-width="280"><template #default="{ row }">
            <div v-if="row.diff_detail.length" class="diff-lines">
              <div v-for="d in row.diff_detail" :key="d.component_type" class="mono">
                {{ d.component_type }}：<span v-if="d.old_price">{{ d.old_price }} → </span>{{ d.new_price }}<span v-if="d.delta_pct" class="muted"> （{{ (Number(d.delta_pct) * 100).toFixed(2) }}%）</span>
              </div>
            </div>
            <div v-else class="mono muted">{{ Object.entries(row.payload).map(([k, v]) => `${k}:${v}`).join(' · ') || '—' }}</div>
          </template></el-table-column>
          <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="row.processed ? 'success' : 'info'" size="small">{{ row.processed ? '已确认' : '待确认' }}</el-tag></template></el-table-column>
          <el-table-column label="录入时间" min-width="170"><template #default="{ row }">{{ formatTime(row.created_at) }}</template></el-table-column>
        </el-table>
      </div>
      <div class="catalog-pagination"><el-pagination :current-page="stagingPage.page" :page-size="stagingPage.size" :total="stagingTotal" layout="total, prev, pager, next" @current-change="changeStagingPage" /></div>
    </template>

    <!-- 服务端变更单进度 -->
    <template v-else>
      <div class="sync-toolbar"><el-select v-model="changeType" style="width: 180px" @change="queryChanges"><el-option label="官方涨价" value="PRICE_UP" /><el-option label="官方降价" value="PRICE_DOWN" /></el-select><el-button :loading="loading" @click="queryChanges">查询</el-button></div>
      <el-empty v-if="!changeRows.length && !loading" description="暂无对应变更单" />
      <el-table v-else :data="changeRows" row-key="id">
        <el-table-column label="变更单号" width="120"><template #default="{ row }"><span class="mono">#{{ row.id }}</span></template></el-table-column>
        <el-table-column label="SKU" width="110"><template #default="{ row }">{{ row.skuId || '—' }}</template></el-table-column>
        <el-table-column label="状态" width="110" prop="status" />
        <el-table-column label="审批进度" min-width="210"><template #default="{ row }">{{ row.stepsApproved }} / {{ row.stepsTotal }}<span v-if="row.pendingRole" class="muted"> · 等待 {{ row.pendingRole }}</span></template></el-table-column>
        <el-table-column label="更新时间" min-width="180"><template #default="{ row }">{{ formatTime(row.updatedAt) }}</template></el-table-column>
        <el-table-column label="操作" width="90"><template #default="{ row }"><el-button link type="primary" @click="openChangeDetail(row.id)">详情</el-button></template></el-table-column>
      </el-table>
      <div class="catalog-pagination"><el-pagination :current-page="changePage.page" :page-size="changePage.size" :total="changeTotal" layout="total, prev, pager, next" @current-change="changeRequestPage" /></div>
    </template>
  </section>

  <el-drawer v-model="changeDetailOpen" title="变更单详情" size="min(720px, 96vw)" v-loading="changeDetailLoading">
    <template v-if="changeDetail"><el-descriptions :column="1" border><el-descriptions-item label="变更单">#{{ changeDetail.id }}</el-descriptions-item><el-descriptions-item label="类型 / 状态">{{ changeDetail.changeType }} / {{ changeDetail.status }}</el-descriptions-item><el-descriptions-item label="SKU">{{ changeDetail.skuId || '—' }}</el-descriptions-item><el-descriptions-item label="审批进度">{{ changeDetail.stepsApproved }} / {{ changeDetail.stepsTotal }}<span v-if="changeDetail.pendingRole"> · 等待 {{ changeDetail.pendingRole }}</span></el-descriptions-item></el-descriptions><h3>审批步骤</h3><el-timeline><el-timeline-item v-for="step in changeDetail.steps" :key="step.stepNo" :timestamp="formatTime(step.decidedAt)">第 {{ step.stepNo }} 步 · {{ step.requiredRole }} · {{ step.decision || '待审批' }}<div v-if="step.comment" class="muted">{{ step.comment }}</div></el-timeline-item></el-timeline><h3>建单内容</h3><pre class="payload-preview">{{ JSON.stringify(changeDetail.payload ?? {}, null, 2) }}</pre></template>
  </el-drawer>

  <!-- 新建采集批次 -->
  <el-dialog v-model="jobOpen" title="新建采集批次" width="min(560px, 96vw)" :close-on-click-modal="false" :close-on-press-escape="!submitting" :show-close="!submitting">
    <el-alert type="info" :closable="false" title="人工录入模式：批次创建后即为 SUCCESS，仅作为一批录入的容器。SKU ID 选填，仅留存计数。" />
    <el-form label-position="top" :disabled="submitting">
      <el-form-item label="采集类型"><el-select v-model="jobForm.job_type"><el-option label="价格采集（SYNC_PRICES）" value="SYNC_PRICES" /><el-option label="模型采集（SYNC_MODELS）" value="SYNC_MODELS" /><el-option label="社区采集（SYNC_COMMUNITY）" value="SYNC_COMMUNITY" /></el-select></el-form-item>
      <el-form-item label="来源标识"><el-input v-model="jobForm.source" maxlength="32" show-word-limit placeholder="如 manual / openai-web" /></el-form-item>
      <el-form-item v-if="canReadSkus" label="SKU（选填）"><el-select v-model="jobForm.selectedIds" multiple filterable remote :remote-method="(value: string) => { skuKeyword = value; searchSkus(value); }" :loading="skuLoading" @visible-change="(open: boolean) => { if (open && !skuOptions.length) searchSkus(); }" @change="(ids: string[]) => ids.forEach(rememberSku)"><el-option v-for="sku in [...skuOptions, ...[...selectedSkuOptions.values()].filter(row => !skuOptions.some(option => String(option.id) === String(row.id)))]" :key="String(sku.id)" :label="skuLabel(String(sku.id))" :value="String(sku.id)" /></el-select></el-form-item>
      <el-form-item v-else label="SKU ID（无模型查看权限，选填）"><el-input v-model="jobForm.sku_ids" placeholder="如 40, 41" /></el-form-item>
      <p v-if="jobForm.selectedIds.length" class="muted">本次选择：{{ jobForm.selectedIds.map(skuLabel).join('；') }}</p>
      <el-alert v-if="skuError" :title="skuError" type="error" :closable="false"><el-button @click="searchSkus(skuKeyword)">重试</el-button></el-alert>
      <el-button v-if="canReadSkus && skuOptions.length < skuTotal" text :loading="skuLoading" @click="searchSkus(skuKeyword, true)">加载更多 SKU</el-button>
    </el-form>
    <el-alert v-if="jobError" :title="jobError" type="error" :closable="false" />
    <template #footer><el-button :disabled="submitting" @click="jobOpen = false">取消</el-button><el-button type="primary" :loading="submitting" @click="submitJob">创建批次</el-button></template>
  </el-dialog>

  <!-- 录入暂存价格 -->
  <el-dialog v-model="stagingOpen" title="录入暂存价格" width="min(880px, 96vw)" :close-on-click-modal="false" :close-on-press-escape="!submitting" :show-close="!submitting">
    <el-alert type="info" :closable="false" title="每行按 SKU 录入组件价格；SKU ID 与 SKU 编码二选一（都填以 ID 为准）。价格为非负十进制字符串，差异由后端实时比对。" />
    <el-form label-position="top" :disabled="submitting">
      <el-form-item label="采集批次 ID"><el-input v-model="stagingForm.sync_job_id" placeholder="填写已创建的采集批次 ID" style="width: 240px" /></el-form-item>
      <article v-for="(row, index) in stagingForm.rows" :key="index" class="staging-row">
        <div class="staging-row-head">
          <b>第 {{ index + 1 }} 行</b>
          <el-button v-if="stagingForm.rows.length > 1" text type="danger" @click="removeStagingRow(index)">删除行</el-button>
        </div>
        <div class="staging-row-grid">
          <el-select v-if="canReadSkus" v-model="row.sku_id" filterable remote clearable :remote-method="(value: string) => { skuKeyword = value; searchSkus(value); }" :loading="skuLoading" placeholder="选择 SKU ID" @visible-change="(open: boolean) => { if (open && !skuOptions.length) searchSkus(); }" @change="rememberSku"><el-option v-for="sku in [...skuOptions, ...selectedSkuOptions.values()]" :key="String(sku.id)" :label="skuLabel(String(sku.id))" :value="String(sku.id)" /></el-select>
          <el-input v-else v-model="row.sku_id" placeholder="SKU ID" />
          <el-input v-model="row.raw_sku_code" placeholder="或 SKU 编码" />
          <el-input v-model="row.currency" placeholder="币种（如 USD）" maxlength="3" />
        </div>
        <p v-if="row.sku_id" class="muted">已选：{{ skuLabel(row.sku_id) }}</p>
        <div v-for="(component, ci) in row.components" :key="ci" class="staging-component">
          <el-select v-model="component.component_type" style="width: 180px"><el-option v-for="ct in QUOTE_COMPONENTS" :key="ct" :label="ct" :value="ct" /></el-select>
          <el-input v-model="component.unit_price" placeholder="单价（非负，最多 8 位小数）" />
          <el-button v-if="row.components.length > 1" text type="danger" @click="removeComponent(row, ci)">移除</el-button>
        </div>
        <el-button text type="primary" @click="addComponent(row)">＋ 组件</el-button>
      </article>
      <el-button @click="addStagingRow">＋ 增加一行</el-button>
      <el-alert v-if="skuError" :title="skuError" type="error" :closable="false"><el-button @click="searchSkus(skuKeyword)">重试</el-button></el-alert>
      <el-button v-if="canReadSkus && skuOptions.length < skuTotal" text :loading="skuLoading" @click="searchSkus(skuKeyword, true)">加载更多 SKU</el-button>
      <p class="muted">本次录入：{{ stagingForm.rows.map(row => row.sku_id ? skuLabel(row.sku_id) : row.raw_sku_code || '未选择').join('；') }}</p>
    </el-form>
    <el-alert v-if="stagingError" :title="stagingError" type="error" :closable="false" />
    <template #footer><el-button :disabled="submitting" @click="stagingOpen = false">取消</el-button><el-button type="primary" :loading="submitting" @click="submitStaging">录入暂存</el-button></template>
  </el-dialog>

  <!-- 确认建单 -->
  <el-dialog v-model="confirmOpen" title="确认建单" width="min(520px, 96vw)" :close-on-click-modal="false" :close-on-press-escape="!submitting" :show-close="!submitting">
    <el-alert type="info" :closable="false" title="将对已勾选的暂存价格生成官方价变更单，进入审批。生效时间不得晚于当前时间。" />
    <p class="muted">本次暂存 SKU：{{ confirmableStaging.map(row => row.sku_id ? skuLabel(String(row.sku_id)) : row.raw_sku_code || '未匹配').join('；') }}</p>
    <el-form label-position="top" :disabled="submitting">
      <el-form-item label="生效时间"><el-date-picker v-model="confirmEffectiveTime" type="datetime" value-format="YYYY-MM-DDTHH:mm:ss" placeholder="选择生效时间（≤ 当前）" style="width: 100%" /></el-form-item>
    </el-form>
    <el-alert v-if="confirmError" :title="confirmError" type="error" :closable="false" />
    <template #footer><el-button :disabled="submitting" @click="confirmOpen = false">取消</el-button><el-button type="primary" :loading="submitting" @click="submitConfirm">确认建单</el-button></template>
  </el-dialog>
</template>

<style scoped>
.sync-panel { padding: 22px; }
.sync-toolbar { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; margin: 4px 0 18px; }
.sync-list { min-height: 290px; }
.diff-lines { line-height: 1.7; }
.staging-row { margin: 16px 0; padding: 14px 16px; border: 1px solid var(--app-border); border-radius: 10px; }
.staging-row-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px; }
.staging-row-grid { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 10px; margin-bottom: 10px; }
.staging-component { display: flex; gap: 10px; align-items: center; margin-bottom: 8px; }
.staging-component > .el-input { flex: 1; }
@media (max-width: 900px) { .staging-row-grid { grid-template-columns: 1fr; } }
</style>
