<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { customersApi } from "../../../api/customers";
import type { CustomerListItemDTO, CustomerTransferImpactDTO } from "../../../api/customers.types";
import { customerQuotesApi } from "../../../api/customerQuotes";
import { modelsApi } from "../../../api/models";
import type { ModelSkuContractDTO } from "../../../api/models.types";
import type { CustomerQuoteContextDTO, CustomerQuoteHistoryPreviewDTO, CustomerQuotePreviewItemDTO, CustomerQuoteType, GenerateCustomerQuoteDraft, GenerateCustomerQuoteItem } from "../../../api/customerQuotes.types";
import { ApiError } from "../../../domain/common";
import { formatDateTime } from "../../../domain/date";
import { isDecimalAmount } from "../../../domain/money";
import { PERMISSIONS } from "../../../domain/permissions";
import { usePermissionStore } from "../../../stores/permission";
import * as e2e from "../../../e2e/priceBookQuote";

const permissions = usePermissionStore();
const canTransfer = computed(() => permissions.canAction(PERMISSIONS.CUSTOMER_EDIT));
const canGenerateQuote = computed(() => permissions.canAction(PERMISSIONS.CUSTOMER_QUOTE_EDIT));
const canReadSkus = computed(() => permissions.canAction(PERMISSIONS.MODEL_VIEW));
const skuChoices = ref<ModelSkuContractDTO[]>([]);
const selectedSkus = reactive(new Map<string, ModelSkuContractDTO>());
const skuKeyword = ref(""), skuError = ref(""), skuLoading = ref(false), skuPage = ref(1), skuTotal = ref(0);
let skuSequence = 0;
async function searchSkus(keyword = "", append = false) {
  if (!canReadSkus.value) return;
  const sequence = ++skuSequence; skuLoading.value = true; skuError.value = "";
  const page = append ? skuPage.value + 1 : 1;
  try {
    const result = await modelsApi.list({ view: "sku", keyword: keyword.trim() || undefined, page, size: 20, lifecycle_status: "PUBLISHED" });
    if (sequence !== skuSequence) return;
    const list = result.list.filter((row): row is ModelSkuContractDTO => "sku_code" in row);
    skuChoices.value = append ? [...skuChoices.value, ...list] : list;
    skuPage.value = page; skuTotal.value = result.total;
  } catch (error) { if (sequence === skuSequence) skuError.value = message(error); }
  finally { if (sequence === skuSequence) skuLoading.value = false; }
}
function skuLabel(id: string) {
  const sku = selectedSkus.get(id) || skuChoices.value.find(row => String(row.id) === id);
  return sku ? `${sku.vendor_name} / ${sku.family_name} · ${sku.sku_code} · ID ${id}` : `SKU ID ${id}`;
}
function rememberSku(id: string) {
  const sku = skuChoices.value.find(row => String(row.id) === id);
  if (sku) selectedSkus.set(id, sku);
}
const message = (value: unknown) => value instanceof ApiError ? value.message : value instanceof Error ? value.message : "操作失败，请稍后重试。";
const positiveId = (value: string) => /^[1-9]\d*$/.test(value.trim()) && Number.isSafeInteger(Number(value));

const rows = ref<CustomerListItemDTO[]>([]);
const loading = ref(false);
const loadError = ref("");
const pagination = reactive({ page: 1, size: 10, total: 0 });
const keyword = ref("");
let listSequence = 0;

async function loadCustomers() {
  const sequence = ++listSequence;
  loading.value = true;
  loadError.value = "";
  try {
    const result = await customersApi.list({ page: pagination.page, size: pagination.size, keyword: keyword.value.trim() || undefined });
    if (sequence !== listSequence) return;
    rows.value = result.list;
    pagination.total = result.total;
  } catch (error) {
    if (sequence !== listSequence) return;
    rows.value = [];
    pagination.total = 0;
    loadError.value = message(error);
  } finally {
    if (sequence === listSequence) loading.value = false;
  }
}
function query() { pagination.page = 1; void loadCustomers(); }
function reset() { keyword.value = ""; query(); }
function changePage(page: number) { pagination.page = page; void loadCustomers(); }

const quoteOpen = ref(false);
const quotePreviewOpen = ref(false);
const quoteSubmitting = ref(false);
const quoteError = ref("");
const quotePreview = ref<{ draft: GenerateCustomerQuoteDraft; customerName: string; levelCode: string;
  typeLabel: string; hint: string; items: Array<{ label: string; unitPrice: string }> }>();
const quoteForm = reactive<{ customerId: string; quoteType: CustomerQuoteType; sourceQuoteId: string; validTo: string; items: GenerateCustomerQuoteItem[] }>({
  customerId: "", quoteType: "APPLY", sourceQuoteId: "", validTo: "", items: [],
});
const quoteCustomers = ref<CustomerListItemDTO[]>([]);
const selectedQuoteCustomer = ref<CustomerListItemDTO>();
const quoteCustomerOptions = computed(() => selectedQuoteCustomer.value && !quoteCustomers.value.some(row => row.id === selectedQuoteCustomer.value?.id)
  ? [...quoteCustomers.value, selectedQuoteCustomer.value] : quoteCustomers.value);
const quoteCustomerKeyword = ref(""), quoteCustomerPage = ref(1), quoteCustomerTotal = ref(0);
const quoteCustomerLoading = ref(false), quoteCustomerError = ref("");
let quoteCustomerSequence = 0;
const quoteContext = ref<CustomerQuoteContextDTO>();
const quoteContextLoading = ref(false);
const quoteContextError = ref("");
let quoteContextSequence = 0;
async function loadQuoteContext(customerId: string) {
  const sequence = ++quoteContextSequence;
  quoteContext.value = undefined;
  quoteContextError.value = "";
  if (!positiveId(customerId)) return;
  quoteContextLoading.value = true;
  try {
    const result = await customerQuotesApi.context(customerId);
    if (sequence === quoteContextSequence) quoteContext.value = result;
  } catch (error) {
    if (sequence === quoteContextSequence) quoteContextError.value = message(error);
  } finally {
    if (sequence === quoteContextSequence) quoteContextLoading.value = false;
  }
}
async function searchQuoteCustomers(value = "", append = false) {
  const sequence = ++quoteCustomerSequence;
  quoteCustomerKeyword.value = value;
  quoteCustomerLoading.value = true; quoteCustomerError.value = "";
  const page = append ? quoteCustomerPage.value + 1 : 1;
  try {
    const result = await customersApi.list({ page, size: 20, keyword: value.trim() || undefined });
    if (sequence !== quoteCustomerSequence) return;
    quoteCustomers.value = append ? [...quoteCustomers.value, ...result.list] : result.list;
    quoteCustomerPage.value = page; quoteCustomerTotal.value = result.total;
  } catch (error) { if (sequence === quoteCustomerSequence) quoteCustomerError.value = message(error); }
  finally { if (sequence === quoteCustomerSequence) quoteCustomerLoading.value = false; }
}
function rememberQuoteCustomer(id: string) {
  selectedQuoteCustomer.value = quoteCustomers.value.find(row => row.id === id)
    || (selectedQuoteCustomer.value?.id === id ? selectedQuoteCustomer.value : undefined);
  quoteForm.sourceQuoteId = "";
  void loadQuoteContext(id);
}
const quoteTypeOptions: Array<{ value: CustomerQuoteType; label: string; hint: string }> = [
  { value: "APPLY", label: "套用当前价目表", hint: "系统按客户 level_code 自动匹配当前生效价目表，无需手工选择价目表。" },
  { value: "CLONE", label: "克隆历史报价", hint: "从该客户过去的报价中选择一份，由服务端复制价格。" },
  { value: "TEMP", label: "临时报价", hint: "填写到期时间，以及 SKU ID 与客户单价。" },
];
const quoteHint = computed(() => quoteTypeOptions.find(option => option.value === quoteForm.quoteType)?.hint || "");
const chosenCustomerLevel = computed(() => selectedQuoteCustomer.value?.levelCode || "");
const selectedHistoryQuote = computed<CustomerQuoteHistoryPreviewDTO | undefined>(() =>
  quoteContext.value?.history.list.find(row => row.id === quoteForm.sourceQuoteId));
const contextPreviewItems = computed<CustomerQuotePreviewItemDTO[]>(() => {
  if (quoteForm.quoteType === "APPLY") return quoteContext.value?.priceBook?.items || [];
  if (quoteForm.quoteType === "CLONE") return selectedHistoryQuote.value?.items || [];
  return [];
});
const quoteActionOpen = ref(false);
const quoteActionSubmitting = ref(false);
const quoteActionError = ref("");
const quoteActionForm = reactive({ quoteId: "", reason: "", expectedMargin: "0.08" });

function openQuoteActions(id = "") {
  Object.assign(quoteActionForm, { quoteId: id, reason: "", expectedMargin: "0.08" });
  quoteActionError.value = "";
  quoteActionOpen.value = true;
}

function openQuote(customer?: CustomerListItemDTO) {
  Object.assign(quoteForm, { customerId: customer?.id || "", quoteType: "APPLY", sourceQuoteId: "", validTo: "", items: [] });
  selectedQuoteCustomer.value = customer;
  quoteCustomers.value = customer ? [customer] : [];
  quoteCustomerKeyword.value = "";
  quoteContext.value = undefined;
  quoteContextError.value = "";
  if (customer) void loadQuoteContext(customer.id);
  void searchQuoteCustomers();
  quotePreviewOpen.value = false;
  quotePreview.value = undefined;
  quoteError.value = "";
  quoteOpen.value = true;
}
function changeQuoteType() {
  quoteForm.sourceQuoteId = "";
  quoteForm.validTo = "";
  quoteForm.items = [];
  quotePreview.value = undefined;
  quoteError.value = "";
}
function addQuoteItem() { quoteForm.items.push({ skuId: "", unitPrice: "" }); }

function floorViolationMessage(error: unknown) {
  if (!(error instanceof ApiError) || !error.details || typeof error.details !== "object") return "";
  const details = error.details as { floor_violations?: unknown };
  if (!Array.isArray(details.floor_violations)) return "";
  const lines = details.floor_violations.flatMap(item => {
    if (!item || typeof item !== "object") return [];
    const row = item as Record<string, unknown>;
    return [`${String(row.sku_code || `SKU ${row.sku_id || "?"}`)}：报价 ${String(row.unit_price || "-")}，floor ${String(row.floor_price || "-")}`];
  });
  return lines.length ? `${error.message} ${lines.join("；")}` : "";
}

function validateQuoteForm() {
  if (!positiveId(quoteForm.customerId)) return "请选择有效客户。";
  if (quoteContextLoading.value) return "正在加载该客户的报价信息，请稍候。";
  if (quoteContextError.value) return "报价信息加载失败，请重试后再提交。";
  if (quoteForm.quoteType === "APPLY" && !quoteContext.value?.priceBook) return "该客户等级暂无当前生效价目表，不能套用。";
  if (quoteForm.quoteType === "CLONE" && !selectedHistoryQuote.value) return "请选择该客户的一份历史报价。";
  if (quoteForm.quoteType === "TEMP") {
    if (!quoteForm.validTo || Number.isNaN(Date.parse(quoteForm.validTo))) return "请选择临时报价到期时间。";
    if (!quoteForm.items.length) return "临时报价至少需要一个 SKU 价格项。";
    if (quoteForm.items.some(item => !positiveId(item.skuId) || !isDecimalAmount(item.unitPrice)))
      return "SKU ID 必须为正整数，报价必须为大于 0 且最多 8 位小数的普通十进制字符串。";
    if (new Set(quoteForm.items.map(item => item.skuId.trim())).size !== quoteForm.items.length)
      return "同一 SKU 不能重复报价。";
  }
  return "";
}

function openQuotePreview() {
  quoteError.value = validateQuoteForm();
  if (quoteError.value) return;
  const customer = selectedQuoteCustomer.value;
  quotePreview.value = {
    draft: {
      customerId: quoteForm.customerId.trim(), quoteType: quoteForm.quoteType,
      sourceQuoteId: quoteForm.quoteType === "CLONE" ? quoteForm.sourceQuoteId.trim() : undefined,
      validTo: quoteForm.quoteType === "TEMP" ? new Date(quoteForm.validTo).toISOString() : undefined,
      items: quoteForm.quoteType === "TEMP" ? quoteForm.items.map(item => ({ skuId: item.skuId.trim(), unitPrice: item.unitPrice.trim() })) : undefined,
    },
    customerName: customer?.legalName || `客户 ID ${quoteForm.customerId}`,
    levelCode: customer?.levelCode || "未提供",
    typeLabel: quoteTypeOptions.find(option => option.value === quoteForm.quoteType)?.label || quoteForm.quoteType,
    hint: quoteHint.value,
    items: quoteForm.quoteType === "TEMP"
      ? quoteForm.items.map(item => ({ label: skuLabel(item.skuId), unitPrice: item.unitPrice.trim() }))
      : contextPreviewItems.value.map(item => ({ label: `${item.skuCode} · ID ${item.skuId}`, unitPrice: `${item.unitPrice} ${item.currency}` })),
  };
  quotePreviewOpen.value = true;
}

async function generateQuote() {
  if (quoteSubmitting.value || !quotePreview.value) return;
  quoteError.value = "";
  const draft = quotePreview.value.draft;
  quoteSubmitting.value = true;
  try {
    const result = await customerQuotesApi.generate(draft);
    if (e2e.enabled && draft.quoteType === "APPLY") {
      e2e.advance("FINANCE_APPROVED", "COMPLETED", run =>
        run.customer_id === draft.customerId && run.price_book_version !== null &&
        run.price_book_version === result.priceBookVersion && result.itemCount > 0,
      { customer_quote_id: result.id });
    }
    quotePreviewOpen.value = false;
    quoteOpen.value = false;
    ElMessage.success(`客户报价 #${result.id} 已生成：V${result.versionNo}，${result.itemCount} 项，状态 ${result.status}。`);
    openQuoteActions(result.id);
  } catch (error) {
    quoteError.value = floorViolationMessage(error) || message(error);
  } finally {
    quoteSubmitting.value = false;
  }
}

async function requestSpecialPrice() {
  if (!positiveId(quoteActionForm.quoteId)) return void (quoteActionError.value = "请输入有效的报价 ID。");
  if (!quoteActionForm.reason.trim()) return void (quoteActionError.value = "申请特价必须填写原因。");
  if (!isDecimalAmount(quoteActionForm.expectedMargin)) return void (quoteActionError.value = "期望毛利必须是合法十进制数。");
  quoteActionSubmitting.value = true; quoteActionError.value = "";
  try {
    const result = await customerQuotesApi.requestSpecialPrice(quoteActionForm.quoteId.trim(), quoteActionForm.reason, quoteActionForm.expectedMargin);
    ElMessage.success(`特价申请已提交，变更单 #${result.changeRequestId}，共 ${result.stepCount} 级审批。`);
  } catch (error) { quoteActionError.value = message(error); }
  finally { quoteActionSubmitting.value = false; }
}

async function refreshQuote() {
  if (!positiveId(quoteActionForm.quoteId)) return void (quoteActionError.value = "请输入有效的报价 ID。");
  quoteActionSubmitting.value = true; quoteActionError.value = "";
  try {
    const result = await customerQuotesApi.refresh(quoteActionForm.quoteId.trim(), quoteActionForm.reason);
    ElMessage.success(result.unchanged ? "最新成本未改变 floor，未生成新版本。" : `已刷新为 V${result.newVersionNo}，变更 ${result.changedItems.length} 项。`);
  } catch (error) { quoteActionError.value = message(error); }
  finally { quoteActionSubmitting.value = false; }
}

async function exportQuote() {
  if (!positiveId(quoteActionForm.quoteId)) return void (quoteActionError.value = "请输入有效的报价 ID。");
  quoteActionSubmitting.value = true; quoteActionError.value = "";
  try {
    const blob = await customerQuotesApi.exportXlsx(quoteActionForm.quoteId.trim());
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a"); link.href = url; link.download = `customer_quote_${quoteActionForm.quoteId.trim()}.xlsx`; link.click();
    URL.revokeObjectURL(url);
  } catch (error) { quoteActionError.value = message(error); }
  finally { quoteActionSubmitting.value = false; }
}

const transferOpen = ref(false);
const transferSubmitting = ref(false);
const transferCustomer = ref<CustomerListItemDTO>();
const transferForm = reactive({ toOperatorId: "", reason: "" });
const transferImpact = ref<CustomerTransferImpactDTO>();
const transferError = ref("");
let transferSequence = 0;

function openTransfer(customer: CustomerListItemDTO) {
  ++transferSequence;
  transferCustomer.value = customer;
  transferSubmitting.value = false;
  Object.assign(transferForm, { toOperatorId: "", reason: "" });
  transferImpact.value = undefined;
  transferError.value = "";
  transferOpen.value = true;
}
function invalidateTransferPreview() { ++transferSequence; transferImpact.value = undefined; transferError.value = ""; }
async function previewTransfer() {
  if (!transferCustomer.value || transferSubmitting.value) return;
  if (!positiveId(transferForm.toOperatorId)) return void (transferError.value = "请输入有效的目标销售人员 ID。");
  const sequence = ++transferSequence;
  const customerId = transferCustomer.value.id;
  const draft = { toOperatorId: transferForm.toOperatorId.trim(), reason: transferForm.reason.trim() };
  transferSubmitting.value = true;
  transferError.value = "";
  try {
    const impact = await customersApi.transferPreview(customerId, draft);
    if (sequence === transferSequence && transferCustomer.value?.id === customerId) transferImpact.value = impact;
  } catch (error) {
    if (sequence === transferSequence) { transferImpact.value = undefined; transferError.value = message(error); }
  } finally {
    if (sequence === transferSequence) transferSubmitting.value = false;
  }
}
async function confirmTransfer() {
  if (!transferCustomer.value || !transferImpact.value || transferSubmitting.value) return;
  const customerId = transferCustomer.value.id;
  const impact = transferImpact.value;
  const draft = { toOperatorId: transferForm.toOperatorId.trim(), reason: transferForm.reason.trim() };
  transferSubmitting.value = true;
  try {
    await ElMessageBox.confirm(`确认将客户从“${impact.fromOperatorName}”移交给“${impact.toOperatorName}”？${impact.quoteCount} 份报价将随迁。`, "确认客户移交", { type: "warning" });
  } catch { transferSubmitting.value = false; return; }
  transferError.value = "";
  try {
    const result = await customersApi.transferConfirm(customerId, draft);
    transferOpen.value = false;
    ElMessage.success(`客户移交完成，已迁移 ${result.quotesMigrated} 份报价。`);
    await loadCustomers();
  } catch (error) {
    transferError.value = message(error);
  } finally {
    transferSubmitting.value = false;
  }
}

onMounted(loadCustomers);
</script>

<template>
  <section class="panel customer-panel">
    <div class="section-title">
      <div><h2>客户管理 <span>{{ pagination.total }}</span></h2><p class="muted">真实后端阶段 9a/9b：客户、报价生成、特价、刷新与导出。</p></div>
      <div class="header-actions"><el-button :loading="loading" @click="loadCustomers">刷新</el-button><el-button v-if="canGenerateQuote" @click="openQuoteActions()">报价后续操作</el-button><el-button v-if="canGenerateQuote" type="primary" @click="openQuote()">生成报价</el-button></div>
    </div>
    <div class="filters customer-filters"><el-input v-model="keyword" clearable aria-label="客户关键字" placeholder="按客户法定名称搜索" @keyup.enter="query" /><el-button type="primary" @click="query">查询</el-button><el-button text @click="reset">重置</el-button></div>
    <el-alert v-if="loadError" :title="loadError" type="error" show-icon :closable="false"><el-button text @click="loadCustomers">重新加载</el-button></el-alert>
    <div v-loading="loading" class="customer-list"><el-empty v-if="!rows.length && !loading && !loadError" description="没有符合条件的客户" /><el-table v-else :data="rows" row-key="id"><el-table-column label="客户" min-width="210"><template #default="{ row }"><b>{{ row.legalName }}</b><div class="mono muted">ID {{ row.id }} · 主体 {{ row.subjectId }}</div></template></el-table-column><el-table-column prop="levelCode" label="等级" width="105" /><el-table-column label="归属销售" min-width="150"><template #default="{ row }">{{ row.ownerSalesName }}<div class="mono muted">{{ row.ownerSalesOperatorId }}</div></template></el-table-column><el-table-column label="授信" min-width="170"><template #default="{ row }"><span class="mono">{{ row.creditUsed }} / {{ row.creditLimit }}</span></template></el-table-column><el-table-column label="押金" min-width="135"><template #default="{ row }"><span class="mono">{{ row.depositAmount }}</span><div class="muted">{{ row.depositStatus }}</div></template></el-table-column><el-table-column label="状态 / 创建" min-width="170"><template #default="{ row }"><el-tag :type="row.status === 'ACTIVE' ? 'success' : 'info'">{{ row.status }}</el-tag><div class="muted">{{ formatDateTime(row.createdAt, '暂缺', false) }}</div></template></el-table-column><el-table-column label="操作" width="170" fixed="right"><template #default="{ row }"><el-button v-if="canGenerateQuote" link type="primary" @click="openQuote(row)">生成报价</el-button><el-button v-if="canTransfer" link @click="openTransfer(row)">移交</el-button></template></el-table-column></el-table></div>
    <div class="catalog-pagination"><el-pagination :current-page="pagination.page" :page-size="pagination.size" :total="pagination.total" layout="total, prev, pager, next" @current-change="changePage" /></div>
  </section>

  <el-dialog v-model="quoteOpen" title="生成客户报价" width="min(760px, 96vw)" :close-on-click-modal="false" :close-on-press-escape="!quoteSubmitting" :show-close="!quoteSubmitting">
    <el-alert title="只调用阶段 9a 的统一生成接口；报价版本、floor 和数据域均由服务端决定。" type="info" show-icon :closable="false" />
    <el-form class="dialog-form" label-position="top" :disabled="quoteSubmitting"><div class="form-grid"><el-form-item label="客户 *"><el-select v-model="quoteForm.customerId" filterable remote clearable :remote-method="searchQuoteCustomers" :loading="quoteCustomerLoading" placeholder="搜索客户名称" @visible-change="(open: boolean) => { if (open && !quoteCustomers.length) searchQuoteCustomers(); }" @change="rememberQuoteCustomer"><el-option v-for="customer in quoteCustomerOptions" :key="customer.id" :label="`${customer.legalName} · ${customer.levelCode} · ID ${customer.id}`" :value="customer.id" /></el-select></el-form-item><el-form-item label="报价类型 *"><el-select v-model="quoteForm.quoteType" @change="changeQuoteType"><el-option v-for="option in quoteTypeOptions" :key="option.value" :label="option.label" :value="option.value" /></el-select></el-form-item></div><el-alert v-if="quoteCustomerError" :title="quoteCustomerError" type="error" :closable="false"><el-button text @click="searchQuoteCustomers(quoteCustomerKeyword)">重试查询客户</el-button></el-alert><p v-else-if="!quoteCustomerLoading && !quoteCustomers.length" class="muted">没有匹配客户，可换关键字查询。</p><el-button v-if="quoteCustomers.length < quoteCustomerTotal" text :loading="quoteCustomerLoading" @click="searchQuoteCustomers(quoteCustomerKeyword, true)">加载更多客户</el-button><p class="muted">{{ quoteHint }} <span v-if="quoteForm.quoteType === 'APPLY'">当前客户等级：{{ chosenCustomerLevel || '未选择客户' }}。</span></p><el-alert v-if="quoteContextError" :title="quoteContextError" type="error" :closable="false"><el-button text @click="loadQuoteContext(quoteForm.customerId)">重试加载报价信息</el-button></el-alert><p v-else-if="quoteContextLoading" class="muted">正在加载该客户的价目表和历史报价…</p><el-form-item v-if="quoteForm.quoteType === 'CLONE'" label="来源报价 *"><el-select v-model="quoteForm.sourceQuoteId" filterable clearable placeholder="选择该客户的历史报价"><el-option v-for="item in quoteContext?.history.list || []" :key="item.id" :label="`报价 #${item.id} · V${item.versionNo} · ${item.quoteType} · ${item.status}`" :value="item.id" /></el-select><p v-if="quoteContext && !quoteContext.history.list.length" class="muted">该客户暂无可克隆的历史报价。</p></el-form-item><el-alert v-if="quoteForm.quoteType === 'APPLY' && quoteContext && !quoteContext.priceBook" title="该客户等级暂无当前生效价目表。" type="warning" :closable="false" /><div v-if="quoteForm.quoteType === 'APPLY' && quoteContext?.priceBook" class="context-preview"><b>将套用价目表 #{{ quoteContext.priceBook.id }} · V{{ quoteContext.priceBook.versionNo }}</b><span class="muted">，共 {{ quoteContext.priceBook.items.length }} 个 SKU</span></div><div v-else-if="quoteForm.quoteType === 'CLONE' && selectedHistoryQuote" class="context-preview"><b>将克隆报价 #{{ selectedHistoryQuote.id }} · V{{ selectedHistoryQuote.versionNo }}</b><span class="muted">，共 {{ selectedHistoryQuote.items.length }} 个 SKU</span></div><el-table v-if="contextPreviewItems.length" class="context-table" :data="contextPreviewItems" border max-height="220"><el-table-column prop="skuCode" label="SKU" min-width="180" /><el-table-column prop="skuId" label="SKU ID" width="100" /><el-table-column prop="unitPrice" label="单价" min-width="120" /><el-table-column prop="currency" label="币种" width="90" /></el-table><template v-if="quoteForm.quoteType === 'TEMP'"><el-form-item label="有效期至 *"><el-date-picker v-model="quoteForm.validTo" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" placeholder="选择临时报价到期时间" /></el-form-item><div class="section-title"><h3>SKU 报价</h3><el-button @click="addQuoteItem">添加 SKU</el-button></div><el-table :data="quoteForm.items" border><el-table-column label="SKU ID *" min-width="180"><template #default="{ row }"><el-select v-if="canReadSkus" v-model="row.skuId" filterable remote clearable :remote-method="(value: string) => { skuKeyword = value; searchSkus(value); }" :loading="skuLoading" @visible-change="(open: boolean) => { if (open && !skuChoices.length) searchSkus(); }" @change="rememberSku"><el-option v-for="sku in [...skuChoices, ...selectedSkus.values()]" :key="String(sku.id)" :label="skuLabel(String(sku.id))" :value="String(sku.id)" /></el-select><el-input v-else v-model="row.skuId" inputmode="numeric" /></template></el-table-column><el-table-column label="客户单价 *" min-width="220"><template #default="{ row }"><el-input v-model="row.unitPrice" placeholder="普通十进制，最多 8 位小数" /></template></el-table-column><el-table-column label="操作" width="80"><template #default="{ $index }"><el-button link type="danger" @click="quoteForm.items.splice($index, 1)">移除</el-button></template></el-table-column></el-table><p class="muted">本次报价 SKU：{{ quoteForm.items.map(item => skuLabel(item.skuId)).join("；") }}</p><el-alert v-if="skuError" :title="skuError" type="error" :closable="false"><el-button @click="searchSkus(skuKeyword)">重试</el-button></el-alert><el-button v-if="canReadSkus && skuChoices.length < skuTotal" text :loading="skuLoading" @click="searchSkus(skuKeyword, true)">加载更多 SKU</el-button></template><el-alert v-if="quoteError" class="form-error" :title="quoteError" type="error" show-icon :closable="false" /></el-form>
    <template #footer><el-button :disabled="quoteSubmitting" @click="quoteOpen = false">取消</el-button><el-button type="primary" :disabled="quoteSubmitting" @click="openQuotePreview">预览确认信息</el-button></template>
  </el-dialog>

  <el-dialog v-model="quotePreviewOpen" title="报价提交前确认" width="min(680px, 96vw)" append-to-body :close-on-click-modal="false" :close-on-press-escape="!quoteSubmitting" :show-close="!quoteSubmitting">
    <template v-if="quotePreview">
      <el-alert title="此处仅核对提交内容，不是后端计算后的报价单；价格、版本和 floor 校验以生成结果为准。" type="info" show-icon :closable="false" />
      <el-descriptions class="quote-preview" :column="1" border>
        <el-descriptions-item label="客户">{{ quotePreview.customerName }} · ID {{ quotePreview.draft.customerId }}</el-descriptions-item>
        <el-descriptions-item label="客户等级">{{ quotePreview.levelCode }}</el-descriptions-item>
        <el-descriptions-item label="报价类型">{{ quotePreview.typeLabel }}</el-descriptions-item>
        <el-descriptions-item v-if="quotePreview.draft.sourceQuoteId" label="来源报价 ID">{{ quotePreview.draft.sourceQuoteId }}</el-descriptions-item>
        <el-descriptions-item v-if="quotePreview.draft.validTo" label="临时报价有效期至">{{ formatDateTime(quotePreview.draft.validTo, '未设置', false) }}</el-descriptions-item>
      </el-descriptions>
      <p class="muted">{{ quotePreview.hint }}</p>
      <el-table v-if="quotePreview.items.length" :data="quotePreview.items" border>
        <el-table-column prop="label" label="SKU" min-width="260" />
        <el-table-column prop="unitPrice" label="预览单价" min-width="160" />
      </el-table>
      <el-alert v-if="quoteError" class="form-error" :title="quoteError" type="error" show-icon :closable="false" />
    </template>
    <template #footer><el-button :disabled="quoteSubmitting" @click="quotePreviewOpen = false">返回修改</el-button><el-button type="primary" :loading="quoteSubmitting" @click="generateQuote">确认生成</el-button></template>
  </el-dialog>

  <el-dialog v-model="quoteActionOpen" title="客户报价后续操作" width="min(620px, 96vw)" :close-on-click-modal="false" :show-close="!quoteActionSubmitting">
    <el-alert title="特价、刷新和导出均直接调用阶段 9b 真实接口；服务端负责权限、状态机和幂等。" type="info" show-icon :closable="false" />
    <el-form class="dialog-form" label-position="top" :disabled="quoteActionSubmitting"><el-form-item label="报价 ID *"><el-input v-model="quoteActionForm.quoteId" inputmode="numeric" /></el-form-item><el-form-item label="原因"><el-input v-model="quoteActionForm.reason" type="textarea" :rows="3" maxlength="300" show-word-limit /></el-form-item><el-form-item label="期望毛利（申请特价时必填）"><el-input v-model="quoteActionForm.expectedMargin" placeholder="例如 0.08" /></el-form-item><el-alert v-if="quoteActionError" class="form-error" :title="quoteActionError" type="error" show-icon :closable="false" /></el-form>
    <template #footer><el-button :loading="quoteActionSubmitting" @click="exportQuote">导出 XLSX</el-button><el-button :loading="quoteActionSubmitting" @click="refreshQuote">按最新成本刷新</el-button><el-button type="primary" :loading="quoteActionSubmitting" @click="requestSpecialPrice">申请特价</el-button></template>
  </el-dialog>

  <el-dialog v-model="transferOpen" title="移交客户" width="min(680px, 96vw)" :close-on-click-modal="false" :close-on-press-escape="!transferSubmitting" :show-close="!transferSubmitting">
    <p v-if="transferCustomer">客户：<b>{{ transferCustomer.legalName }}</b> · 当前归属 {{ transferCustomer.ownerSalesName }}</p>
    <el-form class="dialog-form" label-position="top" :disabled="transferSubmitting"><el-form-item label="目标销售人员 ID *"><el-input v-model="transferForm.toOperatorId" inputmode="numeric" @input="invalidateTransferPreview" /></el-form-item><el-form-item label="移交原因"><el-input v-model="transferForm.reason" type="textarea" :rows="3" maxlength="200" show-word-limit @input="invalidateTransferPreview" /></el-form-item></el-form>
    <el-descriptions v-if="transferImpact" :column="2" border><el-descriptions-item label="原归属">{{ transferImpact.fromOperatorName }}（{{ transferImpact.fromOperatorId }}）</el-descriptions-item><el-descriptions-item label="新归属">{{ transferImpact.toOperatorName }}（{{ transferImpact.toOperatorId }}）</el-descriptions-item><el-descriptions-item label="随迁报价">{{ transferImpact.quoteCount }} 份</el-descriptions-item><el-descriptions-item label="关联价目表">{{ transferImpact.priceBookCount }} 条</el-descriptions-item></el-descriptions>
    <el-alert v-if="transferError" class="form-error" :title="transferError" type="error" show-icon :closable="false" />
    <template #footer><el-button :disabled="transferSubmitting" @click="transferOpen = false">取消</el-button><el-button v-if="!transferImpact" type="primary" :loading="transferSubmitting" @click="previewTransfer">检查影响</el-button><el-button v-else type="danger" :loading="transferSubmitting" @click="confirmTransfer">确认移交</el-button></template>
  </el-dialog>
</template>

<style scoped>
.customer-panel { padding: 22px; }.section-title,.header-actions { display: flex; align-items: center; justify-content: space-between; gap: 12px; }.section-title h2,.section-title h3 { margin: 0; }.section-title p { margin: 5px 0 0; }.customer-filters { grid-template-columns: minmax(260px, 1fr) auto auto; margin: 18px 0; }.customer-list { min-height: 300px; }.dialog-form { margin-top: 18px; }.form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }.context-preview { margin: 14px 0 8px; }.context-table { margin-bottom: 16px; }.quote-preview { margin-top: 16px; }.form-error { margin-top: 18px; }@media (max-width: 760px) { .customer-filters,.form-grid { grid-template-columns: 1fr; }.section-title { align-items: flex-start; flex-direction: column; } }
</style>
