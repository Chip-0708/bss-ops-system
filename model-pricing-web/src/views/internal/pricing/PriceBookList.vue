<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { pricingApi } from "../../../api/pricing";
import { changeRequestsApi } from "../../../api/changeRequests";
import type { ChangeRequestDetailDTO, ChangeRequestItemDTO } from "../../../api/changeRequests.types";
import { pricingPoliciesApi } from "../../../api/pricingPolicies";
import { modelsApi } from "../../../api/models";
import { modelContractId } from "../../../api/models.catalog";
import type { PricingPolicySummaryDTO } from "../../../api/pricingPolicies.types";
import type { ModelSkuContractDTO } from "../../../api/models.types";
import type { GeneratedPriceBookDTO, PriceBookDiffDTO, PublishedPriceBookDTO } from "../../../api/pricing.types";
import { ApiError } from "../../../domain/common";
import { formatDateTime } from "../../../domain/date";
import { PERMISSIONS } from "../../../domain/permissions";
import { usePermissionStore } from "../../../stores/permission";
import { useSessionStore } from "../../../stores/session";
import * as e2e from "../../../e2e/priceBookQuote";

const permissions = usePermissionStore();
const session = useSessionStore();
const e2eRun = ref<e2e.Run | null>(null);
const e2eError = ref("");
const e2eCreating = ref(false);
if (e2e.enabled) try { e2eRun.value = e2e.current(); } catch (error) { e2eError.value = String(error); }
function continueRun() {
  try {
    e2eRun.value = e2e.current();
    if (!e2eRun.value) return;
    publishForm.draftId = e2eRun.value.price_book_id;
    approval.changeRequestId = e2eRun.value.change_request_id || "";
    approval.priceBookId = e2eRun.value.price_book_id;
    approval.step1Approved = e2eRun.value.stage === "PRICING_APPROVED" || e2eRun.value.stage === "FINANCE_APPROVED" || e2eRun.value.stage === "COMPLETED";
    approval.completed = e2eRun.value.stage === "FINANCE_APPROVED" || e2eRun.value.stage === "COMPLETED";
    if (approval.changeRequestId) void loadApprovalDetail(approval.changeRequestId);
    e2eError.value = "";
  } catch (error) { e2eError.value = message(error); }
}
async function newRun() {
  if (e2eCreating.value || !canMaintain.value || !session.user) return;
  e2eCreating.value = true; e2eError.value = "";
  try {
    const previous = e2e.pending();
    const fixture = previous?.fixture || await e2e.loadFixture();
    const record = e2e.begin(session.user.id, fixture);
    const result = await pricingApi.generate({ levelCode: "GOLD", currency: fixture.currency,
      skuIds: fixture.sku_ids.map(Number), policyIds: [Number(fixture.policy_id)], operationId: record.operation_id });
    generated.value = result;
    try { e2eRun.value = e2e.finish(result, record); }
    catch (error) { e2e.discardPending(); throw error; }
    continueRun();
  } catch (error) {
    e2eError.value = message(error);
    if (error instanceof ApiError && !error.retryable) e2e.discardPending();
  } finally { e2eCreating.value = false; }
}
const canMaintain = computed(() => permissions.canAction(PERMISSIONS.PRICE_BOOK_EDIT));
const canPricingApprove = computed(() => session.user?.roleCodes?.includes("PRICING_OP") === true);
const canFinanceApprove = computed(() => session.user?.roleCodes?.includes("FINANCE") === true);
const generating = ref(false);
const publishing = ref(false);
const generateError = ref("");
const publishError = ref("");
const approvalError = ref("");
const generated = ref<GeneratedPriceBookDTO>();
const generatedSection = ref<HTMLElement>();
const published = ref<PublishedPriceBookDTO>();
const generateForm = reactive({ levelCode: "GOLD", currency: "USD", policyIds: [] as string[], skuIds: [] as string[] });
const policies = ref<PricingPolicySummaryDTO[]>([]);
const policyLoading = ref(false), policyError = ref("");
const skuChoices = ref<ModelSkuContractDTO[]>([]);
const selectedSkus = reactive(new Map<string, ModelSkuContractDTO>());
const manualSkuIds = ref("");
const skuLoading = ref(false), skuError = ref(""), skuKeyword = ref(""), skuPage = ref(1), skuTotal = ref(0);
const canReadSkus = computed(() => permissions.canAction(PERMISSIONS.MODEL_VIEW));
let skuSearchSequence = 0;
let operationRevision = 0;
const generatedBySignature = new Map<string, GeneratedPriceBookDTO>();
watch(generateForm, () => { operationRevision += 1; });
watch(manualSkuIds, () => { operationRevision += 1; });
const selectedSkuLabels = computed(() => generateForm.skuIds.map(id => {
  const sku = selectedSkus.get(id) || skuChoices.value.find(row => String(row.id) === id);
  return sku ? `${sku.vendor_name} / ${sku.family_name} · ${sku.sku_code} · ID ${id}` : `SKU ID ${id}`;
}));
async function loadPolicies() {
  if (!canMaintain.value || policyLoading.value) return;
  policyLoading.value = true; policyError.value = "";
  try {
    const loaded: PricingPolicySummaryDTO[] = [];
    for (let page = 1; ; page += 1) {
      const result = await pricingPoliciesApi.list({ page, size: 100 });
      loaded.push(...result.list);
      if (loaded.length >= result.total || !result.list.length) break;
    }
    policies.value = loaded;
  } catch (error) { policyError.value = message(error); }
  finally { policyLoading.value = false; }
}
async function searchSkus(keyword = "", append = false) {
  if (!canReadSkus.value) return;
  const sequence = ++skuSearchSequence;
  skuLoading.value = true; skuError.value = "";
  const page = append ? skuPage.value + 1 : 1;
  try {
    const result = await modelsApi.list({ view: "sku", keyword: keyword.trim() || undefined,
      lifecycle_status: "PUBLISHED", page, size: 20 });
    if (sequence !== skuSearchSequence) return;
    const list = result.list.filter((row): row is ModelSkuContractDTO => "sku_code" in row);
    skuChoices.value = append ? [...skuChoices.value, ...list] : list;
    skuPage.value = page; skuTotal.value = result.total;
  } catch (error) { if (sequence === skuSearchSequence) skuError.value = message(error); }
  finally { if (sequence === skuSearchSequence) skuLoading.value = false; }
}
function selectSkus(ids: string[]) {
  for (const id of ids) {
    const sku = skuChoices.value.find(row => modelContractId(row.id) === id);
    if (sku) selectedSkus.set(id, sku);
  }
  generateForm.skuIds = ids;
}
const publishForm = reactive({ draftId: "", effectiveTime: new Date().toISOString() });
const levelOptions = ["GOLD", "SILVER", "STANDARD", "ECONOMY"];
interface StoredApprovalProgress {
  changeRequestId: string;
  priceBookId?: string;
  step1Approved: boolean;
  completed: boolean;
}
const approval = reactive<StoredApprovalProgress>({ changeRequestId: "", step1Approved: false, completed: false });
const approvingStep = ref<1 | 2>();
const approvalRows = ref<ChangeRequestItemDTO[]>([]);
const approvalTotal = ref(0);
const approvalPage = ref(1);
const approvalLoading = ref(false);
const approvalDetail = ref<ChangeRequestDetailDTO>();

const message = (value: unknown) => value instanceof ApiError ? value.message : value instanceof Error ? value.message : "操作失败，请稍后重试。";
const approvalQueryMessage = (value: unknown) => value instanceof ApiError && value.httpStatus === 404
  ? "审批进度查询接口返回 404。请先部署包含 /api/internal/change-requests 的最新后端；发布返回的 change_request_id 已保留。"
  : message(value);
const formatTime = (value?: string | null) => formatDateTime(value, "未设置", false);
const objectOf = (value: unknown): Record<string, unknown> | undefined => value && typeof value === "object" && !Array.isArray(value)
  ? value as Record<string, unknown> : undefined;
const payloadOf = computed(() => objectOf(approvalDetail.value?.payload));
const payloadPriceBookId = computed(() => {
  const value = payloadOf.value?.price_book_id;
  return typeof value === "string" || typeof value === "number" ? String(value) : approval.priceBookId || "";
});
const payloadSkuIds = computed(() => Array.isArray(payloadOf.value?.sku_ids)
  ? payloadOf.value.sku_ids.filter(value => typeof value === "string" || typeof value === "number").map(String) : []);
const payloadEffectiveTime = computed(() => typeof payloadOf.value?.effective_time === "string" ? payloadOf.value.effective_time : "");
const payloadMode = computed(() => typeof payloadOf.value?.mode === "string" ? payloadOf.value.mode : "");
const payloadLevelCode = computed(() => typeof payloadOf.value?.level_code === "string" ? payloadOf.value.level_code : "");
const payloadVersionNo = computed(() => typeof payloadOf.value?.version_no === "number" ? payloadOf.value.version_no : undefined);
function diffRows(value: unknown): PriceBookDiffDTO[] {
  const source = Array.isArray(value) ? value : Array.isArray(objectOf(value)?.diff_report) ? objectOf(value)?.diff_report as unknown[] : [];
  return source.flatMap((raw) => {
    const row = objectOf(raw);
    if (!row || !(typeof row.sku_id === "string" || typeof row.sku_id === "number") || typeof row.sku_code !== "string"
      || typeof row.new_price !== "string" || typeof row.floor_price !== "string") return [];
    return [{ skuId: String(row.sku_id), skuCode: row.sku_code,
      oldPrice: typeof row.old_price === "string" ? row.old_price : null, newPrice: row.new_price,
      deltaPct: typeof row.delta_pct === "string" ? row.delta_pct : null, floorPrice: row.floor_price,
      floorViolation: row.floor_violation === true }];
  });
}
const approvalDiffReport = computed(() => {
  const serverRows = diffRows(payloadOf.value?.diff_report).length
    ? diffRows(payloadOf.value?.diff_report) : diffRows(approvalDetail.value?.marginPreview);
  if (serverRows.length) return serverRows;
  return generated.value && generated.value.draftId === payloadPriceBookId.value ? generated.value.diffReport : [];
});
const approvalMessage = computed(() => approvalDetail.value?.status === "REJECTED" ? "已驳回"
  : approval.completed ? "已生效"
    : approvalDetail.value?.pendingRole ? `等待 ${approvalDetail.value.pendingRole}`
      : approval.step1Approved ? "等待 FINANCE" : "等待 PRICING_OP");

function applyApprovalDetail(detail: ChangeRequestDetailDTO) {
  approvalDetail.value = detail;
  approval.changeRequestId = detail.id;
  const payload = objectOf(detail.payload);
  if (typeof payload?.price_book_id === "string" || typeof payload?.price_book_id === "number") approval.priceBookId = String(payload.price_book_id);
  approval.step1Approved = detail.steps.some(step => step.stepNo === 1 && step.decision === "APPROVED");
  approval.completed = detail.status === "APPROVED";
}
async function loadApprovalDetail(id: string) {
  approvalError.value = "";
  if (approvalDetail.value?.id !== id) approvalDetail.value = undefined;
  try { applyApprovalDetail(await changeRequestsApi.get(id)); }
  catch (error) { approvalError.value = approvalQueryMessage(error); }
}
async function loadApprovalRows() {
  approvalLoading.value = true; approvalError.value = "";
  try {
    const result = await changeRequestsApi.list({ type: "PRICE_BOOK_PUBLISH", page: approvalPage.value, size: 10 });
    approvalRows.value = result.list; approvalTotal.value = result.total;
    if (!approval.changeRequestId && result.list[0]) await loadApprovalDetail(result.list[0].id);
  } catch (error) { approvalRows.value = []; approvalTotal.value = 0; approvalError.value = approvalQueryMessage(error); }
  finally { approvalLoading.value = false; }
}
function changeApprovalPage(page: number) { approvalPage.value = page; void loadApprovalRows(); }

function numericIds(ids: string[], label: string) {
  const values = ids.map(Number);
  if (values.some(id => !Number.isSafeInteger(id) || id <= 0)) throw new Error(`${label}超出安全整数范围。`);
  return values;
}

async function generateDraft() {
  if (!canMaintain.value || generating.value) return;
  generateError.value = "";
  published.value = undefined;
  try {
    const levelCode = generateForm.levelCode.trim();
    if (!levelCode || !generateForm.currency) throw new Error("请填写客户等级和币种。");
    const policyIds = numericIds(generateForm.policyIds, "策略 ID");
    const fallbackIds = !canReadSkus.value && manualSkuIds.value.trim()
      ? manualSkuIds.value.split(/[,，\s]+/).filter(Boolean) : [];
    const skuIds = numericIds(canReadSkus.value ? generateForm.skuIds : fallbackIds, "SKU ID");
    const signature = JSON.stringify([levelCode, generateForm.currency, [...policyIds].sort((a,b)=>a-b), [...skuIds].sort((a,b)=>a-b)]);
    const existing = generatedBySignature.get(signature);
    if (existing) {
      generated.value = existing;
      publishForm.draftId = existing.draftId;
      ElMessage.info(`相同条件的草稿 #${existing.draftId} 已生成，请直接使用该草稿。`);
      await nextTick();
      generatedSection.value?.scrollIntoView({ behavior: "smooth", block: "start" });
      return;
    }
    generating.value = true;
    const result = await pricingApi.generate({ levelCode, currency: generateForm.currency, policyIds, skuIds,
      operationId: `${performance.timeOrigin}:${operationRevision}` });
    generated.value = result;
    generatedBySignature.set(signature, result);
    publishForm.draftId = result.draftId;
    publishForm.effectiveTime = new Date().toISOString();
    ElMessage.success("价目表草稿已生成。");
  } catch (value) { generateError.value = message(value); }
  finally { generating.value = false; }
}

async function publishDraft() {
  if (!canMaintain.value || publishing.value) return;
  publishError.value = "";
  published.value = undefined;
  try {
    const draftId = publishForm.draftId.trim();
    if (!/^[1-9]\d*$/.test(draftId)) throw new Error("draft_id 必须是正整数。");
    const timestamp = Date.parse(publishForm.effectiveTime);
    if (!Number.isFinite(timestamp)) throw new Error("effective_time 格式不正确。");
    if (timestamp > Date.now()) throw new Error("IMMEDIATE 模式的 effective_time 不能晚于当前时间。");
    publishing.value = true;
    published.value = await pricingApi.publish({ draftId, effectiveTime: new Date(timestamp).toISOString() });
    if (e2e.enabled && published.value.priceBookId === draftId)
      e2eRun.value = e2e.advance("CREATED", "PUBLISHED", run => run.price_book_id === draftId,
        { change_request_id: published.value.changeRequestId, price_book_version: published.value.versionNo }) || e2eRun.value;
    approval.changeRequestId = published.value.changeRequestId;
    approval.priceBookId = published.value.priceBookId;
    approval.step1Approved = false;
    approval.completed = false;
    await loadApprovalDetail(approval.changeRequestId);
    approvalPage.value = 1; await loadApprovalRows();
    ElMessage.success("草稿已进入 PRICING_OP → FINANCE 审批。");
  } catch (value) { publishError.value = message(value); }
  finally { publishing.value = false; }
}

async function approveStep(stepNo: 1 | 2) {
  const changeRequestId = approval.changeRequestId.trim();
  if (!/^[1-9]\d*$/.test(changeRequestId)) {
    ElMessage.error("change_request_id 必须是正整数。");
    return;
  }
  approvingStep.value = stepNo;
  approvalError.value = "";
  try {
    const result = await pricingApi.approve(changeRequestId, {
      stepNo,
      comment: stepNo === 1 ? "pricing approved" : "finance approved",
    });
    approval.changeRequestId = result.changeRequestId;
    if (e2e.enabled && result.changeRequestId === changeRequestId) {
      e2eRun.value = (stepNo === 1
        ? e2e.advance("PUBLISHED", "PRICING_APPROVED", run => run.change_request_id === changeRequestId && result.finalStatus === "PENDING")
        : e2e.advance("PRICING_APPROVED", "FINANCE_APPROVED", run => run.change_request_id === changeRequestId && result.finalStatus === "APPROVED")) || e2eRun.value;
    }
    await loadApprovalDetail(changeRequestId);
    await loadApprovalRows();
    ElMessage.success(result.finalStatus === "APPROVED"
      ? "审批完成，价目表已生效。"
      : "定价审批已通过，等待财务审批。");
  } catch (value) {
    const actionError = value instanceof ApiError && value.httpStatus === 409 && value.code === 10005 && value.message === "幂等冲突"
      ? `审批单 #${changeRequestId} 的上次请求结果未知；请核对服务端审批状态，不要通过更换幂等键重复提交。`
      : value instanceof ApiError && value.httpStatus >= 500
        ? `审批单 #${changeRequestId} 返回服务错误，结果未知；请先核对服务端状态再操作。`
        : message(value);
    ElMessage.error(actionError);
    await loadApprovalDetail(changeRequestId);
    approvalError.value = actionError;
  } finally {
    approvingStep.value = undefined;
  }
}
onMounted(loadApprovalRows);
</script>

<template>
  <section v-if="e2e.enabled" class="panel book-panel">
    <h2>PRICE_BOOK_QUOTE E2E 测试</h2>
    <p>当前 Run：{{ e2eRun?.run_id || '无' }} · 阶段：{{ e2eRun?.stage || '无' }}</p>
    <p>客户：{{ e2eRun?.customer_id || '—' }} · Price Book：{{ e2eRun?.price_book_id || '—' }} · 审批单：{{ e2eRun?.change_request_id || '—' }}</p>
    <el-alert v-if="e2eError" :title="e2eError" type="error" :closable="false" />
    <el-button @click="continueRun">继续当前测试</el-button>
    <el-button :loading="e2eCreating" :disabled="!canMaintain || generating" @click="newRun">新建一轮测试</el-button>
  </section>
  <el-alert title="当前后端只提供草稿生成与发布。列表、详情、草稿编辑、历史回滚均未开放；发布固定为 IMMEDIATE，并进入 PRICING_OP → FINANCE 两级审批。" type="info" show-icon :closable="false" />
  <section class="page-heading">
    <div><div class="eyebrow">PRICE BOOKS</div><h1>价目表</h1><p>按真实定价策略生成草稿，并将指定草稿提交发布审批。</p></div>
  </section>

  <section class="panel book-panel">
    <div class="section-title"><h2>生成价目表草稿</h2></div>
    <el-form class="book-form" label-position="top" :disabled="generating || !canMaintain">
      <div class="book-form-grid">
        <el-form-item label="客户等级 level_code *"><el-select v-model="generateForm.levelCode" filterable allow-create default-first-option><el-option v-for="level in levelOptions" :key="level" :label="level" :value="level" /></el-select></el-form-item>
        <el-form-item label="币种 currency *"><el-select v-model="generateForm.currency"><el-option label="USD" value="USD" /><el-option label="CNY" value="CNY" /></el-select></el-form-item>
        <el-form-item label="定价策略 policy_ids（可选）"><el-select v-model="generateForm.policyIds" multiple filterable collapse-tags placeholder="留空由后端匹配 ACTIVE 策略" @visible-change="(open: boolean) => { if (open && !policies.length) loadPolicies(); }"><el-option v-for="policy in policies" :key="policy.id" :label="`${policy.name} · ${policy.code} · #${policy.id} · ${policy.status} · ${policy.levelCode ?? '全部等级'}`" :value="policy.id" /></el-select></el-form-item>
        <el-form-item label="SKU sku_ids（可选）"><el-select :model-value="generateForm.skuIds" multiple filterable remote reserve-keyword :remote-method="(value: string) => { skuKeyword = value; searchSkus(value); }" :loading="skuLoading" collapse-tags placeholder="留空使用全部在架 SKU" :disabled="!canReadSkus" @visible-change="(open: boolean) => { if (open && !skuChoices.length) searchSkus(); }" @update:model-value="selectSkus"><el-option v-for="sku in [...skuChoices, ...[...selectedSkus.values()].filter(row => !skuChoices.some(choice => String(choice.id) === String(row.id)))]" :key="String(sku.id)" :label="`${sku.vendor_name} / ${sku.family_name} · ${sku.sku_code} · ID ${sku.id}`" :value="String(sku.id)" /></el-select></el-form-item>
      </div>
      <el-alert v-if="policyError" :title="`策略查询失败：${policyError}`" type="error" :closable="false"><el-button @click="loadPolicies">重试</el-button></el-alert>
      <el-alert v-if="skuError" :title="`SKU 查询失败：${skuError}`" type="error" :closable="false"><el-button @click="searchSkus(skuKeyword)">重试</el-button></el-alert>
      <el-form-item v-if="!canReadSkus" label="SKU ID（无模型查看权限，可手工填写）"><el-input v-model="manualSkuIds" placeholder="逗号分隔；留空为全部在架 SKU" /></el-form-item>
      <el-button v-if="canReadSkus && skuChoices.length < skuTotal" text :loading="skuLoading" @click="searchSkus(skuKeyword, true)">加载更多 SKU</el-button>
      <p v-if="generateForm.skuIds.length" class="muted">已选 SKU：{{ selectedSkuLabels.join('；') }}</p>
      <el-alert v-if="generateError" :title="generateError" type="error" show-icon :closable="false" />
      <div class="book-actions"><el-button type="primary" :loading="generating" @click="generateDraft">生成草稿</el-button></div>
    </el-form>
  </section>

  <section v-if="generated" ref="generatedSection" class="panel book-panel">
    <div class="section-title"><h2>草稿生成结果</h2><el-tag type="info">DRAFT</el-tag></div>
    <el-descriptions :column="3" border>
      <el-descriptions-item label="draft_id"><span class="mono">{{ generated.draftId }}</span></el-descriptions-item>
      <el-descriptions-item label="生成条目">{{ generated.itemCount }}</el-descriptions-item>
      <el-descriptions-item label="阻塞条目"><span :class="{ 'book-danger': generated.blockedCount > 0 }">{{ generated.blockedCount }}</span></el-descriptions-item>
      <el-descriptions-item label="客户等级">{{ generated.levelCode }}</el-descriptions-item>
      <el-descriptions-item label="币种">{{ generated.currency }}</el-descriptions-item>
    </el-descriptions>
    <h3>diff_report</h3>
    <el-table :data="generated.diffReport" border empty-text="后端未生成可定价条目">
      <el-table-column label="SKU" min-width="180"><template #default="{ row }"><b>{{ row.skuCode }}</b><div class="mono muted">{{ row.skuId }}</div></template></el-table-column>
      <el-table-column label="原价" min-width="130"><template #default="{ row }"><span class="mono">{{ row.oldPrice ?? '首次定价' }}</span></template></el-table-column>
      <el-table-column label="新价" min-width="130"><template #default="{ row }"><span class="mono">{{ row.newPrice }}</span></template></el-table-column>
      <el-table-column label="变化比例" min-width="120"><template #default="{ row }">{{ row.deltaPct ?? '—' }}</template></el-table-column>
      <el-table-column label="floor" min-width="130"><template #default="{ row }"><span class="mono">{{ row.floorPrice }}</span></template></el-table-column>
      <el-table-column label="校验" width="120"><template #default="{ row }"><el-tag :type="row.floorViolation ? 'danger' : 'success'">{{ row.floorViolation ? '阻塞' : '通过' }}</el-tag></template></el-table-column>
    </el-table>
  </section>

  <section class="panel book-panel">
    <div class="section-title"><h2>发布草稿</h2><el-tag>IMMEDIATE</el-tag></div>
    <el-alert title="发布不会直接生效：成功后状态为 APPROVING，依次由 PRICING_OP、FINANCE 审批。存在 floor_violation 的草稿会被后端拒绝。" type="warning" show-icon :closable="false" />
    <el-form class="book-form" label-position="top" :disabled="publishing || !canMaintain">
      <div class="book-form-grid">
        <el-form-item label="草稿 ID draft_id *"><el-input v-model="publishForm.draftId" placeholder="可复用上方生成结果或手工输入" /></el-form-item>
        <el-form-item label="生效时间 effective_time *"><el-date-picker v-model="publishForm.effectiveTime" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" placeholder="IMMEDIATE 要求不得晚于当前时间" /></el-form-item>
      </div>
      <el-alert v-if="publishError" :title="publishError" type="error" show-icon :closable="false" />
      <div class="book-actions"><el-button type="primary" :loading="publishing" @click="publishDraft">发布草稿</el-button></div>
    </el-form>
    <el-result v-if="published" icon="success" title="已发布（已提交审批）" sub-title="等待 PRICING_OP → 等待 FINANCE → 已生效">
      <template #extra>
        <el-descriptions :column="2" border>
          <el-descriptions-item label="状态"><el-tag type="warning">{{ published.status }}</el-tag></el-descriptions-item>
          <el-descriptions-item label="价目表 ID"><span class="mono">{{ published.priceBookId }}</span></el-descriptions-item>
          <el-descriptions-item label="change_request_id"><span class="mono">{{ published.changeRequestId }}</span></el-descriptions-item>
          <el-descriptions-item label="审批步数">{{ published.stepCount }}</el-descriptions-item>
          <el-descriptions-item label="版本号">V{{ published.versionNo }}</el-descriptions-item>
          <el-descriptions-item label="effective_time">{{ formatTime(published.effectiveTime) }}</el-descriptions-item>
        </el-descriptions>
      </template>
    </el-result>
    <div class="section-title"><h3>价目表审批记录</h3><el-button :loading="approvalLoading" @click="loadApprovalRows">查询</el-button></div>
    <el-table v-loading="approvalLoading" :data="approvalRows" row-key="id" empty-text="暂无价目表发布审批记录">
      <el-table-column label="审批单" width="110"><template #default="{ row }">#{{ row.id }}</template></el-table-column>
      <el-table-column label="状态" width="110" prop="status" />
      <el-table-column label="进度" min-width="190"><template #default="{ row }">{{ row.stepsApproved }} / {{ row.stepsTotal }}<span v-if="row.pendingRole" class="muted"> · 等待 {{ row.pendingRole }}</span></template></el-table-column>
      <el-table-column label="更新时间" min-width="170"><template #default="{ row }">{{ formatTime(row.updatedAt) }}</template></el-table-column>
      <el-table-column label="操作" width="90"><template #default="{ row }"><el-button link type="primary" @click="loadApprovalDetail(row.id)">查看 / 审批</el-button></template></el-table-column>
    </el-table>
    <el-pagination :current-page="approvalPage" :page-size="10" :total="approvalTotal" layout="total, prev, pager, next" @current-change="changeApprovalPage" />
    <div class="approval-actions" v-if="approval.changeRequestId">
      <div class="section-title"><h3>审批操作</h3><el-tag :type="approval.completed ? 'success' : 'warning'">{{ approvalMessage }}</el-tag></div>
      <p class="muted" v-if="approvalDetail">审批单 #{{ approval.changeRequestId }} · {{ approvalDetail.stepsApproved }} / {{ approvalDetail.stepsTotal }} 步已通过，进度来自服务端。</p>
      <p class="muted" v-else>审批单 #{{ approval.changeRequestId }} 已由发布接口返回；等待查询服务返回权威进度。</p>
      <el-alert v-if="approvalError" :title="approvalError" type="error" show-icon :closable="false" />
      <template v-if="approvalDetail">
        <h4>价目表审批资料</h4>
        <el-descriptions :column="3" border>
          <el-descriptions-item label="审批单">#{{ approvalDetail.id }}</el-descriptions-item>
          <el-descriptions-item label="价目表 ID">{{ payloadPriceBookId || '—' }}</el-descriptions-item>
          <el-descriptions-item label="客户等级">{{ payloadLevelCode || '—' }}</el-descriptions-item>
          <el-descriptions-item label="版本">{{ payloadVersionNo === undefined ? '—' : `V${payloadVersionNo}` }}</el-descriptions-item>
          <el-descriptions-item label="风险等级">{{ approvalDetail.riskLevel || '—' }}</el-descriptions-item>
          <el-descriptions-item label="发布模式">{{ payloadMode || '—' }}</el-descriptions-item>
          <el-descriptions-item label="生效时间">{{ formatTime(payloadEffectiveTime) }}</el-descriptions-item>
          <el-descriptions-item label="当前待审">{{ approvalDetail.pendingRole || '已终结' }}</el-descriptions-item>
        </el-descriptions>
        <p class="muted">涉及 SKU：{{ payloadSkuIds.length ? payloadSkuIds.map(id => `#${id}`).join('、') : '后端未返回' }}</p>
        <el-table v-if="approvalDiffReport.length" :data="approvalDiffReport" border>
          <el-table-column label="SKU" min-width="180"><template #default="{ row }"><b>{{ row.skuCode }}</b><div class="mono muted">{{ row.skuId }}</div></template></el-table-column>
          <el-table-column label="原价" min-width="130"><template #default="{ row }"><span class="mono">{{ row.oldPrice ?? '首次定价' }}</span></template></el-table-column>
          <el-table-column label="新价" min-width="130"><template #default="{ row }"><span class="mono">{{ row.newPrice }}</span></template></el-table-column>
          <el-table-column label="变化比例" min-width="120"><template #default="{ row }">{{ row.deltaPct ?? '—' }}</template></el-table-column>
          <el-table-column label="floor" min-width="130"><template #default="{ row }"><span class="mono">{{ row.floorPrice }}</span></template></el-table-column>
          <el-table-column label="校验" width="100"><template #default="{ row }"><el-tag :type="row.floorViolation ? 'danger' : 'success'">{{ row.floorViolation ? '阻塞' : '通过' }}</el-tag></template></el-table-column>
        </el-table>
        <el-alert v-else title="该审批单未携带价格差异。旧审批单不会自动补齐历史明细；新发布的价目表需使用包含 diff_report 的最新后端。" type="warning" show-icon :closable="false" />
        <h4>审批步骤</h4>
        <el-table :data="approvalDetail.steps" border>
          <el-table-column prop="stepNo" label="步骤" width="80" />
          <el-table-column prop="requiredRole" label="审批角色" min-width="140" />
          <el-table-column label="结果" width="110"><template #default="{ row }"><el-tag :type="row.decision === 'APPROVED' ? 'success' : row.decision === 'REJECTED' ? 'danger' : 'warning'">{{ row.decision || '待审批' }}</el-tag></template></el-table-column>
          <el-table-column label="审批人" min-width="110"><template #default="{ row }">{{ row.approverId || '—' }}</template></el-table-column>
          <el-table-column label="审批时间" min-width="170"><template #default="{ row }">{{ formatTime(row.decidedAt) }}</template></el-table-column>
          <el-table-column label="意见" min-width="160"><template #default="{ row }">{{ row.comment || '—' }}</template></el-table-column>
        </el-table>
      </template>
      <div class="book-actions">
        <el-button v-if="canPricingApprove" :type="approval.step1Approved ? 'info' : 'primary'" :loading="approvingStep === 1" :disabled="Boolean(approvingStep) || approval.step1Approved || approvalDetail?.pendingRole !== 'PRICING_OP'" @click="approveStep(1)">{{ approval.step1Approved ? '定价已审核' : '定价审批通过' }}</el-button>
        <el-button v-if="canFinanceApprove && approval.step1Approved" :type="approval.completed ? 'info' : 'primary'" :loading="approvingStep === 2" :disabled="Boolean(approvingStep) || approval.completed || approvalDetail?.pendingRole !== 'FINANCE'" @click="approveStep(2)">{{ approval.completed ? '财务已审核' : '财务审批通过' }}</el-button>
      </div>
    </div>
  </section>
</template>
