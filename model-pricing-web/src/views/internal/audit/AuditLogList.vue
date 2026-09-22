<script setup lang="ts">
import { formatDateTime } from "../../../domain/date";
import { onMounted, reactive, ref } from "vue";
import { ElMessage } from "element-plus";
import { auditApi } from "../../../api/audit";
import type { AuditListParams, AuditLogDetailDTO, AuditLogSummaryDTO } from "../../../api/audit.types";
import { ApiError } from "../../../domain/common";

const rows = ref<AuditLogSummaryDTO[]>([]);
const loading = ref(false);
const exporting = ref(false);
const error = ref("");
const pagination = reactive({ page: 1, size: 5, total: 0 });
const filters = reactive({ action: "", targetType: "", sourceType: "", from: "", to: "" });
const detailOpen = ref(false);
const detailLoading = ref(false);
const detailError = ref("");
const detail = ref<AuditLogDetailDTO>();

const message = (value: unknown) => value instanceof ApiError ? value.message : value instanceof Error ? value.message : "操作失败，请稍后重试。";
const formatTime = (value?: string | null) => formatDateTime(value, "—", true);
const formatJson = (value?: Record<string, unknown>) => value ? JSON.stringify(value, null, 2) : "未记录";
function params() { return { action: filters.action.trim() || undefined, targetType: filters.targetType.trim() || undefined, sourceType: filters.sourceType || undefined, from: filters.from || undefined, to: filters.to || undefined }; }

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const result = await auditApi.list({ page: pagination.page, size: pagination.size, ...params() });
    rows.value = result.list;
    pagination.total = result.total;
  } catch (value) { error.value = message(value); rows.value = []; pagination.total = 0; }
  finally { loading.value = false; }
}
function query() { pagination.page = 1; void load(); }
function reset() { Object.assign(filters, { action: "", targetType: "", sourceType: "", from: "", to: "" }); query(); }
function changePage(page: number) { pagination.page = page; void load(); }
function openDetail(row: AuditLogSummaryDTO) { detailOpen.value = true; detailError.value = ""; detail.value = row as AuditLogDetailDTO; }
async function exportLogs() {
  if (exporting.value) return;
  if (!filters.from || !filters.to) { ElMessage.warning("导出前请选择开始和结束日期。"); return; }
  exporting.value = true;
  try {
    const result = await auditApi.export({ from: filters.from, to: filters.to, format: "csv" });
    const url = URL.createObjectURL(result.blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = result.fileName;
    link.click();
    URL.revokeObjectURL(url);
    ElMessage.success("审计报告已按当前筛选条件导出。");
  } catch (value) { ElMessage.error(message(value)); }
  finally { exporting.value = false; }
}
onMounted(load);
</script>

<template>
  <section class="page-heading"><div><div class="eyebrow">AUDIT LOG</div><h1>审计日志</h1><p>查询谁在何时、通过什么来源、因何改变了业务对象。</p></div><el-button type="primary" :loading="exporting" @click="exportLogs">导出当前筛选</el-button></section>
  <section class="panel audit-panel">
    <div class="section-title"><h2>变更记录 <span>{{ pagination.total }}</span></h2><el-button :loading="loading" @click="query">查询</el-button></div>
    <div class="filters audit-filters">
      <el-input v-model="filters.action" clearable aria-label="审计动作" placeholder="动作，如 CUSTOMER_QUOTE_ACCEPTED" />
      <el-input v-model="filters.targetType" clearable aria-label="目标类型" placeholder="目标类型，如 CUSTOMER_QUOTE" />
      <el-select v-model="filters.sourceType" clearable aria-label="来源类型" placeholder="全部来源"><el-option label="内部人员" value="INTERNAL" /><el-option label="后台任务" value="WORKER" /><el-option label="系统" value="SYSTEM" /></el-select>
      <el-date-picker v-model="filters.from" type="date" value-format="YYYY-MM-DD" aria-label="审计开始日期" placeholder="开始日期" />
      <el-date-picker v-model="filters.to" type="date" value-format="YYYY-MM-DD" aria-label="审计结束日期" placeholder="结束日期" />
      <el-button type="primary" @click="query">查询</el-button><el-button text @click="reset">重置</el-button>
    </div>
    <el-alert v-if="error" :title="error" type="error" show-icon :closable="false"><el-button text @click="load">重新加载</el-button></el-alert>
    <div v-loading="loading" class="audit-list">
      <el-empty v-if="!rows.length && !loading && !error" description="没有符合条件的审计记录" />
      <el-table v-else :data="rows" row-key="id">
        <el-table-column label="时间" min-width="170"><template #default="{ row }">{{ formatTime(row.occurredAt) }}</template></el-table-column>
        <el-table-column label="操作人" min-width="135"><template #default="{ row }"><b>{{ row.operatorName }}</b><div class="muted">{{ row.operatorType === 'SYSTEM' ? '系统任务' : '内部用户' }}</div></template></el-table-column>
        <el-table-column prop="action" label="动作" min-width="150" />
        <el-table-column prop="module" label="模块" min-width="110" />
        <el-table-column label="业务对象" min-width="170"><template #default="{ row }"><span class="mono">{{ row.entityType }} · {{ row.entityId }}</span></template></el-table-column>
        <el-table-column label="记录" width="85"><template #default><el-tag type="info">已记录</el-tag></template></el-table-column>
        <el-table-column prop="source" label="来源" min-width="120" />
        <el-table-column label="操作" width="80"><template #default="{ row }"><el-button link type="primary" @click="openDetail(row)">详情</el-button></template></el-table-column>
      </el-table>
    </div>
    <div class="catalog-pagination"><el-pagination :current-page="pagination.page" :page-size="pagination.size" :total="pagination.total" layout="total, prev, pager, next" @current-change="changePage" /></div>
  </section>

  <el-drawer v-model="detailOpen" v-loading="detailLoading" title="审计记录详情" size="min(760px, 96vw)">
    <el-alert v-if="detailError" :title="detailError" type="error" show-icon :closable="false" />
    <template v-if="detail">
      <div class="audit-detail-head"><div><div class="eyebrow">{{ detail.module }}</div><h2>{{ detail.action }}</h2><p class="mono muted">{{ detail.entityType }} · {{ detail.entityId }}</p></div><el-tag type="info" size="large">审计记录</el-tag></div>
      <el-descriptions :column="2" border><el-descriptions-item label="发生时间">{{ formatTime(detail.occurredAt) }}</el-descriptions-item><el-descriptions-item label="操作人">{{ detail.operatorName }}</el-descriptions-item><el-descriptions-item label="身份类型">{{ detail.operatorType === 'SYSTEM' ? '系统任务' : '内部用户' }}</el-descriptions-item><el-descriptions-item label="来源">{{ detail.source }}</el-descriptions-item><el-descriptions-item label="请求标识" :span="2"><span class="mono">{{ detail.requestId }}</span></el-descriptions-item><el-descriptions-item label="原因 / 依据" :span="2">{{ detail.reason || '未记录' }}</el-descriptions-item></el-descriptions>
      <div class="audit-snapshots"><div><h3>变更前</h3><pre>{{ formatJson(detail.before) }}</pre></div><div><h3>变更后</h3><pre>{{ formatJson(detail.after) }}</pre></div></div>
      <h3>影响记录</h3><el-empty v-if="!detail.impact.length" description="未记录下游影响" :image-size="55" /><ul v-else class="audit-impact"><li v-for="item in detail.impact" :key="item">{{ item }}</li></ul>
      <el-alert title="审计页面只展示服务端保存的证据，不用于从日志反向重建业务状态。" type="info" show-icon :closable="false" />
    </template>
  </el-drawer>
</template>

<style scoped>
.audit-panel { padding: 22px; }.audit-filters { grid-template-columns: minmax(230px, 1fr) 140px 120px 145px 145px auto auto; margin-bottom: 18px; }.audit-list { min-height: 300px; }.audit-detail-head { display: flex; justify-content: space-between; gap: 20px; margin-bottom: 18px; }.audit-detail-head h2 { margin: 8px 0 6px; }.audit-snapshots { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }.audit-snapshots pre { min-height: 130px; margin: 0; padding: 14px; overflow: auto; border: 1px solid var(--app-border); border-radius: 8px; background: var(--app-surface-muted); color: var(--app-text-secondary); white-space: pre-wrap; }.audit-impact { padding: 14px 18px 14px 34px; border-radius: 8px; background: var(--app-surface-muted); line-height: 1.9; }@media (max-width: 1100px) { .audit-filters, .audit-snapshots { grid-template-columns: 1fr; } }
</style>
