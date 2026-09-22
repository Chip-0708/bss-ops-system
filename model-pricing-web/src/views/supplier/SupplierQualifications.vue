<script setup lang="ts">
import { formatDateTime } from "../../domain/date";
import { ElMessage, type UploadFile } from "element-plus";
import { computed, onMounted, reactive, ref } from "vue";
import { supplierAccountApi } from "../../api/supplierAccount";
import type { CreateSupplierQualificationRequest, SupplierQualificationSubmissionDTO } from "../../api/supplierAccount.types";
import { ApiError } from "../../domain/common";
import { QUALIFICATION_SUBMISSION_STATUS, type QualificationSubmissionStatus } from "../../domain/status";

const rows = ref<SupplierQualificationSubmissionDTO[]>([]);
const loading = ref(false);
const error = ref("");
const statusFilter = ref<QualificationSubmissionStatus | "">("");
const formOpen = ref(false);
const submitting = ref(false);
const formError = ref("");
const form = reactive<CreateSupplierQualificationRequest>({ qualificationType: "BUSINESS_LICENSE", qualificationName: "", documentNo: "", validFrom: undefined, validTo: undefined, attachmentName: "", remark: "" });
const typeOptions = [
  { value: "BUSINESS_LICENSE", label: "营业执照" },
  { value: "PARTNER_AUTHORIZATION", label: "厂商或云服务合作授权" },
  { value: "SECURITY_REPORT", label: "安全与合规资料" },
  { value: "OTHER", label: "其他资质" },
];
const filteredRows = computed(() => statusFilter.value ? rows.value.filter((row) => row.status === statusFilter.value) : rows.value);

const errorMessage = (value: unknown) => value instanceof ApiError ? value.message : "操作失败，请稍后重试。";
const statusConfig = (status: QualificationSubmissionStatus) => QUALIFICATION_SUBMISSION_STATUS[status];
const formatTime = (value?: string | null) => formatDateTime(value, "暂缺", false);

async function load() {
  loading.value = true; error.value = "";
  try { rows.value = await supplierAccountApi.listQualifications(); }
  catch (value) { rows.value = []; error.value = errorMessage(value); }
  finally { loading.value = false; }
}

function startCreate() {
  Object.assign(form, { qualificationType: "BUSINESS_LICENSE", qualificationName: "", documentNo: "", validFrom: undefined, validTo: undefined, attachmentName: "", remark: "" });
  formError.value = "";
  formOpen.value = true;
}

function chooseFile(uploadFile: UploadFile) {
  if (uploadFile.size && uploadFile.size > 10 * 1024 * 1024) {
    form.attachmentName = "";
    formError.value = "附件不能超过 10 MB。";
    return;
  }
  form.attachmentName = uploadFile.name;
  formError.value = "";
}

function validate() {
  if (!form.qualificationName.trim() || !form.documentNo.trim()) return "请填写资质名称和证件编号。";
  if (!form.attachmentName) return "请选择资质附件。";
  if (form.validFrom && form.validTo && Date.parse(form.validTo) <= Date.parse(form.validFrom)) return "有效期截止时间必须晚于开始时间。";
  return "";
}

async function submitMaterial() {
  formError.value = validate();
  if (formError.value) return;
  submitting.value = true;
  try {
    const item = await supplierAccountApi.submitQualification({ ...form, qualificationName: form.qualificationName.trim(), documentNo: form.documentNo.trim(), attachmentName: form.attachmentName, remark: form.remark?.trim() || undefined });
    rows.value.unshift(item);
    formOpen.value = false;
    ElMessage.success("资质材料已提交，等待内部审核。");
  } catch (value) { formError.value = errorMessage(value); }
  finally { submitting.value = false; }
}

onMounted(load);
</script>

<template>
  <section class="page-heading"><div><div class="eyebrow">QUALIFICATIONS</div><h1>资质管理</h1><p>查看已提交资质、有效期、审核进度和审核意见。</p></div><el-button type="primary" @click="startCreate">＋ 提交资质材料</el-button></section>
  <div class="stats qualification-stats"><div><span>资质材料</span><strong>{{ rows.length }}<small>份</small></strong></div><div><span>审核中</span><strong>{{ rows.filter((row) => ['SUBMITTED','REVIEWING'].includes(row.status)).length }}<small>待处理</small></strong></div><div><span>已通过</span><strong>{{ rows.filter((row) => row.status === 'APPROVED').length }}<small class="green">有效记录</small></strong></div><div><span>驳回 / 过期</span><strong>{{ rows.filter((row) => ['REJECTED','EXPIRED'].includes(row.status)).length }}<small>需处理</small></strong></div></div>
  <section class="panel qualification-panel">
    <div class="section-title"><h2>资质记录 <span>{{ filteredRows.length }}</span></h2><div><el-select v-model="statusFilter" clearable aria-label="资质状态筛选" placeholder="全部状态" style="width:150px"><el-option v-for="(config, status) in QUALIFICATION_SUBMISSION_STATUS" :key="status" :label="config.label" :value="status" /></el-select><el-button :loading="loading" @click="load">刷新</el-button></div></div>
    <el-alert v-if="error" :title="error" type="error" show-icon :closable="false"><el-button text @click="load">重新加载</el-button></el-alert>
    <div v-loading="loading" class="qualification-list"><el-empty v-if="!filteredRows.length && !loading && !error" description="没有符合条件的资质材料" /><el-table v-else :data="filteredRows" row-key="id"><el-table-column label="资质名称" min-width="210"><template #default="{ row }"><b>{{ row.qualificationName }}</b><div class="mono muted">{{ row.documentNoMasked }}</div></template></el-table-column><el-table-column label="有效期至" min-width="155"><template #default="{ row }">{{ formatTime(row.validTo) }}</template></el-table-column><el-table-column label="附件" min-width="180"><template #default="{ row }">{{ row.attachmentName }}</template></el-table-column><el-table-column label="状态" width="115"><template #default="{ row }"><el-tag :type="statusConfig(row.status).type">{{ statusConfig(row.status).label }}</el-tag></template></el-table-column><el-table-column label="提交 / 审核" min-width="180"><template #default="{ row }">{{ formatTime(row.submittedAt) }}<div class="muted">审核 {{ formatTime(row.reviewedAt) }}</div></template></el-table-column><el-table-column label="审核意见" min-width="210"><template #default="{ row }">{{ row.reviewComment || '暂无' }}</template></el-table-column></el-table></div>
  </section>
  <el-dialog v-model="formOpen" title="提交资质材料" width="min(760px, 96vw)" :close-on-click-modal="false"><el-alert title="当前开发契约仅登记附件文件名；真实文件上传、病毒扫描和存储凭证待后端接入。" type="warning" show-icon :closable="false" /><el-form label-position="top" class="qualification-form"><div class="qualification-form-grid"><el-form-item label="资质类型 *"><el-select v-model="form.qualificationType"><el-option v-for="option in typeOptions" :key="option.value" :label="option.label" :value="option.value" /></el-select></el-form-item><el-form-item label="资质名称 *"><el-input v-model="form.qualificationName" /></el-form-item><el-form-item label="证件编号 *"><el-input v-model="form.documentNo" /></el-form-item><el-form-item label="有效期开始"><el-date-picker v-model="form.validFrom" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" /></el-form-item><el-form-item label="有效期截止"><el-date-picker v-model="form.validTo" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" /></el-form-item><el-form-item label="附件 *"><el-upload accept=".pdf,.png,.jpg,.jpeg" :auto-upload="false" :show-file-list="false" :on-change="chooseFile"><el-button>选择文件</el-button></el-upload><div class="muted">{{ form.attachmentName || '尚未选择' }}</div></el-form-item></div><el-form-item label="备注"><el-input v-model="form.remark" type="textarea" :rows="2" maxlength="200" show-word-limit /></el-form-item><el-alert v-if="formError" :title="formError" type="error" show-icon :closable="false" /></el-form><template #footer><el-button :disabled="submitting" @click="formOpen = false">取消</el-button><el-button type="primary" :loading="submitting" @click="submitMaterial">提交审核</el-button></template></el-dialog>
</template>

<style scoped>
.qualification-stats { margin-bottom: 22px; }.qualification-panel { padding: 22px; }.section-title > div { display: flex; gap: 8px; }.qualification-list { min-height: 280px; }.qualification-form { margin-top: 18px; }.qualification-form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0 14px; }@media (max-width: 760px) { .qualification-form-grid { grid-template-columns: 1fr; } }
</style>
