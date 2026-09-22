<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { internalModelApplicationsApi } from "../api/supplierModelApplications";
import type { ModelApplicationDecision, ModelApplicationStatus, SupplierModelApplicationDTO } from "../api/supplierModelApplications.types";
import { modelsApi } from "../api/models";
import type { ModelSkuContractDTO } from "../api/models.types";
import { MODEL_APPLICATION_STATUS } from "../domain/status";
import { ApiError } from "../domain/common";
import { PERMISSIONS } from "../domain/permissions";
import { usePermissionStore } from "../stores/permission";

const permissions = usePermissionStore();
const canReview = computed(() => permissions.canAction(PERMISSIONS.MODEL_EDIT));
const rows = ref<SupplierModelApplicationDTO[]>([]), detail = ref<SupplierModelApplicationDTO>();
const loading = ref(false), submitting = ref(false), error = ref("");
const open = ref(false), total = ref(0);
const filters = reactive({ page: 1, size: 5, status: "" as ModelApplicationStatus | "", supplier_id: "" });
const decision = reactive<{ action: ModelApplicationDecision; targetSkuId: string; reason: string }>({ action: "APPROVE", targetSkuId: "", reason: "" });
const skuOptions = ref<ModelSkuContractDTO[]>([]), skuLoading = ref(false);
const message = (value: unknown) => value instanceof ApiError || value instanceof Error ? value.message : "操作失败，请稍后重试。";
const statusLabel = (value: ModelApplicationStatus) => MODEL_APPLICATION_STATUS[value].label;
async function load(reset = false) {
  if (reset) filters.page = 1; loading.value = true; error.value = "";
  try { const result = await internalModelApplicationsApi.list({ page: filters.page, size: filters.size, status: filters.status || undefined, supplier_id: filters.supplier_id.trim() || undefined }); rows.value = result.list; total.value = result.total; }
  catch (value) { rows.value = []; total.value = 0; error.value = message(value); }
  finally { loading.value = false; }
}
function show(row: SupplierModelApplicationDTO) { detail.value = row; Object.assign(decision, { action: "APPROVE", targetSkuId: "", reason: "" }); open.value = true; }
async function searchSkus(keyword = "") {
  skuLoading.value = true;
  try { const result = await modelsApi.list({ view: "sku", keyword: keyword.trim() || undefined, page: 1, size: 20 }); skuOptions.value = result.list.filter((row): row is ModelSkuContractDTO => "sku_code" in row); }
  catch (value) { ElMessage.error(message(value)); }
  finally { skuLoading.value = false; }
}
async function submitDecision() {
  if (!detail.value || submitting.value || detail.value.status !== "SUBMITTED") return;
  const target = Number(decision.targetSkuId);
  if (decision.action !== "REJECT" && (!Number.isSafeInteger(target) || target <= 0)) return void ElMessage.warning("通过或合并时请选择目标 SKU。");
  if (decision.action === "REJECT" && !decision.reason.trim()) return void ElMessage.warning("驳回时必须填写原因。");
  submitting.value = true;
  try {
    await ElMessageBox.confirm(`确认执行 ${decision.action}？终态申请不能再次审核。`, "确认审核", { type: "warning" });
    detail.value = await internalModelApplicationsApi.decide(detail.value.id, { action: decision.action,
      targetSkuId: decision.action === "REJECT" ? undefined : target, reason: decision.action === "REJECT" ? decision.reason.trim() : undefined });
    ElMessage.success("审核结果已保存。"); await load();
  } catch (value) { if (value !== "cancel" && value !== "close") ElMessage.error(message(value)); }
  finally { submitting.value = false; }
}
onMounted(() => load());
</script>

<template>
  <section class="panel review-panel" v-loading="loading"><div class="section-title"><h3>供应商新模型申请审核</h3><el-button @click="load()">查询</el-button></div><div class="review-filters"><el-input v-model="filters.supplier_id" clearable placeholder="供应商 ID" /><el-select v-model="filters.status" clearable placeholder="全部状态"><el-option v-for="(item, key) in MODEL_APPLICATION_STATUS" v-show="!['DRAFT','REVIEWING'].includes(key)" :key="key" :label="item.label" :value="key" /></el-select><el-button @click="load(true)">查询</el-button></div><el-alert v-if="error" :title="error" type="error" :closable="false" /><el-table :data="rows" empty-text="暂无申请"><el-table-column label="供应商 / 模型" min-width="220"><template #default="{ row }">供应商 {{ row.supplierId }}<div>{{ row.modelName }}</div></template></el-table-column><el-table-column label="状态" width="110"><template #default="{ row }">{{ statusLabel(row.status) }}</template></el-table-column><el-table-column label="可能重复" width="110"><template #default="{ row }">{{ row.duplicateCandidates.length }} 项</template></el-table-column><el-table-column label="操作" width="100"><template #default="{ row }"><el-button link type="primary" @click="show(row)">详情 / 审核</el-button></template></el-table-column></el-table><el-pagination v-model:current-page="filters.page" :page-size="filters.size" :total="total" layout="prev, pager, next, total" @current-change="load()" /></section>
  <el-drawer v-model="open" title="新模型申请审核" size="min(760px, 96vw)" :show-close="!submitting"><template v-if="detail"><div class="section-title"><h2>{{ detail.modelName }}</h2><el-tag>{{ statusLabel(detail.status) }}</el-tag></div><el-alert v-if="detail.rejectReason" :title="detail.rejectReason" type="error" :closable="false" /><el-descriptions :column="1" border><el-descriptions-item label="供应商 / 厂商">{{ detail.supplierId }} / {{ detail.vendorId || '未指定' }}</el-descriptions-item><el-descriptions-item label="申请资料"><pre>{{ JSON.stringify(detail.payload, null, 2) }}</pre></el-descriptions-item></el-descriptions><h3>可能重复</h3><el-empty v-if="!detail.duplicateCandidates.length" description="无查重候选" /><el-table v-else :data="detail.duplicateCandidates"><el-table-column prop="skuCode" label="SKU" /><el-table-column prop="matchedOn" label="来源" /><el-table-column prop="matchedValue" label="命中值" /><el-table-column label="相似度"><template #default="{ row }">{{ (row.similarity * 100).toFixed(1) }}%</template></el-table-column></el-table><template v-if="detail.status === 'SUBMITTED' && canReview"><el-form label-position="top" :disabled="submitting"><el-form-item label="审核动作"><el-radio-group v-model="decision.action"><el-radio-button value="APPROVE">入库</el-radio-button><el-radio-button value="MERGE">合并</el-radio-button><el-radio-button value="REJECT">驳回</el-radio-button></el-radio-group></el-form-item><el-form-item v-if="decision.action !== 'REJECT'" label="目标 SKU *"><el-select v-model="decision.targetSkuId" filterable remote :remote-method="searchSkus" :loading="skuLoading" @visible-change="(visible: boolean) => visible && searchSkus()"><el-option v-for="sku in skuOptions" :key="String(sku.id)" :label="`${sku.sku_code} · ID ${sku.id}`" :value="String(sku.id)" /></el-select></el-form-item><el-form-item v-else label="驳回原因 *"><el-input v-model="decision.reason" type="textarea" maxlength="512" show-word-limit /></el-form-item></el-form><el-button type="primary" :loading="submitting" @click="submitDecision">确认审核</el-button></template><el-button v-else disabled>已审核</el-button></template></el-drawer>
</template>

<style scoped>.review-panel{padding:20px;margin-bottom:20px}.review-filters{display:flex;gap:10px;margin-bottom:14px}.review-filters>*{max-width:180px}pre{white-space:pre-wrap;word-break:break-word;margin:0}</style>
