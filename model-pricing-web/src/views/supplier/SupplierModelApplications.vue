<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { supplierModelApplicationsApi } from "../../api/supplierModelApplications";
import type { ModelApplicationStatus, SupplierModelApplicationDTO } from "../../api/supplierModelApplications.types";
import { ApiError } from "../../domain/common";
import { formatDateTime } from "../../domain/date";
import { MODEL_APPLICATION_STATUS } from "../../domain/status";

interface ApplicationForm { modelName: string; vendorId: string; modelCode: string; modelType: string; officialUrl: string; apiDocsUrl: string; capabilities: string[]; contextWindow: string; businessReason: string }
const emptyForm = (): ApplicationForm => ({ modelName: "", vendorId: "", modelCode: "", modelType: "TEXT", officialUrl: "", apiDocsUrl: "", capabilities: [], contextWindow: "", businessReason: "" });
const rows = ref<SupplierModelApplicationDTO[]>([]);
const detail = ref<SupplierModelApplicationDTO>();
const loading = ref(false), submitting = ref(false), error = ref(""), formError = ref("");
const detailOpen = ref(false), formOpen = ref(false);
const filters = reactive({ status: "" as ModelApplicationStatus | "" });
const pagination = reactive({ page: 1, size: 10, total: 0 });
const form = reactive<ApplicationForm>(emptyForm());
const capabilityOptions = ["文本生成", "图像理解", "工具调用", "结构化输出", "向量检索", "语音能力"];
const message = (value: unknown) => value instanceof ApiError || value instanceof Error ? value.message : "操作失败，请稍后重试。";
const formatTime = (value?: string | null) => formatDateTime(value, "—", false);
const statusConfig = (status: ModelApplicationStatus) => MODEL_APPLICATION_STATUS[status];

async function load() {
  loading.value = true; error.value = "";
  try { const result = await supplierModelApplicationsApi.list({ page: pagination.page, size: pagination.size, status: filters.status || undefined }); rows.value = result.list; pagination.total = result.total; }
  catch (value) { rows.value = []; pagination.total = 0; error.value = message(value); }
  finally { loading.value = false; }
}
function query() { pagination.page = 1; void load(); }
function changePage(page: number) { pagination.page = page; void load(); }
function openDetail(row: SupplierModelApplicationDTO) { detail.value = row; detailOpen.value = true; }
function openCreate() { Object.assign(form, emptyForm()); formError.value = ""; formOpen.value = true; }
function validate() {
  if (!form.modelName.trim() || !form.modelCode.trim() || !form.businessReason.trim()) return "请填写模型名称、模型编码和申请原因。";
  if (form.vendorId && (!/^[1-9]\d*$/.test(form.vendorId) || !Number.isSafeInteger(Number(form.vendorId)))) return "vendor_id 必须是安全范围内的正整数。";
  for (const url of [form.officialUrl.trim(), form.apiDocsUrl.trim()].filter(Boolean)) { try { if (!/^https?:$/.test(new URL(url).protocol)) return "资料 URL 仅支持 HTTP/HTTPS。"; } catch { return "资料 URL 格式不正确。"; } }
  if (form.contextWindow && !/^\d+$/.test(form.contextWindow)) return "上下文长度必须是非负整数。";
  return "";
}
async function submit() {
  if (submitting.value) return;
  formError.value = validate(); if (formError.value) return;
  const payload = { model_code: form.modelCode.trim(), model_type: form.modelType, official_url: form.officialUrl.trim() || undefined,
    api_docs_url: form.apiDocsUrl.trim() || undefined, capabilities: [...form.capabilities], context_window: form.contextWindow.trim() || undefined,
    business_reason: form.businessReason.trim() };
  submitting.value = true;
  try {
    await ElMessageBox.confirm(`确认提交新模型申请“${form.modelName.trim()}”？提交后直接进入内部审核。`, "提交模型申请", { type: "warning" });
    const result = await supplierModelApplicationsApi.submit({ modelName: form.modelName.trim(), vendorId: form.vendorId ? Number(form.vendorId) : undefined, payload });
    formOpen.value = false; await load(); openDetail(result);
    ElMessage.success(result.duplicateCandidates.length ? "申请已提交，请同时核对可能重复项。" : "申请已提交，等待内部审核。");
  } catch (value) { if (value !== "cancel" && value !== "close") formError.value = message(value); }
  finally { submitting.value = false; }
}
onMounted(load);
</script>

<template>
  <section class="page-heading"><div><div class="eyebrow">MODEL APPLICATIONS</div><h1>新模型申请</h1><p>提交拟引入模型，并查看内部审核结果。</p></div><el-button type="primary" @click="openCreate">＋ 新建申请</el-button></section>
  <section class="panel application-panel">
    <div class="section-title"><h2>我的申请 <span>{{ pagination.total }}</span></h2><el-button :loading="loading" @click="query">查询</el-button></div>
    <div class="filters"><el-select v-model="filters.status" clearable placeholder="全部状态"><el-option v-for="(config, status) in MODEL_APPLICATION_STATUS" v-show="!['DRAFT','REVIEWING'].includes(status)" :key="status" :label="config.label" :value="status" /></el-select><el-button type="primary" @click="query">查询</el-button></div>
    <el-alert v-if="error" :title="error" type="error" :closable="false"><el-button text @click="load">重新加载</el-button></el-alert>
    <el-table v-loading="loading" :data="rows" row-key="id" empty-text="暂无模型申请">
      <el-table-column label="申请 / 模型" min-width="230"><template #default="{ row }"><b>{{ row.modelName }}</b><div class="mono muted">#{{ row.id }} · 供应商 {{ row.supplierId }}</div></template></el-table-column>
      <el-table-column label="状态" width="110"><template #default="{ row }"><el-tag :type="statusConfig(row.status).type">{{ statusConfig(row.status).label }}</el-tag></template></el-table-column>
      <el-table-column label="目标 SKU" width="120"><template #default="{ row }">{{ row.mergedSkuId || '—' }}</template></el-table-column>
      <el-table-column label="更新时间" min-width="180"><template #default="{ row }">{{ formatTime(row.updatedAt) }}</template></el-table-column>
      <el-table-column label="操作" width="90"><template #default="{ row }"><el-button link type="primary" @click="openDetail(row)">详情</el-button></template></el-table-column>
    </el-table>
    <el-pagination :current-page="pagination.page" :page-size="pagination.size" :total="pagination.total" layout="total, prev, pager, next" @current-change="changePage" />
  </section>

  <el-drawer v-model="detailOpen" title="模型申请详情" size="min(760px, 96vw)"><template v-if="detail"><div class="section-title"><h2>{{ detail.modelName }}</h2><el-tag :type="statusConfig(detail.status).type">{{ statusConfig(detail.status).label }}</el-tag></div><el-alert v-if="detail.rejectReason" :title="`驳回原因：${detail.rejectReason}`" type="error" :closable="false" /><el-descriptions :column="1" border><el-descriptions-item label="申请 ID">{{ detail.id }}</el-descriptions-item><el-descriptions-item label="厂商 ID">{{ detail.vendorId || '未指定' }}</el-descriptions-item><el-descriptions-item label="模型编码">{{ detail.payload.model_code || '未填写' }}</el-descriptions-item><el-descriptions-item label="申请资料"><pre>{{ JSON.stringify(detail.payload, null, 2) }}</pre></el-descriptions-item><el-descriptions-item label="目标 SKU">{{ detail.mergedSkuId || '—' }}</el-descriptions-item></el-descriptions><h3>可能重复（仅提示，不阻止申请）</h3><el-empty v-if="!detail.duplicateCandidates.length" description="没有查重候选" /><el-table v-else :data="detail.duplicateCandidates"><el-table-column prop="skuCode" label="SKU" /><el-table-column prop="matchedOn" label="命中来源" /><el-table-column prop="matchedValue" label="命中内容" /><el-table-column label="相似度"><template #default="{ row }">{{ (row.similarity * 100).toFixed(1) }}%</template></el-table-column></el-table></template></el-drawer>

  <el-dialog v-model="formOpen" title="提交新模型申请" width="min(760px, 96vw)" :close-on-click-modal="false" :show-close="!submitting"><el-alert title="后端收到后直接进入 SUBMITTED；查重候选只提示，不阻止提交。" type="info" :closable="false" /><el-form label-position="top" :disabled="submitting"><div class="form-grid"><el-form-item label="模型名称 *"><el-input v-model="form.modelName" maxlength="128" /></el-form-item><el-form-item label="厂商 ID（选填）"><el-input v-model="form.vendorId" inputmode="numeric" /></el-form-item><el-form-item label="模型编码 *"><el-input v-model="form.modelCode" /></el-form-item><el-form-item label="模型类型"><el-select v-model="form.modelType"><el-option label="文本" value="TEXT" /><el-option label="多模态" value="MULTIMODAL" /><el-option label="向量" value="EMBEDDING" /><el-option label="图像" value="IMAGE" /><el-option label="音频" value="AUDIO" /></el-select></el-form-item><el-form-item label="官方资料 URL"><el-input v-model="form.officialUrl" /></el-form-item><el-form-item label="API 文档 URL"><el-input v-model="form.apiDocsUrl" /></el-form-item><el-form-item label="上下文长度"><el-input v-model="form.contextWindow" /></el-form-item></div><el-form-item label="能力"><el-select v-model="form.capabilities" multiple><el-option v-for="item in capabilityOptions" :key="item" :label="item" :value="item" /></el-select></el-form-item><el-form-item label="申请原因 *"><el-input v-model="form.businessReason" type="textarea" :rows="3" /></el-form-item><el-alert v-if="formError" :title="formError" type="error" :closable="false" /></el-form><template #footer><el-button :disabled="submitting" @click="formOpen = false">取消</el-button><el-button type="primary" :loading="submitting" @click="submit">提交申请</el-button></template></el-dialog>
</template>

<style scoped>.application-panel{padding:22px}.filters{display:flex;gap:10px;margin-bottom:16px}.filters .el-select{width:180px}.form-grid{display:grid;grid-template-columns:1fr 1fr;gap:0 14px}pre{white-space:pre-wrap;word-break:break-word;margin:0}@media(max-width:760px){.form-grid{grid-template-columns:1fr}}</style>
