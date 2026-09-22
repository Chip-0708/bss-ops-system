<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { costApi } from "../../../api/cost";
import type { CurrentCostBaselineDTO } from "../../../api/cost.types";
import { costParamsApi } from "../../../api/costParams";
import type { CostParamOverrideDTO, CostParamsViewDTO } from "../../../api/costParams.types";
import { ApiError } from "../../../domain/common";
import { assertContractId } from "../../../domain/contractId";
import { formatDateTime } from "../../../domain/date";
import { usePermissionStore } from "../../../stores/permission";
import { PERMISSIONS } from "../../../domain/permissions";

const permission = usePermissionStore();
const canEditParams = computed(() => permission.canAction(PERMISSIONS.COST_EDIT));

const rows = ref<CurrentCostBaselineDTO[]>([]);
const loading = ref(false);
const error = ref("");
const keyword = ref("");
const onlySinglePoint = ref(false);
const pagination = reactive({ page: 1, size: 10, total: 0 });
let requestSequence = 0;

async function load() {
  const sequence = ++requestSequence;
  loading.value = true;
  error.value = "";
  try {
    const result = await costApi.currentList({
      page: pagination.page,
      size: pagination.size,
      keyword: keyword.value.trim() || undefined,
      only_single_point: onlySinglePoint.value || undefined,
    });
    if (sequence !== requestSequence) return;
    if (!Array.isArray(result.list) || !Number.isSafeInteger(result.total) || result.total < 0) {
      throw new Error("成本基线列表响应格式不正确。");
    }
    result.list.forEach((row) => {
      assertContractId(row.sku_id);
      assertContractId(row.primary_supplier_id);
      if (typeof row.sku_code !== "string" || !Number.isSafeInteger(row.version) ||
          typeof row.currency !== "string" || typeof row.valid_from !== "string" ||
          typeof row.supplier_count !== "number" || typeof row.single_point !== "boolean" ||
          (row.unit_cost !== undefined && typeof row.unit_cost !== "string") ||
          (row.floor_price != null && typeof row.floor_price !== "string")) {
        throw new Error("成本基线字段不符合当前后端约定。");
      }
    });
    rows.value = result.list;
    pagination.total = result.total;
  } catch (value) {
    if (sequence !== requestSequence) return;
    rows.value = [];
    pagination.total = 0;
    error.value = value instanceof ApiError || value instanceof Error
      ? value.message : "成本基线加载失败，请重试。";
  } finally {
    if (sequence === requestSequence) loading.value = false;
  }
}

function query() { pagination.page = 1; void load(); }
function reset() { keyword.value = ""; onlySinglePoint.value = false; query(); }
function changePage(page: number) { pagination.page = page; void load(); }

// —— 成本参数（GET/PUT /cost/params：defaults 只读 + overrides 全量替换）——
const paramsView = ref<CostParamsViewDTO>();
const paramsLoading = ref(false);
const paramsError = ref("");
const message = (value: unknown) => value instanceof ApiError ? value.message : value instanceof Error ? value.message : "成本参数操作失败，请稍后重试。";
const rate = (value: string) => `${(Number(value) * 100).toFixed(2)}%`;

async function loadParams() {
  paramsLoading.value = true;
  paramsError.value = "";
  try { paramsView.value = await costParamsApi.get(); }
  catch (value) { paramsView.value = undefined; paramsError.value = message(value); }
  finally { paramsLoading.value = false; }
}

const editOpen = ref(false);
const submitting = ref(false);
const editError = ref("");
interface OverrideForm { scope_type: "MODEL" | "SUPPLIER"; scope_id: string; loss_rate: string; channel_rate: string; }
const overrideForms = ref<OverrideForm[]>([]);
function openEditor() {
  if (!paramsView.value) return;
  overrideForms.value = paramsView.value.overrides.map((o) => ({
    scope_type: o.scope_type, scope_id: String(o.scope_id), loss_rate: o.loss_rate, channel_rate: o.channel_rate,
  }));
  editError.value = ""; editOpen.value = true;
}
function addOverride() { overrideForms.value.push({ scope_type: "MODEL", scope_id: "", loss_rate: "0.0300", channel_rate: "0.0100" }); }
function removeOverride(index: number) { overrideForms.value.splice(index, 1); }
async function saveParams() {
  if (submitting.value || !paramsView.value) return;
  editError.value = "";
  const rateOk = (v: string) => /^\d+(\.\d{1,4})?$/.test(v.trim()) && Number(v) >= 0 && Number(v) <= 1;
  const seen = new Set<string>();
  const overrides: CostParamOverrideDTO[] = [];
  for (const [i, form] of overrideForms.value.entries()) {
    const id = Number(form.scope_id);
    if (!Number.isSafeInteger(id) || id <= 0) { editError.value = `第 ${i + 1} 条：scope_id 必须是正整数。`; return; }
    const key = `${form.scope_type}:${id}`;
    if (seen.has(key)) { editError.value = `第 ${i + 1} 条：${key} 重复。`; return; }
    seen.add(key);
    if (!rateOk(form.loss_rate) || !rateOk(form.channel_rate)) { editError.value = `第 ${i + 1} 条：费率必须是 0~1、最多 4 位小数。`; return; }
    // 继承该 scope 现有的税项字段（本页不编辑税项，全量替换须原样带回）。
    const existing = paramsView.value.overrides.find((o) => o.scope_type === form.scope_type && String(o.scope_id) === form.scope_id);
    overrides.push({
      scope_type: form.scope_type, scope_id: id,
      loss_rate: form.loss_rate.trim(), channel_rate: form.channel_rate.trim(),
      tax_inclusive: existing?.tax_inclusive ?? paramsView.value.defaults.tax_inclusive,
      withholding_tax: existing?.withholding_tax ?? paramsView.value.defaults.withholding_tax,
    });
  }
  submitting.value = true;
  try {
    await ElMessageBox.confirm(`确认保存 ${overrides.length} 条成本参数覆盖？将全量替换现有覆盖，并触发受影响 SKU 重算。`, "保存成本参数", { confirmButtonText: "确认保存", cancelButtonText: "取消" });
    const result = await costParamsApi.replace({ overrides });
    editOpen.value = false;
    ElMessage.success(`已保存 ${result.overrides_count} 条覆盖，触发 ${result.submitted_tasks} 个重算任务。`);
    await loadParams();
    await load();
  } catch (value) { if (value !== "cancel" && value !== "close") editError.value = message(value); }
  finally { submitting.value = false; }
}

onMounted(() => { void load(); void loadParams(); });
</script>

<template>
  <section class="page-heading">
    <div><div class="eyebrow">COST BASELINES</div><h1>成本管理</h1><p>查看当前成本基线和服务端计算的代表组件成本，并维护成本参数。</p></div>
    <el-button :loading="loading" @click="load">刷新基线</el-button>
  </section>
  <div class="stats cost-stats">
    <div><span>当前成本基线</span><strong>{{ pagination.total }}<small>个 SKU</small></strong></div>
    <div><span>本页记录</span><strong>{{ rows.length }}<small>条</small></strong></div>
    <div><span>单一供应商</span><strong>{{ rows.filter((row) => row.single_point).length }}<small>本页</small></strong></div>
  </div>

  <section class="panel cost-params-panel" v-loading="paramsLoading">
    <div class="section-title"><h2>成本参数</h2><el-button v-if="canEditParams && paramsView" :disabled="paramsLoading" @click="openEditor">维护覆盖参数</el-button></div>
    <el-alert type="info" :closable="false" title="全局默认参数只读；MODEL/SUPPLIER 覆盖可维护。保存为全量替换，并由后端触发受影响 SKU 重算。费率按数值显示。" />
    <el-alert v-if="paramsError" :title="paramsError" type="error" show-icon :closable="false"><el-button text @click="loadParams">重新加载</el-button></el-alert>
    <template v-if="paramsView">
      <el-descriptions :column="2" border class="cost-params-defaults">
        <el-descriptions-item label="默认损耗率">{{ rate(paramsView.defaults.loss_rate) }}</el-descriptions-item>
        <el-descriptions-item label="默认通道费率">{{ rate(paramsView.defaults.channel_rate) }}</el-descriptions-item>
      </el-descriptions>
      <el-table :data="paramsView.overrides" row-key="scope_type-scope_id" class="cost-params-table">
        <el-table-column label="范围" min-width="150"><template #default="{ row }">{{ row.scope_type === 'MODEL' ? '模型' : '供应商' }} #{{ row.scope_id }}</template></el-table-column>
        <el-table-column label="损耗率" min-width="120"><template #default="{ row }">{{ rate(row.loss_rate) }}</template></el-table-column>
        <el-table-column label="通道费率" min-width="120"><template #default="{ row }">{{ rate(row.channel_rate) }}</template></el-table-column>
      </el-table>
      <el-empty v-if="!paramsView.overrides.length" description="暂无覆盖参数，全部沿用默认" :image-size="56" />
    </template>
  </section>

  <section class="panel current-cost-panel">
    <div class="section-title"><h2>当前成本基线 <span>{{ pagination.total }}</span></h2><span class="muted">只读 · 服务端计算结果</span></div>
    <div class="filters current-cost-filters">
      <el-input v-model="keyword" clearable aria-label="成本 SKU 搜索" placeholder="搜索 SKU 编码" @keyup.enter="query" />
      <el-checkbox v-model="onlySinglePoint">只看单一供应商</el-checkbox>
      <el-button type="primary" @click="query">查询</el-button><el-button text @click="reset">重置</el-button>
    </div>
    <el-alert v-if="error" :title="error" type="error" show-icon :closable="false"><el-button text @click="load">重新加载</el-button></el-alert>
    <div v-loading="loading" class="current-cost-list">
      <el-empty v-if="!rows.length && !loading && !error" description="没有符合条件的当前成本基线" />
      <el-table v-else :data="rows" row-key="sku_id">
        <el-table-column prop="sku_code" label="SKU" min-width="200" />
        <el-table-column label="代表组件完全成本" min-width="175"><template #default="{ row }"><b class="mono">{{ row.unit_cost ?? '权限限制或暂缺' }}</b><div class="muted">{{ row.unit_cost_basis ?? '组件未提供' }} · {{ row.currency }}</div></template></el-table-column>
        <el-table-column label="成本底价" min-width="130"><template #default="{ row }"><span class="mono">{{ row.floor_price ?? '暂缺' }}</span> {{ row.currency }}</template></el-table-column>
        <el-table-column prop="primary_supplier_name" label="主供应商" min-width="150" />
        <el-table-column label="有效报价供应商" min-width="125"><template #default="{ row }">{{ row.supplier_count }} 家<el-tag v-if="row.single_point" size="small" type="warning" class="cost-single-tag">单一来源</el-tag></template></el-table-column>
        <el-table-column label="版本 / 生效时间" min-width="170"><template #default="{ row }">V{{ row.version }}<div class="muted">{{ formatDateTime(row.valid_from, '暂缺', false) }}</div></template></el-table-column>
      </el-table>
    </div>
    <div class="catalog-pagination"><el-pagination :current-page="pagination.page" :page-size="pagination.size" :total="pagination.total" layout="total, prev, pager, next" @current-change="changePage" /></div>
  </section>

  <el-dialog v-model="editOpen" title="维护成本参数覆盖" width="min(760px, 96vw)" :close-on-click-modal="false" :close-on-press-escape="!submitting" :show-close="!submitting">
    <el-alert type="warning" :closable="false" title="保存为全量替换：未列出的覆盖将被删除并回退到默认。费率填 0~1 的小数（如 0.03 表示 3%），最多 4 位小数。" />
    <el-form label-position="top" :disabled="submitting">
      <article v-for="(form, index) in overrideForms" :key="index" class="override-row">
        <el-select v-model="form.scope_type" style="width: 130px"><el-option label="模型" value="MODEL" /><el-option label="供应商" value="SUPPLIER" /></el-select>
        <el-input v-model="form.scope_id" placeholder="scope_id（正整数）" />
        <el-input v-model="form.loss_rate" placeholder="损耗率 如 0.0300" />
        <el-input v-model="form.channel_rate" placeholder="通道费率 如 0.0100" />
        <el-button text type="danger" @click="removeOverride(index)">删除</el-button>
      </article>
      <el-button @click="addOverride">＋ 增加覆盖</el-button>
    </el-form>
    <el-alert v-if="editError" :title="editError" type="error" :closable="false" />
    <template #footer><el-button :disabled="submitting" @click="editOpen = false">取消</el-button><el-button type="primary" :loading="submitting" @click="saveParams">确认保存</el-button></template>
  </el-dialog>
</template>

<style scoped>
.cost-stats { margin-bottom: 22px; }
.cost-params-panel { padding: 22px; margin-bottom: 22px; }
.cost-params-defaults { margin: 14px 0; }
.cost-params-table { margin-top: 10px; }
.current-cost-panel { padding: 22px; }
.current-cost-filters { grid-template-columns: minmax(250px, 1fr) auto auto auto; margin-bottom: 18px; }
.current-cost-list { min-height: 280px; }
.cost-single-tag { margin-left: 8px; }
.override-row { display: flex; gap: 10px; align-items: center; margin-bottom: 10px; }
.override-row > .el-input { flex: 1; }
@media (max-width: 960px) { .current-cost-filters { grid-template-columns: 1fr; } .override-row { flex-wrap: wrap; } }
</style>
