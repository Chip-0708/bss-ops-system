<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { modelsApi } from "./api/models";
import { capabilityLabels, mockModelTypes, modelContractId, toModelListItem, validateModelSkuRequest } from "./api/models.catalog";
import { ApiError } from "./domain/common";
import { formatDateTime } from "./domain/date";
import { MODEL_STATUS_LABELS as statuses, MODEL_VERIFY_STATUS_LABELS, getModelStatusType } from "./domain/status";
import type { ModelStatus } from "./domain/status";
import { usePermissionStore } from "./stores/permission";
import { PERMISSIONS } from "./domain/permissions";
import type {
  Model,
  ModelListItem,
  ModelOptionsDTO,
  CreateModelSkuRequest,
  UpdateModelSkuRequest,
  ModelCapabilityDTO,
  ModelDeprecationImpactDTO,
  DeprecateModelResponseDTO,
  BatchResult,
  ModelSuggestionDTO,
} from "./api/models.types";

const permissions = usePermissionStore();
const canPublish = computed(() => permissions.canAction(PERMISSIONS.MODEL_EDIT));

const rows = ref<ModelListItem[]>([]),
  loading = ref(false),
  busy = ref(false),
  error = ref("");
const selected = ref<string[]>([]),
  expanded = ref<string[]>([]);
const filters = reactive({
  search: "",
  vendor: "",
  type: "",
  status: "" as ModelStatus | "",
  family: "",
  tier: "",
});
const view = ref<"family" | "sku">("family");
const loadedView = ref<"family" | "sku">("sku");
const options = ref<ModelOptionsDTO>({ vendors: [], families: [] });
const optionsError = ref("");
const aliasesUnavailable = ref(false);
const serverGroups = ref<Array<{ key: string; name: string; items: ModelListItem[]; skuCount: number }>>([]);
watch(filters, () => {
  selected.value = [];
});
const isEditor = computed(() => permissions.canAction(PERMISSIONS.MODEL_EDIT));
const pagination = reactive({ page: 1, size: 20, total: 0 });
const filtered = computed(() => rows.value);
const groups = computed(() => loadedView.value === "family" ? serverGroups.value
  : rows.value.length ? [{ key: "sku", name: "SKU 列表", items: rows.value, skuCount: rows.value.length }] : []);
const vendors = computed(() => [...new Set(rows.value.map((m) => m.vendor))]);
const editable = (m: ModelListItem | Model) =>
  isEditor.value && !["OFFLINE", "DEPRECATING"].includes(m.status);
const tagType = getModelStatusType;
const message = (e: unknown) =>
  e instanceof ApiError
    ? `${e.httpStatus || "网络"} · ${e.message}`
    : e instanceof Error
      ? e.message
      : "操作失败，请重试";
let loadSequence = 0;
async function load() {
  const sequence = ++loadSequence;
  const requestedView = view.value;
  loading.value = true;
  error.value = "";
  try {
    const result = await modelsApi.list({
      view: requestedView,
      page: pagination.page,
      size: pagination.size,
      keyword: filters.search.trim() || undefined,
      vendor_id: filters.vendor || undefined,
      family_id: filters.family || undefined,
      model_type: filters.type || undefined,
      lifecycle_status: filters.status || undefined,
      tier_tag: filters.tier || undefined,
    });
    if (sequence !== loadSequence) return;
    loadedView.value = requestedView;
    aliasesUnavailable.value = result.list.some((item) =>
      "sku_code" in item && item.aliases === null,
    );
    serverGroups.value = [];
    if (requestedView === "family") {
      serverGroups.value = result.list.map((item) => {
        if (!("children" in item)) throw new Error("系列视图响应格式不正确。");
        return { key: modelContractId(item.family_id), name: `${item.vendor_name} / ${item.family_name}`,
          skuCount: item.sku_count, items: item.children.map(toModelListItem) };
      });
      rows.value = serverGroups.value.flatMap((group) => group.items);
    } else {
      rows.value = result.list.map((item) => {
        if (!("sku_code" in item)) throw new Error("SKU 视图响应格式不正确。");
        return toModelListItem(item);
      });
    }
    pagination.total = result.total;
    selected.value = [];
    expanded.value = groups.value.map((group) => group.key);
  } catch (e) {
    if (sequence !== loadSequence) return;
    rows.value = [];
    serverGroups.value = [];
    pagination.total = 0;
    error.value = message(e);
  } finally {
    if (sequence === loadSequence) loading.value = false;
  }
}
async function initialize() {
  loading.value = true;
  error.value = "";
  optionsError.value = "";
  try { options.value = await modelsApi.getOptions(); }
  catch { options.value = { vendors: [], families: [] }; optionsError.value = "厂商与系列选项接口尚未接通，新建模型和厂商/系列筛选暂不可用。"; }
  await load();
}
onMounted(initialize);
watch(() => filters.vendor, () => { filters.family = ""; });
watch(view, () => { pagination.page = 1; selected.value = []; void load(); });
const filterFamilies = computed(() => options.value.families.filter((item) => !filters.vendor || item.vendor_id === filters.vendor));
async function act(work: () => Promise<unknown>, success = "操作成功") {
  if (busy.value) return;
  busy.value = true;
  try {
    await work();
    ElMessage.success(success);
    await load();
  } catch (e) {
    ElMessage.error(message(e));
  } finally {
    busy.value = false;
  }
}
function resetFilters() {
  Object.assign(filters, {
    search: "",
    vendor: "",
    type: "",
    status: "",
    family: "",
    tier: "",
  });
  pagination.page = 1;
  void load();
}
function applyFilters() {
  pagination.page = 1;
  selected.value = [];
  void load();
}
function changePage(page: number) {
  pagination.page = page;
  selected.value = [];
  void load();
}
function changePageSize(size: number) {
  pagination.size = size;
  pagination.page = 1;
  selected.value = [];
  void load();
}
function toggle(id: string) {
  selected.value = selected.value.includes(id)
    ? selected.value.filter((x) => x !== id)
    : [...selected.value, id];
}
function toggleAll() {
  selected.value =
    selected.value.length === filtered.value.length
      ? []
      : filtered.value.map((m) => m.id);
}

const detail = ref<ModelListItem>(),
  detailOpen = ref(false),
  detailError = ref("");
const detailId = ref("");
function showDetail(m: { id: string }) {
  detailId.value = m.id;
  detailOpen.value = true;
  detail.value = rows.value.find(row => row.id === m.id);
  detailError.value = detail.value ? "" : "当前列表已变化，请刷新后重新选择 SKU。";
}
async function refreshDetail() { await load(); showDetail({ id: detailId.value }); }
const editOpen = ref(false),
  editingId = ref(""),
  formError = ref("");
interface ModelSkuForm {
  sku_code: string; vendor_id: string; family_id: string; model_type: string;
  native_currency: string; context_window: number | null; capability: ModelCapabilityDTO;
  tier_tag: string; is_sensitive: boolean; cross_border: boolean;
}
const formSubmitting = ref(false);
const capabilityChanged = ref(false);
const capabilityProvided = ref(true);
const originalContext = ref<number | null>(null);
const blank = (): ModelSkuForm => ({
  sku_code: "", vendor_id: "", family_id: "", model_type: "对话",
  native_currency: "USD", context_window: 128000, capability: {},
  tier_tag: "主力", is_sensitive: false, cross_border: false,
});
const form = reactive<ModelSkuForm>(blank());
const familyOptions = computed(() => options.value.families.filter((item) => item.vendor_id === form.vendor_id));
function chooseVendor() { form.family_id = familyOptions.value[0]?.id || ""; }
function edit(row?: ModelListItem) {
  if (busy.value || loading.value || formSubmitting.value) return;
  Object.assign(form, row ? {
    sku_code: row.code, vendor_id: row.vendorId, family_id: row.familyId,
    model_type: row.type, native_currency: row.currency, context_window: row.context,
    capability: { ...(row.capability ?? {}) }, tier_tag: row.tier ?? "",
    is_sensitive: row.sensitive, cross_border: row.crossBorder,
  } : blank());
  editingId.value = row?.id || "";
  capabilityProvided.value = row ? row.capability !== null : true;
  capabilityChanged.value = false;
  originalContext.value = row?.context ?? null;
  formError.value = "";
  editOpen.value = true;
}
async function save() {
  if (!isEditor.value || busy.value || formSubmitting.value) return;
  formError.value = "";
  const capability = Object.fromEntries(Object.entries(form.capability).filter(([, value]) => value !== undefined)) as ModelCapabilityDTO;
  const request: UpdateModelSkuRequest = {
    sku_code: form.sku_code.trim(), model_type: form.model_type, native_currency: form.native_currency,
    ...(form.context_window == null ? {} : { context_window: form.context_window }),
    ...(capabilityProvided.value || capabilityChanged.value ? { capability } : {}),
    ...(form.tier_tag ? { tier_tag: form.tier_tag } : {}),
    is_sensitive: form.is_sensitive, cross_border: form.cross_border,
  };
  const createRequest: CreateModelSkuRequest = { ...request, vendor_id: form.vendor_id, family_id: form.family_id };
  try {
    if (!options.value.families.some((item) => item.id === form.family_id && item.vendor_id === form.vendor_id)) throw new Error("请选择厂商及其所属系列。");
    if (editingId.value && originalContext.value !== null && form.context_window == null) throw new Error("当前契约未定义清空上下文，请填写非负整数。");
    validateModelSkuRequest(editingId.value ? request : createRequest, !!editingId.value);
  } catch (e) {
    formError.value = e instanceof Error ? e.message : "请检查模型输入。";
    return;
  }
  const id = editingId.value;
  formSubmitting.value = true;
  try {
    await ElMessageBox.confirm(`确认保存 SKU「${request.sku_code}」？${id ? "不会直接修改生命周期状态。" : "新建后为草稿，仍需提交验证。"}`, "确认保存模型", { confirmButtonText: "确认保存", cancelButtonText: "返回检查" });
    await act(async () => {
      if (id) await modelsApi.update(id, request);
      else await modelsApi.create(createRequest);
      editOpen.value = false;
    }, "模型档案已保存");
  } catch (e) {
    if (e !== "cancel" && e !== "close") formError.value = message(e);
  } finally { formSubmitting.value = false; }
}

const verifyOpen = ref(false),
  target = ref<ModelListItem | Model>(),
  verification = reactive({ result: "PASS", note: "" });
const verificationError = ref("");
function verify(m: ModelListItem | Model) {
  if (!isEditor.value || busy.value || m.status !== "PENDING_VERIFY") return;
  target.value = m;
  verificationError.value = "";
  verification.note = "";
  verification.result = "PASS";
  verifyOpen.value = true;
}
async function saveVerify() {
  if (!isEditor.value || busy.value || !target.value) return;
  const current = target.value;
  const body = { result: verification.result as "PASS" | "FAIL", note: verification.note.trim() };
  verificationError.value = "";
  if (current.status !== "PENDING_VERIFY" || !["PASS", "FAIL"].includes(body.result) || !body.note || body.note.length > 1000) {
    verificationError.value = "仅待验证模型可操作；请选择结果并填写 1~1000 字的说明（Mock 暂定限制）。";
    return;
  }
  busy.value = true;
  try {
    await ElMessageBox.confirm(`模型：${current.code}；当前状态：${statuses[current.status]}；结果：${body.result === 'PASS' ? '通过' : '不通过'}。${body.result === 'PASS' ? 'Mock 将转为可采购，不会直接上架。' : 'Mock 保留待验证状态。'}\n说明：${body.note}`, "确认人工验证", { type: "warning" });
  } catch (error) {
    if (error !== "cancel" && error !== "close") verificationError.value = message(error);
    return;
  } finally { busy.value = false; }
  await act(async () => {
    try { await modelsApi.verify(current.id, body); }
    catch (error) { verificationError.value = message(error); throw error; }
    verifyOpen.value = false;
  }, "人工验证已记录");
}

const aliasesOpen = ref(false),
  alias = ref(""),
  suggestions = ref<ModelSuggestionDTO[]>([]);
function aliases(m: ModelListItem | Model) {
  target.value = m;
  alias.value = m.name;
  suggestions.value = [];
  aliasesOpen.value = true;
}
async function changeAlias(value: string, remove = false) {
  if (!isEditor.value || busy.value || !target.value) return;
  const current = target.value;
  const name = value.trim();
  if (!remove && (!name || name.length > 128 || current.aliases.some(item => item.toLowerCase() === name.toLowerCase()))) {
    ElMessage.warning("别名须为 1~128 个字符，且不能与已有别名重复。");
    return;
  }
  const aliases = remove ? current.aliases.filter(item => item !== value) : [...current.aliases, name];
  busy.value = true;
  try {
    await ElMessageBox.confirm(`${remove ? '移除' : '添加'}模型「${current.code}」的别名「${name}」？`, "确认维护别名");
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(message(error));
    return;
  } finally { busy.value = false; }
  await act(
    async () => {
      await modelsApi.setAliases(current.id, aliases);
      target.value = { ...current, aliases };
      alias.value = "";
      suggestions.value = [];
    },
    remove ? "别名已移除" : "别名已添加",
  );
}
async function suggest() {
  if (busy.value || !target.value || !alias.value.trim()) return;
  suggestions.value = [];
  busy.value = true;
  try {
    suggestions.value = await modelsApi.suggestions(
      target.value!.id,
      alias.value,
    );
  } catch (e) {
    ElMessage.error(message(e));
  } finally {
    busy.value = false;
  }
}
async function merge(destination: ModelSuggestionDTO) {
  if (!isEditor.value || busy.value || !target.value) return;
  const source = target.value;
  const name = alias.value.trim();
  if (!name || name.length > 128 || source.id === destination.id) {
    ElMessage.warning("请选择不同的来源和目标，别名长度为 1~128 个字符。");
    return;
  }
  busy.value = true;
  try {
    await ElMessageBox.confirm(
      `来源：${source.code}；保留目标：${destination.code}；别名：${name}。当前 Mock 仅登记别名，不删除来源或改变生命周期。`,
      "确认别名归属",
      { type: "warning" },
    );
  } catch (error) {
    busy.value = false;
    if (error !== "cancel" && error !== "close") ElMessage.error(message(error));
    return;
  }
  busy.value = false;
  await act(async () => {
      await modelsApi.mergeAlias(destination.id, source.id, name);
      aliasesOpen.value = false;
    }, "新名称已合并为别名");
}

const batchOpen = ref(false),
  batchAction = ref("tier"),
  batchValue = ref("主力"),
  batchResults = ref<BatchResult[]>([]);
function openBatch() {
  batchAction.value = "tier";
  batchValue.value = "主力";
  batchResults.value = [];
  batchOpen.value = true;
}
async function batch() {
  if (!isEditor.value || busy.value) return;
  await act(async () => {
    const codes = new Map(rows.value.map((model) => [model.id, model.code]));
    const results = await modelsApi.batch({
      ids: [...selected.value],
      action: batchAction.value,
      value: batchValue.value,
    });
    batchResults.value = results.map((item) => ({ ...item, code: codes.get(item.id) || item.id }));
  }, "批量操作已处理，请查看逐项结果");
}
async function publish(m: ModelListItem | Model) {
  if (busy.value || !canPublish.value || m.status !== "PURCHASABLE") return;
  const snapshot = { id: m.id, code: m.code, status: m.status };
  busy.value = true;
  try {
    await ElMessageBox.confirm(`模型：${snapshot.code}；当前状态：${statuses[snapshot.status]}。确认上架？是否合法由接口重新校验。`, "模型上架", { type: "warning" });
  } catch (error) {
    if (error !== "cancel" && error !== "close") ElMessage.error(message(error));
    return;
  } finally { busy.value = false; }
  await act(async () => {
    await modelsApi.publish(snapshot.id);
  }, "上架请求已完成");
}
function exportCsv() {
  const data = filtered.value.filter(
    (m) => !selected.value.length || selected.value.includes(m.id),
  );
  const headers = [
    "SKU",
    "厂商",
    "系列",
    "状态",
    "分级",
    "币种",
    "验证状态",
  ];
  const cell = (v: unknown) =>
    `"${String(v ?? "")
      .replace(/^[=+@-]/, "'$&")
      .replaceAll('"', '""')}"`;
  const content = [
    headers,
    ...data.map((m) => [
      m.code,
      m.vendor,
      m.family,
      statuses[m.status],
      m.tier,
      m.currency,
      m.verifyStatus,
    ]),
  ]
    .map((r) => r.map(cell).join(","))
    .join("\r\n");
  const url = URL.createObjectURL(
    new Blob(["\ufeff", content], { type: "text/csv;charset=utf-8" }),
  );
  const a = document.createElement("a");
  a.href = url;
  a.download = "模型清单.csv";
  a.click();
  URL.revokeObjectURL(url);
}

const retireOpen = ref(false),
  impact = ref<ModelDeprecationImpactDTO>(),
  retireResult = ref<DeprecateModelResponseDTO>(),
  retireForm = reactive({ offlineAt: "", reason: "", replacementId: "" }),
  impactLoading = ref(false);
const retireError = ref("");
let impactSequence = 0;
function retire(m: ModelListItem | Model) {
  if (!isEditor.value || busy.value || impactLoading.value || m.status !== "PUBLISHED") return;
  ++impactSequence;
  retireError.value = "";
  target.value = m;
  impact.value = undefined;
  retireResult.value = undefined;
  retireForm.offlineAt = "";
  retireForm.reason = "";
  retireForm.replacementId = "";
  retireOpen.value = true;
}
async function loadImpact() {
  if (busy.value || impactLoading.value || !target.value) return;
  const modelId = target.value.id, sequence = ++impactSequence;
  impactLoading.value = true;
  impact.value = undefined;
  retireError.value = "";
  try {
    const report = await modelsApi.getDeprecationImpact(modelId);
    if (sequence !== impactSequence) return;
    if (modelContractId(report.sku_id) !== modelId || !report.snapshot_id ||
        !Number.isFinite(Date.parse(report.generated_at)) || !report.references ||
        !Array.isArray(report.references.price_books) || !Array.isArray(report.references.contracts) ||
        !Array.isArray(report.references.customer_quotes) || !Array.isArray(report.replacements))
      throw new Error("影响报告与模型不一致或内容异常，请重新加载。");
    impact.value = report;
  } catch (e) {
    if (sequence === impactSequence) retireError.value = message(e);
  } finally {
    if (sequence === impactSequence) impactLoading.value = false;
  }
}
async function submitRetire() {
  if (!isEditor.value || busy.value || impactLoading.value || retireResult.value || !target.value) return;
  const current = target.value, report = impact.value;
  const body = { sunset_date: retireForm.offlineAt, reason: retireForm.reason.trim(), impact_snapshot_id: report?.snapshot_id || "", replacement_sku_id: retireForm.replacementId || undefined };
  retireError.value = "";
  const date = Date.parse(`${body.sunset_date}T00:00:00+08:00`);
  if (!report || modelContractId(report.sku_id) !== current.id || current.status !== "PUBLISHED") {
    retireError.value = "请先加载当前已上架模型的有效影响报告。";
    return;
  }
  if (!/^\d{4}-\d{2}-\d{2}$/.test(body.sunset_date) || !Number.isFinite(date) ||
      new Date(date + 8 * 3600000).toISOString().slice(0, 10) !== body.sunset_date ||
      date <= Date.now()) {
    retireError.value = "下线日须晚于今天；通知期及例外以接口校验为准。";
    return;
  }
  if (!body.reason || Array.from(body.reason).length > 500) {
    retireError.value = "请填写1~500字的退役理由。";
    return;
  }
  busy.value = true;
  try {
    await ElMessageBox.confirm(`模型：${current.code}；下线日期：${body.sunset_date}；影响报告：${report.snapshot_id}。\n理由：${body.reason}\n本次仅提交审批申请，不执行下线。`, "确认退役申请", { type: "warning" });
  } catch (error) {
    if (error !== "cancel" && error !== "close") retireError.value = message(error);
    return;
  } finally { busy.value = false; }
  await act(async () => {
    try {
      retireResult.value = await modelsApi.deprecate(current.id, body);
      if (retireResult.value.lifecycle_status !== "PUBLISHED")
        ElMessage.warning("申请已受理，但接口返回的模型状态与审批前保持已上架的约定不一致，请核对，勿重复提交。");
    }
    catch (error) { retireError.value = message(error); throw error; }
  }, "退役申请已受理，请核对最新模型状态");
}
</script>

<template>
  <section class="page-heading">
    <div>
      <div class="eyebrow">MODEL CATALOG</div>
      <h1>模型管理</h1>
      <p>统一维护模型档案、能力与生命周期，让每次变更有据可循。</p>
    </div>
    <el-button v-if="isEditor" type="primary" size="large" :disabled="!!optionsError" @click="edit()"
      >＋ 新建模型</el-button
    >
  </section>
  <el-alert v-if="optionsError" :title="optionsError" type="warning" show-icon :closable="false" />
  <el-alert v-if="aliasesUnavailable" title="后端列表未返回别名，当前仅展示 SKU 基础信息；别名请勿以此列表核对。" type="info" show-icon :closable="false" />
  <div class="stats">
    <div>
      <span>{{ loadedView === 'family' ? '匹配系列' : '匹配 SKU' }}</span
      ><strong>{{ pagination.total }}<small>{{ loadedView === 'family' ? '个系列' : '个 SKU' }}</small></strong>
    </div>
    <div>
      <span>已上架</span
      ><strong
        >{{ rows.filter((m) => m.status === "PUBLISHED").length
        }}<small class="green">本页可售</small></strong
      >
    </div>
    <div>
      <span>待验证</span
      ><strong
        >{{ rows.filter((m) => m.status === "PENDING_VERIFY").length
        }}<small>本页待验证</small></strong
      >
    </div>
    <div>
      <span>本页厂商</span
      ><strong>{{ vendors.length }}<small>个来源</small></strong>
    </div>
  </div>
  <section class="catalog panel">
    <div class="section-title">
      <h2>
        模型目录 <span>{{ pagination.total }}</span>
      </h2>
      <div><el-radio-group v-model="view" aria-label="模型列表视图"><el-radio-button value="family">系列视图</el-radio-button><el-radio-button value="sku">SKU 视图</el-radio-button></el-radio-group> <el-button @click="initialize" :loading="loading">刷新列表</el-button></div>
    </div>
    <div class="filters">
      <el-input
        v-model="filters.search"
        placeholder="搜索型号、名称或别名"
        maxlength="64"
        clearable
        aria-label="搜索型号"
      /><el-select
        v-model="filters.vendor"
        :disabled="!!optionsError"
        placeholder="全部厂商"
        clearable
        aria-label="厂商筛选"
        ><el-option v-for="v in options.vendors" :key="v.id" :label="v.name" :value="v.id" /></el-select
      ><el-select v-model="filters.family" :disabled="!!optionsError" placeholder="全部系列" clearable aria-label="系列筛选"><el-option v-for="f in filterFamilies" :key="f.id" :label="f.name" :value="f.id" /></el-select
      ><el-select
        v-model="filters.type"
        placeholder="全部类型"
        clearable
        aria-label="类型筛选"
        ><el-option
            v-for="v in mockModelTypes"
          :key="v"
          :value="v" /></el-select
      ><el-select
        v-model="filters.status"
        placeholder="全部状态"
        clearable
        aria-label="状态筛选"
        ><el-option
          v-for="(name, key) in statuses"
          :key="key"
          :value="key"
          :label="name" /></el-select
      ><el-select
        v-model="filters.tier"
        placeholder="全部分级"
        clearable
        aria-label="分级筛选"
        ><el-option
          v-for="v in ['旗舰', '主力', '经济', '长尾']"
          :key="v"
          :value="v" /></el-select
      ><el-button type="primary" @click="applyFilters">查询</el-button
      ><el-button text @click="resetFilters">重置</el-button>
    </div>
    <div class="list-toolbar">
      <div>
        <el-checkbox
          :model-value="
            !!filtered.length && selected.length === filtered.length
          "
          :indeterminate="
            selected.length > 0 && selected.length < filtered.length
          "
          @change="toggleAll"
          >选择当前结果</el-checkbox
        ><span class="muted">已选 {{ selected.length }} 项，单次批量最多 200 项</span>
      </div>
      <div>
        <el-button
          v-if="isEditor"
          :disabled="!selected.length || selected.length > 200 || busy"
          @click="openBatch"
          >批量操作</el-button
        ><el-button :disabled="!filtered.length" @click="exportCsv"
          >导出 CSV</el-button
        >
      </div>
    </div>
    <el-alert
      v-if="error"
      :title="error"
      type="error"
      show-icon
      :closable="false"
      ><el-button text @click="initialize">重新加载</el-button></el-alert
    >
    <div v-loading="loading" class="list-body">
      <el-empty
        v-if="!groups.length && !loading && !error"
        description="没有匹配的模型，试试调整筛选条件"
      />
      <el-collapse v-model="expanded">
        <el-collapse-item
          v-for="group in groups"
          :key="group.key"
          :name="group.key"
          ><template #title
            ><span class="family-icon">◈</span><b>{{ group.name }}</b
            ><span class="family-count"
              >{{ group.skuCount }} 个型号</span
            ></template
          >
          <el-table :data="group.items" row-key="id" style="width: 100%"
            ><el-table-column width="45"
              ><template #default="{ row }"
                ><el-checkbox
                  :model-value="selected.includes(row.id)"
                  @change="toggle(row.id)"
                  :aria-label="`选择 ${row.code}`" /></template></el-table-column
            ><el-table-column label="型号 / SKU" min-width="205"
              ><template #default="{ row }"
                ><button class="model-link" @click="showDetail(row)">
                  {{ row.name }}
                </button>
                <div class="mono muted">{{ row.code }}</div></template
              ></el-table-column
            ><el-table-column label="能力" min-width="135"
              ><template #default="{ row }"
                ><span
                  >{{ row.type }} · {{ row.context == null ? '上下文未提供' : row.context.toLocaleString() }}</span
                >
                <div class="muted">
                  {{ row.capabilities.join(" / ") || (row.capabilitiesProvided ? '未声明扩展能力' : '能力未提供') }}
                </div></template
              ></el-table-column
            ><el-table-column label="原厂币种 / 分级" min-width="140"
              ><template #default="{ row }"
                ><div class="mono">
                  {{ row.currency }}
                </div>
                <div class="mono muted">
                  {{ row.tier || '未设置分级' }}
                </div></template
              ></el-table-column
            ><el-table-column label="验证状态" min-width="110"><template #default="{ row }">{{ MODEL_VERIFY_STATUS_LABELS[row.verifyStatus as keyof typeof MODEL_VERIFY_STATUS_LABELS] }}</template></el-table-column
            ><el-table-column label="状态" min-width="125"
              ><template #default="{ row }"
                ><el-tag :type="tagType(row.status)" effect="light">{{
                  statuses[row.status as keyof typeof statuses]
                }}</el-tag>
                </template
              ></el-table-column
            ><el-table-column label="操作" min-width="230"
              ><template #default="{ row }"
                ><div class="row-actions">
                  <el-button link @click="showDetail(row)">详情</el-button
                  ><el-button
                    v-if="editable(row)"
                    link
                    type="primary"
                    @click="edit(row)"
                    >编辑</el-button
                  ><el-button v-if="editable(row)" link @click="aliases(row)"
                    >别名</el-button
                  ><el-button
                    v-if="isEditor && row.status === 'PENDING_VERIFY'"
                    link
                    type="primary"
                    @click="verify(row)"
                    >人工验证</el-button
                  ><el-button
                    v-if="canPublish && row.status === 'PURCHASABLE'"
                    link
                    type="primary"
                    :disabled="busy"
                    @click="publish(row)"
                    >上架</el-button
                  ><el-button
                    v-if="isEditor && row.status === 'PUBLISHED'"
                    link
                    type="danger"
                    :disabled="busy"
                    @click="retire(row)"
                    >发起退役</el-button
                  >
                </div></template
              ></el-table-column
            ></el-table
          >
        </el-collapse-item>
      </el-collapse>
    </div>
    <div class="catalog-pagination">
      <el-pagination
        :current-page="pagination.page"
        :page-size="pagination.size"
        :total="pagination.total"
        :page-sizes="[5, 10, 20]"
        layout="total, sizes, prev, pager, next"
        @current-change="changePage"
        @size-change="changePageSize"
      />
    </div>
    <div class="catalog-footer">
      SKU 列表契约不包含官方价、成本、协议和验证历史，列表不补零或伪造。<span>系列视图按系列分页，SKU 视图按 SKU 分页；档案仅展示当前列表已有字段。</span>
    </div>
  </section>

  <el-drawer
    v-model="detailOpen"
    title="模型档案"
    size="min(560px, 95vw)"
    ><el-alert
      v-if="detailError"
      :title="detailError"
      type="error"
      show-icon
      :closable="false"
    /><el-button v-if="detailError" @click="refreshDetail">重新加载列表</el-button>
    <el-alert title="档案展示当前列表已返回的 SKU 字段；刷新会重新查询列表。" type="info" :closable="false" />
    <template v-if="detail"
      ><h2>{{ detail.name }}</h2>
      <p class="mono muted">{{ detail.code }}</p>
      <el-button @click="refreshDetail">刷新档案</el-button>
      <el-descriptions :column="1" border
        ><el-descriptions-item label="状态">{{
          statuses[detail.status]
        }}</el-descriptions-item
        ><el-descriptions-item label="厂商 / 系列"
          >{{ detail.vendor }} / {{ detail.family }}</el-descriptions-item
        ><el-descriptions-item label="能力"
          >{{ !detail.capabilitiesProvided ? '未提供' : detail.capabilities.join("、") || '未声明已支持能力' }} ·
          {{ detail.context == null ? '未提供' : detail.context.toLocaleString() }} 上下文</el-descriptions-item
        ><el-descriptions-item label="模型类型 / 原厂币种">{{ detail.type }} / {{ detail.currency }}</el-descriptions-item
        ><el-descriptions-item label="最大输出长度">{{ detail.capability?.max_output_tokens == null ? '未提供' : detail.capability.max_output_tokens.toLocaleString() }}</el-descriptions-item
        ><el-descriptions-item v-for="(label, key) in capabilityLabels" :key="key" :label="label">{{ detail.capability?.[key] === undefined ? '未声明' : detail.capability[key] ? '支持' : '不支持' }}</el-descriptions-item
        ><el-descriptions-item label="验证状态">{{ MODEL_VERIFY_STATUS_LABELS[detail.verifyStatus] }}</el-descriptions-item
        ><el-descriptions-item label="运营属性"
          >{{ detail.tier ?? '未分级' }} · 敏感 {{ detail.sensitive ? "是" : "否" }} · 出境
          {{ detail.crossBorder ? "是" : "否" }}</el-descriptions-item
        ><el-descriptions-item label="别名">{{
          detail.aliases.join("、") || "暂无"
        }}</el-descriptions-item></el-descriptions
      >
      </template
  ></el-drawer>

  <el-dialog
    v-model="editOpen"
    :title="editingId ? '编辑模型档案' : '新建模型'"
    width="720px"
    :close-on-click-modal="false"
    :close-on-press-escape="!formSubmitting"
    :show-close="!formSubmitting"
    ><el-alert
      title="按 SKU 契约保存；新建为草稿。厂商/系列只可选择，编辑时不可迁移；名称、协议和官方价格不在本请求中。模型类型选项仍为 Mock 暂定枚举。"
      type="info"
      :closable="false"
    /><el-form label-position="top" class="form-grid" :disabled="formSubmitting" @submit.prevent="save"
      ><el-form-item label="SKU 标识 *"
        ><el-input
          v-model="form.sku_code"
          maxlength="128"
          placeholder="例如 my-model-v1" /></el-form-item
      ><el-form-item label="厂商 *"
        ><el-select v-model="form.vendor_id" filterable :disabled="!!editingId" placeholder="选择厂商" @change="chooseVendor"><el-option v-for="v in options.vendors" :key="v.id" :label="v.name" :value="v.id" /></el-select></el-form-item
      ><el-form-item label="系列 *"
        ><el-select v-model="form.family_id" filterable :disabled="!!editingId || !form.vendor_id" placeholder="选择所属系列"><el-option v-for="v in familyOptions" :key="v.id" :label="v.name" :value="v.id" /></el-select></el-form-item
      ><el-form-item label="模型类型"
        ><el-select v-model="form.model_type"
          ><el-option
            v-for="v in mockModelTypes"
            :value="v"
            :key="v" /></el-select></el-form-item
      ><el-form-item label="原厂币种 *"><el-select v-model="form.native_currency"><el-option label="USD · 美元" value="USD" /><el-option label="CNY · 人民币" value="CNY" /></el-select></el-form-item
      ><el-form-item label="上下文长度（可选，非负整数）"
        ><el-input-number
          v-model="form.context_window"
          :min="0"
          :precision="0" /></el-form-item
      ><el-form-item label="能力开关"><div class="capability-options"><el-checkbox v-for="(label, key) in capabilityLabels" :key="key" v-model="form.capability[key]" @change="capabilityChanged = true">{{ label }}</el-checkbox></div></el-form-item
      ><el-form-item label="最大输出长度（可选）"><el-input-number v-model="form.capability.max_output_tokens" :min="0" :precision="0" @change="capabilityChanged = true" /></el-form-item
      ><el-form-item label="分级标签"
        ><el-select v-model="form.tier_tag"
          ><el-option
            v-for="v in ['旗舰', '主力', '经济', '长尾']"
            :value="v"
            :key="v" /></el-select></el-form-item
      ><el-form-item label="运营标记"
        ><el-checkbox v-model="form.is_sensitive">敏感</el-checkbox
        ><el-checkbox v-model="form.cross_border"
          >数据出境</el-checkbox
        ></el-form-item
      ></el-form
    ><el-alert
      v-if="formError"
      :title="formError"
      type="error"
      :closable="false"
    /><template #footer
      ><el-button :disabled="formSubmitting" @click="editOpen = false">取消</el-button
      ><el-button type="primary" :loading="busy || formSubmitting" @click="save"
        >保存档案</el-button
      ></template
    ></el-dialog
  >
  <el-dialog v-model="verifyOpen" title="记录人工验证" width="530px" :close-on-click-modal="!busy" :close-on-press-escape="!busy" :show-close="!busy"
    ><el-alert
      title="Mock：通过后转为可采购；不通过保留待验证。正式转换规则待确认。"
      type="info"
      :closable="false"
    /><p>模型：{{ target?.code }} · {{ target ? statuses[target.status] : '' }}</p><el-form label-position="top" :disabled="busy"
      ><el-form-item label="验证结果"
        ><el-radio-group v-model="verification.result"
          ><el-radio value="PASS">通过</el-radio
          ><el-radio value="FAIL">不通过</el-radio></el-radio-group
        ></el-form-item
      ><el-form-item label="验证说明 *"
        ><el-input
          v-model="verification.note"
          maxlength="1000"
          show-word-limit
          type="textarea"
          :rows="4" /></el-form-item></el-form
    ><el-alert v-if="verificationError" :title="verificationError" type="error" :closable="false" /><template #footer
      ><el-button :disabled="busy" @click="verifyOpen = false">取消</el-button
      ><el-button
        type="primary"
        :loading="busy"
        :disabled="!verification.note.trim()"
        @click="saveVerify"
        >保存验证记录</el-button
      ></template
    ></el-dialog
  >
  <el-dialog
    v-model="aliasesOpen"
    :close-on-click-modal="!busy"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    :title="`别名与查重 · ${target?.code || ''}`"
    width="660px"
    ><el-alert
      title="仅把新名称注册为别名，不删除或合并已有模型；候选排序为 Mock 示例。"
      type="info"
      :closable="false"
    />
    <div class="alias-tags">
      <el-tag
        v-for="a in target?.aliases"
        :key="a"
        :closable="!busy"
        @close="changeAlias(a, true)"
        >{{ a }}</el-tag
      ><span v-if="!target?.aliases.length" class="muted">暂无别名</span>
    </div>
    <div class="inline-form">
      <el-input
        v-model="alias"
        :disabled="busy"
        maxlength="128"
        placeholder="输入新别名或疑似重复名称"
      /><el-button :loading="busy" :disabled="!alias.trim()" @click="suggest"
        >查重 Top3</el-button
      ><el-button
        type="primary"
        :loading="busy"
        :disabled="!alias.trim()"
        @click="changeAlias(alias)"
        >添加到当前模型</el-button
      >
    </div>
    <p class="muted">当前模型为来源，查重候选为保留目标。Mock 仅登记未占用别名；若名称与已有 SKU 编码冲突，请勿通过改名绕过，等待正式合并规则确认。</p>
    <el-table v-if="suggestions.length" :data="suggestions"
      ><el-table-column prop="code" label="候选模型" /><el-table-column
        label="操作"
        ><template #default="{ row }"
          ><el-button
            link
            type="primary"
            :disabled="busy"
            @click="merge(row)"
            >以当前模型为来源，登记至此目标</el-button
          ></template
        ></el-table-column
      ></el-table
    ></el-dialog
  >
  <el-dialog v-model="batchOpen" title="批量操作" width="650px"
    ><el-alert
      title="仅演示草稿 → 待验证；禁止绕过独立上架、退役流程。结果按模型逐项返回。"
      :closable="false"
      type="info" />
    <div class="inline-form">
      <el-select
        v-model="batchAction"
        @change="
          batchValue = batchAction === 'tier' ? '主力' : 'PENDING_VERIFY'
        "
        ><el-option label="更新分级" value="tier" /><el-option
          label="提交待验证"
          value="status" /></el-select
      ><el-select v-if="batchAction === 'tier'" v-model="batchValue"
        ><el-option
          v-for="v in ['旗舰', '主力', '经济', '长尾']"
          :key="v"
          :value="v" /></el-select
      ><el-button
        type="primary"
        :disabled="!selected.length || !!batchResults.length"
        :loading="busy"
        @click="batch"
        >执行 {{ selected.length }} 项</el-button
      >
    </div>
    <el-table v-if="batchResults.length" :data="batchResults"
      ><el-table-column prop="code" label="型号" /><el-table-column label="结果"
        ><template #default="{ row }"
          ><el-tag :type="row.ok ? 'success' : 'danger'">{{
            row.ok ? "成功" : "未处理"
          }}</el-tag></template
        ></el-table-column
      ><el-table-column prop="reason" label="说明" min-width="240" /></el-table
  ></el-dialog>
  <el-dialog
    v-model="retireOpen"
    :title="`发起退役 · ${target?.code || ''}`"
    width="730px"
    :close-on-click-modal="false"
    :close-on-press-escape="!busy && !impactLoading"
    :show-close="!busy && !impactLoading"
    ><el-result
      v-if="retireResult"
      icon="success"
      title="已提交退役申请"
      sub-title="本次仅提交退役申请，不执行审批或下线；双人审批通过后才进入即将下线。"
      ><template #extra
        ><p class="mono">单号 {{ retireResult.approval_id }}</p>
        <el-button @click="retireOpen = false"
          >返回模型列表</el-button
        ></template
      ></el-result
    ><template v-else
      ><el-alert
        title="影响报告为必读材料。此阶段只提交申请，不执行审批或下线。"
        type="warning"
        :closable="false"
      /><el-button
        class="spaced"
        :loading="impactLoading"
        :disabled="busy"
        @click="loadImpact"
        >{{ impact ? "重新加载" : "加载" }}退役影响分析</el-button
      ><template v-if="impact"
        ><el-descriptions :column="1" border
          ><el-descriptions-item label="引用价目表">{{
            impact.references.price_books.map(item => item.name).join("、")
          }}</el-descriptions-item
          ><el-descriptions-item label="客户合同">{{
            impact.references.contracts.map(item => item.customer_name).join("、")
          }}</el-descriptions-item
          ><el-descriptions-item label="客户报价">{{
            impact.references.customer_quotes.map(item => `${item.id} · ${item.status}`).join("、")
          }}</el-descriptions-item
          ><el-descriptions-item label="替代建议">{{
            impact.replacements.map(item => `${item.sku_code} · ${item.reason}`).join("、") || "暂无候选，需人工确认"
          }}</el-descriptions-item></el-descriptions
        >
        <p class="muted">
          报告生成时间：{{ formatDateTime(impact.generated_at) }}。下线日须距通知任务发出日至少30天，由服务端校验；报告生成时间不是通知发出时间。
        </p></template
      ><el-alert v-if="retireError" :title="retireError" type="error" :closable="false" /><el-form label-position="top" :disabled="busy || impactLoading"
        ><el-form-item label="下线日期 *"
          ><el-date-picker
            v-model="retireForm.offlineAt"
            type="date"
            value-format="YYYY-MM-DD"
            placeholder="选择下线日" /></el-form-item
        ><el-form-item label="替代SKU（可选）"><el-select v-model="retireForm.replacementId" clearable placeholder="选择影响报告中的替代SKU"><el-option v-for="candidate in impact?.replacements || []" :key="String(candidate.sku_id)" :label="candidate.sku_code" :value="String(candidate.sku_id)" /></el-select></el-form-item
        ><el-form-item label="退役理由 *"
          ><el-input
            v-model="retireForm.reason"
            maxlength="500"
            show-word-limit
            type="textarea"
            :rows="3" /></el-form-item
      ></el-form>
      <div class="approval-note">
        退役流程：客户通知（至少30天提前期） → 双人审批（第二签须换人） → 下线执行。提交或驳回申请均不改变模型生命周期。
      </div></template
    ><template #footer v-if="!retireResult"
      ><el-button :disabled="busy || impactLoading" @click="retireOpen = false">取消</el-button
      ><el-button
        type="primary"
        :loading="busy"
        :disabled="
          !impact ||
          !retireForm.offlineAt ||
          !retireForm.reason.trim() ||
          impactLoading
        "
        @click="submitRetire"
        >提交退役审批</el-button
      ></template
    ></el-dialog
  >
</template>

<style scoped>
.catalog > .filters { grid-template-columns: minmax(180px, 2fr) repeat(5, minmax(105px, 1fr)) auto auto; }
.capability-options { display: flex; flex-wrap: wrap; gap: 0 12px; }
.capability-options .el-checkbox { margin-right: 0; }
@media (max-width: 1300px) { .catalog > .filters { grid-template-columns: repeat(4, minmax(0, 1fr)); } }
@media (max-width: 760px) { .catalog > .filters { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
</style>
