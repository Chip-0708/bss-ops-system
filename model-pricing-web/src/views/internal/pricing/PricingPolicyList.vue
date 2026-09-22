<script setup lang="ts">
import Decimal from "decimal.js";
import { computed, onMounted, reactive, ref } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { pricingPoliciesApi } from "../../../api/pricingPolicies";
import {
  DEFAULT_PRICING_POLICY_PRIORITY,
  type PricingPolicyDraft,
  type PricingPolicySummaryDTO,
} from "../../../api/pricingPolicies.types";
import { ApiError } from "../../../domain/common";
import { isDecimalAmount } from "../../../domain/money";
import { PERMISSIONS } from "../../../domain/permissions";
import { PRICING_POLICY_STATUS, type PricingPolicyStatus } from "../../../domain/status";
import { usePermissionStore } from "../../../stores/permission";

const permissions = usePermissionStore();
const canMaintain = computed(() => permissions.canAction(PERMISSIONS.PRICING_POLICY_EDIT));
const rows = ref<PricingPolicySummaryDTO[]>([]);
const loading = ref(false);
const error = ref("");
const pagination = reactive({ page: 1, size: 5, total: 0 });
const levelOptions = ["GOLD", "SILVER", "STANDARD", "ECONOMY"];
const strategyLabels = {
  TARGET_MARGIN: "目标毛利率",
  OFFICIAL_ANCHOR: "官方价锚定",
  COST_UP: "成本加成",
  FIXED: "固定价格",
} as const;
const detailOpen = ref(false);
const detail = ref<PricingPolicySummaryDTO>();
const editOpen = ref(false);
const editingId = ref("");
const submitting = ref(false);
const statusChangingId = ref("");
const formError = ref("");

// Go API currently supports one level, ALL scope and fixed CEIL-to-8-decimals execution.
// Description, multiple levels, alternate rounding modes and custom steps are deliberately not exposed.
const form = reactive<PricingPolicyDraft>({
  code: "",
  name: "",
  strategyType: "TARGET_MARGIN",
  levelCode: "GOLD",
  targetMarginRate: "25.00",
  officialMultiplier: "0.950000",
  priority: DEFAULT_PRICING_POLICY_PRIORITY,
});

const message = (value: unknown) => value instanceof ApiError
  ? value.message
  : value instanceof Error ? value.message : "操作失败，请稍后重试。";
const statusLabel = (status: PricingPolicyStatus) => PRICING_POLICY_STATUS[status].label;
const statusType = (status: PricingPolicyStatus) => PRICING_POLICY_STATUS[status].type;

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const result = await pricingPoliciesApi.list({ page: pagination.page, size: pagination.size });
    rows.value = result.list;
    pagination.total = result.total;
  } catch (value) {
    error.value = message(value);
    rows.value = [];
    pagination.total = 0;
  } finally {
    loading.value = false;
  }
}

function changePage(page: number) {
  pagination.page = page;
  void load();
}

function openDetail(row: PricingPolicySummaryDTO) {
  detail.value = row;
  detailOpen.value = true;
}

function fillForm(policy?: PricingPolicySummaryDTO) {
  editingId.value = policy?.id ?? "";
  Object.assign(form, {
    code: policy?.code ?? "",
    name: policy?.name ?? "",
    strategyType: policy?.strategyType === "OFFICIAL_ANCHOR" ? "OFFICIAL_ANCHOR" : "TARGET_MARGIN",
    levelCode: policy?.levelCode ?? "GOLD",
    targetMarginRate: policy?.targetMarginRate ?? "25.00",
    officialMultiplier: policy?.officialMultiplier ?? "0.950000",
    priority: policy?.priority ?? DEFAULT_PRICING_POLICY_PRIORITY,
  });
  formError.value = "";
  editOpen.value = true;
}

function createPolicy() {
  fillForm();
}

function editPolicy(row?: PricingPolicySummaryDTO) {
  const policy = row ?? detail.value;
  if (!policy?.canEdit) return;
  fillForm(policy);
  detailOpen.value = false;
}

function validateForm() {
  if (!form.code.trim() || !form.name.trim() || !form.levelCode) {
    return "请填写策略编码、策略名称并选择适用客户等级。";
  }
  const input = form.strategyType === "TARGET_MARGIN" ? form.targetMarginRate : form.officialMultiplier;
  if (!isDecimalAmount(input)) {
    return form.strategyType === "TARGET_MARGIN" ? "目标毛利率格式不正确。" : "官方价倍率格式不正确。";
  }
  try {
    const value = new Decimal(input);
    if (form.strategyType === "TARGET_MARGIN" && value.greaterThanOrEqualTo(100)) {
      return "目标毛利率必须小于 100。";
    }
  } catch {
    return form.strategyType === "TARGET_MARGIN" ? "目标毛利率格式不正确。" : "官方价倍率格式不正确。";
  }
  return "";
}

async function save() {
  if (submitting.value) return;
  formError.value = validateForm();
  if (formError.value) return;
  const draft: PricingPolicyDraft = { ...form };
  const policyId = editingId.value;
  submitting.value = true;
  try {
    await ElMessageBox.confirm(`确认保存“${draft.name}”策略草稿？`, "保存定价策略", {
      confirmButtonText: "确认保存",
      cancelButtonText: "取消",
    });
    if (policyId) await pricingPoliciesApi.update(policyId, draft);
    else await pricingPoliciesApi.create(draft);
    editOpen.value = false;
    ElMessage.success("定价策略草稿已保存。");
    await load();
  } catch (value) {
    if (value !== "cancel" && value !== "close") ElMessage.error(message(value));
  } finally {
    submitting.value = false;
  }
}

async function changeStatus(
  policy: PricingPolicySummaryDTO,
  status: Extract<PricingPolicyStatus, "ACTIVE" | "ARCHIVED">,
) {
  if (statusChangingId.value) return;
  const action = status === "ACTIVE" ? "激活" : "归档";
  statusChangingId.value = policy.id;
  try {
    await ElMessageBox.confirm(`确认${action}“${policy.name}”？`, `${action}定价策略`, {
      confirmButtonText: `确认${action}`,
      cancelButtonText: "取消",
      type: status === "ARCHIVED" ? "warning" : "info",
    });
    await pricingPoliciesApi.updateStatus(policy, status);
    ElMessage.success(`定价策略已${action}。`);
    await load();
    if (detail.value?.id === policy.id) {
      detail.value = rows.value.find((row) => row.id === policy.id);
    }
  } catch (value) {
    if (value !== "cancel" && value !== "close") ElMessage.error(message(value));
  } finally {
    statusChangingId.value = "";
  }
}

onMounted(load);
</script>

<template>
  <section class="page-heading">
    <div><div class="eyebrow">PRICING POLICIES</div><h1>定价策略</h1><p>维护价目表生成所依据的规则、范围与状态。</p></div>
    <el-button v-if="canMaintain" type="primary" @click="createPolicy">＋ 新建策略</el-button>
  </section>
  <div class="stats policy-stats">
    <div><span>策略记录</span><strong>{{ pagination.total }}<small>条</small></strong></div>
    <div><span>本页启用</span><strong>{{ rows.filter((row) => row.status === 'ACTIVE').length }}<small class="green">服务端状态</small></strong></div>
    <div><span>本页草稿</span><strong>{{ rows.filter((row) => row.status === 'DRAFT').length }}<small>兼容草稿可编辑</small></strong></div>
    <div><span>页面支持</span><strong class="policy-mode">2<small>目标毛利 / 官方锚定</small></strong></div>
  </div>
  <section class="panel policy-panel">
    <div class="section-title"><h2>策略列表 <span>{{ pagination.total }}</span></h2><el-button :loading="loading" @click="load">刷新</el-button></div>
    <el-alert v-if="error" :title="error" type="error" show-icon :closable="false"><el-button text @click="load">重新加载</el-button></el-alert>
    <div v-loading="loading" class="policy-list">
      <el-empty v-if="!rows.length && !loading && !error" description="暂无定价策略" />
      <el-table v-else :data="rows" row-key="id">
        <el-table-column label="编码 / 名称" min-width="230"><template #default="{ row }"><b>{{ row.name }}</b><div class="mono muted">{{ row.code }}</div></template></el-table-column>
        <el-table-column label="定价方式" min-width="145"><template #default="{ row }">{{ strategyLabels[row.strategyType as keyof typeof strategyLabels] }}</template></el-table-column>
        <el-table-column label="适用范围" min-width="155"><template #default="{ row }">{{ row.scopeType }}<div class="muted">等级：{{ row.levelCode || '不限' }}</div></template></el-table-column>
        <el-table-column label="优先级" width="90" prop="priority" />
        <el-table-column label="状态" width="105"><template #default="{ row }"><el-tag :type="statusType(row.status)">{{ statusLabel(row.status) }}</el-tag></template></el-table-column>
        <el-table-column label="操作" min-width="210"><template #default="{ row }"><el-button link type="primary" @click="openDetail(row)">详情</el-button><el-button v-if="canMaintain && row.canEdit" link @click="editPolicy(row)">编辑草稿</el-button><el-button v-if="canMaintain && row.status === 'DRAFT'" link type="success" :loading="statusChangingId === row.id" @click="changeStatus(row, 'ACTIVE')">激活</el-button><el-button v-if="canMaintain && row.status === 'ACTIVE'" link type="warning" :loading="statusChangingId === row.id" @click="changeStatus(row, 'ARCHIVED')">归档</el-button></template></el-table-column>
      </el-table>
    </div>
    <div class="catalog-pagination"><el-pagination :current-page="pagination.page" :page-size="pagination.size" :total="pagination.total" layout="total, prev, pager, next" @current-change="changePage" /></div>
  </section>

  <el-drawer v-model="detailOpen" title="定价策略详情" size="min(680px, 96vw)">
    <template v-if="detail">
      <div class="policy-detail-head"><div><div class="eyebrow">{{ detail.code }}</div><h2>{{ detail.name }}</h2><p class="muted">{{ strategyLabels[detail.strategyType] }}</p></div><el-tag :type="statusType(detail.status)" size="large">{{ statusLabel(detail.status) }}</el-tag></div>
      <el-descriptions :column="1" border>
        <el-descriptions-item label="适用范围">{{ detail.scopeType }}{{ detail.scopeId ? ` / ${detail.scopeId}` : '' }}</el-descriptions-item>
        <el-descriptions-item label="适用客户等级">{{ detail.levelCode || '不限' }}</el-descriptions-item>
        <el-descriptions-item label="策略参数"><template v-if="detail.strategyType === 'TARGET_MARGIN'">目标毛利率 {{ detail.targetMarginRate }}%</template><template v-else-if="detail.strategyType === 'OFFICIAL_ANCHOR'">官方价倍率 {{ detail.officialMultiplier }}</template><template v-else>{{ detail.paramValue }}</template></el-descriptions-item>
        <el-descriptions-item label="优先级">{{ detail.priority }}</el-descriptions-item>
      </el-descriptions>
      <el-alert class="policy-note" title="当前后端固定按向上取整到 8 位小数执行；说明、自定义取整和多等级尚不属于真实接口能力。" type="info" show-icon :closable="false" />
      <div v-if="canMaintain && detail.status !== 'ARCHIVED'" class="policy-actions"><el-button v-if="detail.canEdit" @click="editPolicy()">编辑草稿</el-button><el-button v-if="detail.status === 'DRAFT'" type="success" :loading="statusChangingId === detail.id" @click="changeStatus(detail, 'ACTIVE')">激活</el-button><el-button v-if="detail.status === 'ACTIVE'" type="warning" :loading="statusChangingId === detail.id" @click="changeStatus(detail, 'ARCHIVED')">归档</el-button></div>
    </template>
  </el-drawer>

  <el-dialog v-model="editOpen" :title="editingId ? '编辑定价策略草稿' : '新建定价策略草稿'" width="min(720px, 96vw)" :close-on-click-modal="false" :close-on-press-escape="!submitting" :show-close="!submitting">
    <el-alert title="当前按后端真实契约保存：范围固定 ALL、单一客户等级、状态 DRAFT；新建优先级默认 100，价格统一向上取整到 8 位小数。" type="info" show-icon :closable="false" />
    <el-form class="policy-form" label-position="top" :disabled="submitting">
      <el-form-item label="策略编码 *"><el-input v-model="form.code" placeholder="请输入稳定且唯一的策略编码" :disabled="Boolean(editingId)" /></el-form-item>
      <el-form-item label="策略名称 *"><el-input v-model="form.name" /></el-form-item>
      <el-form-item label="定价方式 *"><el-radio-group v-model="form.strategyType"><el-radio-button value="TARGET_MARGIN">目标毛利率</el-radio-button><el-radio-button value="OFFICIAL_ANCHOR">官方价锚定</el-radio-button></el-radio-group></el-form-item>
      <el-form-item label="适用客户等级 *"><el-select v-model="form.levelCode"><el-option v-for="level in levelOptions" :key="level" :label="level" :value="level" /></el-select></el-form-item>
      <el-form-item v-if="form.strategyType === 'TARGET_MARGIN'" label="目标毛利率（%）*"><el-input v-model="form.targetMarginRate" placeholder="例如 25.00" /></el-form-item>
      <el-form-item v-else label="官方价倍率 *"><el-input v-model="form.officialMultiplier" placeholder="例如 0.950000" /></el-form-item>
      <el-alert v-if="formError" :title="formError" type="error" show-icon :closable="false" />
    </el-form>
    <template #footer><el-button :disabled="submitting" @click="editOpen = false">取消</el-button><el-button type="primary" :loading="submitting" @click="save">保存草稿</el-button></template>
  </el-dialog>
</template>

<style scoped>
.policy-stats { margin-bottom: 22px; }.policy-mode { font-size: 25px !important; }.policy-panel { padding: 22px; }.policy-list { min-height: 280px; }.policy-detail-head { display: flex; justify-content: space-between; gap: 20px; margin-bottom: 18px; }.policy-detail-head h2 { margin: 8px 0 4px; }.policy-note { margin-top: 18px; }.policy-actions { display: flex; justify-content: flex-end; margin-top: 20px; }.policy-form { margin-top: 18px; }
</style>
