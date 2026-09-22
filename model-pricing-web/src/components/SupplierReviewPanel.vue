<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { supplierReviewsApi, type ReviewKind, type SupplierReview } from "../api/supplierReviews";
import { QUALIFICATION_SUBMISSION_STATUS } from "../domain/status";
import { usePermissionStore } from "../stores/permission";
import { PERMISSIONS } from "../domain/permissions";

const props = defineProps<{ kind: ReviewKind }>();
const permission = usePermissionStore();
const allowed = computed(() => permission.canAction(PERMISSIONS.SUPPLIER_APPROVE));
const title = computed(() => "供应商资质审核");
const rows = ref<SupplierReview[]>([]); const detail = ref<SupplierReview>(); const open = ref(false);
const loading = ref(false); const submitting = ref(false); const error = ref(""); const reason = ref("");
const detailLoading = ref(false); const detailError = ref(""); const detailId = ref(""); let detailSequence = 0;
const filters = reactive({ page: 1, size: 5, search: "", status: "" }); const total = ref(0);
const statuses = computed(() => QUALIFICATION_SUBMISSION_STATUS);
const statusLabel = (value: string) => (statuses.value as Record<string, { label: string }>)[value]?.label || value;
let sequence = 0;
async function load(reset = false) {
  if (!allowed.value) return; if (reset) filters.page = 1;
  const current = ++sequence; loading.value = true; error.value = "";
  try { const result = await supplierReviewsApi.list(props.kind, { ...filters }); if (current === sequence) { rows.value = result.list; total.value = result.total; } }
  catch (value) { if (current === sequence) error.value = value instanceof Error ? value.message : "申请查询失败"; }
  finally { if (current === sequence) loading.value = false; }
}
async function show(row: { id: string }) {
  if (submitting.value) return;
  const current = ++detailSequence; detailId.value = row.id; detail.value = undefined; reason.value = ""; open.value = true; detailLoading.value = true; detailError.value = "";
  try { const result = await supplierReviewsApi.get(props.kind, row.id); if (current === detailSequence) detail.value = result; }
  catch (value) { if (current === detailSequence) detailError.value = value instanceof Error ? value.message : "详情查询失败"; }
  finally { if (current === detailSequence) detailLoading.value = false; }
}
function closeDetail(done: () => void) { if (!submitting.value) done(); }
async function review(result: "APPROVED" | "REJECTED") {
  if (!detail.value || submitting.value) return;
  if (result === "REJECTED" && !reason.value.trim()) { ElMessage.warning("请填写驳回原因"); return; }
  const id = detail.value.id; const payload = { result, reason: reason.value.trim() }; submitting.value = true;
  try {
    try { await ElMessageBox.confirm(result === "APPROVED" ? "确认通过？模型申请通过仅记录结果，不会自动创建模型。" : "确认驳回并将原因反馈给供应商？", "确认审核"); } catch { return; }
    detail.value = await supplierReviewsApi.review(props.kind, id, payload);
    ElMessage.success("审核结果已保存"); await load(); if (error.value) ElMessage.warning("审核已保存，但列表刷新失败，请重新查询，勿重复提交");
  } catch (value) { ElMessage.error(value instanceof Error ? value.message : "审核失败"); }
  finally { submitting.value = false; }
}
onMounted(() => load());
</script>

<template>
  <section v-if="allowed" class="panel" style="margin-bottom: 20px" v-loading="loading">
    <h3>{{ title }}</h3>
    <el-space wrap><el-input v-model="filters.search" placeholder="申请名称 / 供应商" clearable @keyup.enter="load(true)" /><el-select v-model="filters.status" placeholder="全部状态" clearable style="width: 160px"><el-option v-for="(item, key) in statuses" :key="key" :label="item.label" :value="key" /></el-select><el-button @click="load(true)">查询 / 刷新</el-button></el-space>
    <el-alert v-if="error" :title="error" type="error" :closable="false" /><el-empty v-else-if="!loading && !rows.length" description="暂无申请" />
    <el-table v-else :data="rows"><el-table-column prop="supplierName" label="供应商" /><el-table-column prop="name" label="申请名称" /><el-table-column label="状态"><template #default="{ row }">{{ statusLabel(row.status) }}</template></el-table-column><el-table-column prop="submittedAt" label="提交时间" /><el-table-column label="操作"><template #default="{ row }"><el-button link type="primary" @click="show(row)">详情 / 审核</el-button></template></el-table-column></el-table>
    <el-pagination v-model:current-page="filters.page" :page-size="filters.size" :total="total" layout="prev, pager, next, total" @current-change="load()" />
  </section>
  <el-drawer v-model="open" v-loading="detailLoading" :title="title" size="min(760px, 96vw)" :before-close="closeDetail">
    <el-alert v-if="detailError" :title="detailError" type="error" :closable="false"><el-button @click="show({ id: detailId })">重新查询</el-button></el-alert>
    <template v-if="detail">
      <h3>{{ detail.name }} · {{ statusLabel(detail.status) }}</h3><p>{{ detail.supplierName }}</p>
      <el-descriptions :column="1" border>
        <el-descriptions-item label="证件编号">{{ detail.material.documentNoMasked }}</el-descriptions-item><el-descriptions-item label="附件（未接真实存储）">{{ detail.material.attachmentName }}</el-descriptions-item><el-descriptions-item label="有效期">{{ detail.material.validFrom || '未指定' }} 至 {{ detail.material.validTo || '未指定' }}</el-descriptions-item>
        <el-descriptions-item label="审核意见">{{ detail.material.reviewComment || '暂无意见' }}</el-descriptions-item>
      </el-descriptions>
      <h4>本轮处理记录</h4><el-empty v-if="!detail.history.length" description="暂无本轮处理记录；历史意见见上方资料" /><p v-for="item in detail.history" :key="item.at">{{ item.at }} · {{ item.reviewer }} · {{ statusLabel(item.result) }} · {{ item.reason || '通过' }}</p>
      <template v-if="detail.status === 'SUBMITTED' || detail.status === 'REVIEWING'"><el-input v-model="reason" type="textarea" maxlength="300" show-word-limit placeholder="审核意见（驳回必填）" :disabled="submitting" /><el-space style="margin-top: 16px"><el-button type="primary" :loading="submitting" :disabled="submitting" @click="review('APPROVED')">通过</el-button><el-button type="danger" :disabled="submitting" @click="review('REJECTED')">驳回</el-button></el-space></template>
    </template>
  </el-drawer>
</template>
