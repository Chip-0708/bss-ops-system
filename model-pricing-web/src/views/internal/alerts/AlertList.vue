<script setup lang="ts">
import { formatDateTime } from "../../../domain/date";
import { computed, onMounted, reactive, ref } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { alertsApi } from "../../../api/alerts";
import type { AlertDetailDTO, AlertSummaryDTO } from "../../../api/alerts.types";
import { ApiError } from "../../../domain/common";
import { PERMISSIONS } from "../../../domain/permissions";
import { ALERT_LEVEL, ALERT_STATUS, type AlertLevel, type AlertStatus } from "../../../domain/status";
import { usePermissionStore } from "../../../stores/permission";

const permissions = usePermissionStore();
const rows = ref<AlertSummaryDTO[]>([]);
const loading = ref(false);
const error = ref("");
const pagination = reactive({ page: 1, size: 5, total: 0 });
const filters = reactive({ alertType: "", severity: "" as AlertLevel | "", status: "OPEN" as AlertStatus | "" });
const detailOpen = ref(false);
const detailLoading = ref(false);
const detailError = ref("");
const detail = ref<AlertDetailDTO>();
const submitting = ref(false);
const resolveOpen = ref(false);
const resolveReason = ref("");

const canHandle = computed(() => permissions.canAction(PERMISSIONS.ALERT_HANDLE) && detail.value?.canHandle && detail.value.status !== "RESOLVED");
const message = (value: unknown) => value instanceof ApiError ? value.message : value instanceof Error ? value.message : "操作失败，请稍后重试。";
const formatTime = (value?: string | null) => formatDateTime(value, "—", false);
const statusLabel = (status: AlertStatus) => ALERT_STATUS[status].label;
const statusType = (status: AlertStatus) => ALERT_STATUS[status].type;
const levelLabel = (level: AlertLevel) => ALERT_LEVEL[level].label;
const levelType = (level: AlertLevel) => ALERT_LEVEL[level].type;

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const result = await alertsApi.list({ page: pagination.page, size: pagination.size, alertType: filters.alertType.trim() || undefined, severity: filters.severity || undefined, status: filters.status || undefined });
    rows.value = result.list;
    pagination.total = result.total;
  } catch (value) {
    error.value = message(value);
    rows.value = [];
    pagination.total = 0;
  } finally { loading.value = false; }
}

function query() { pagination.page = 1; void load(); }
function reset() { Object.assign(filters, { alertType: "", severity: "", status: "OPEN" }); query(); }
function changePage(page: number) { pagination.page = page; void load(); }

function openDetail(row: AlertSummaryDTO) { detailOpen.value = true; detailError.value = ""; detail.value = row as AlertDetailDTO; }
async function refresh(id: string) { await load(); detail.value = rows.value.find(row => row.id === id) as AlertDetailDTO | undefined; }

async function acknowledge() {
  if (!detail.value || submitting.value) return;
  try {
    await ElMessageBox.confirm(`确认已知晓告警“${detail.value.title}”并开始处理？`, "确认告警", { confirmButtonText: "确认", cancelButtonText: "取消", type: "warning" });
    submitting.value = true;
    const id = detail.value.id;
    await alertsApi.acknowledge(id);
    ElMessage.success("告警已确认。状态由后端保存。");
    await refresh(id);
  } catch (value) {
    if (value !== "cancel" && value !== "close") ElMessage.error(message(value));
  } finally { submitting.value = false; }
}

function openResolve() { resolveReason.value = ""; resolveOpen.value = true; }
async function resolve() {
  if (!detail.value || submitting.value) return;
  if (!resolveReason.value.trim()) { ElMessage.warning("请填写处理结论。"); return; }
  submitting.value = true;
  try {
    const id = detail.value.id;
    await alertsApi.resolve(id, resolveReason.value);
    resolveOpen.value = false;
    ElMessage.success("告警已解决，处理记录已保留。");
    await refresh(id);
  } catch (value) { ElMessage.error(message(value)); }
  finally { submitting.value = false; }
}
onMounted(load);
</script>

<template>
  <section class="page-heading"><div><div class="eyebrow">ALERT CENTER</div><h1>告警中心</h1><p>集中查看业务异常、处理建议和流转记录。</p></div><el-button type="primary" @click="reset">查看待处理</el-button></section>
  <div class="stats alert-stats"><div><span>当前筛选</span><strong>{{ pagination.total }}<small>条告警</small></strong></div><div><span>本页紧急</span><strong>{{ rows.filter((row) => row.level === 'CRITICAL').length }}<small>优先处理</small></strong></div><div><span>本页处理中</span><strong>{{ rows.filter((row) => row.status === 'HANDLING').length }}<small>处理中</small></strong></div><div><span>处理原则</span><strong class="alert-mode">后端为准<small>页面不自行消警</small></strong></div></div>
  <section class="panel alert-panel">
    <div class="section-title"><h2>业务告警 <span>{{ pagination.total }}</span></h2><el-button :loading="loading" @click="load">刷新</el-button></div>
    <div class="filters alert-filters">
      <el-input v-model="filters.alertType" clearable aria-label="告警类型" placeholder="告警类型，如 QUOTE_EXPIRE" />
      <el-select v-model="filters.severity" clearable placeholder="全部级别" aria-label="告警级别筛选"><el-option v-for="(config, level) in ALERT_LEVEL" :key="level" :label="config.label" :value="level" /></el-select>
      <el-select v-model="filters.status" clearable placeholder="全部状态" aria-label="告警状态筛选"><el-option v-for="(config, status) in ALERT_STATUS" :key="status" :label="config.label" :value="status" /></el-select>
      <el-button type="primary" @click="query">查询</el-button><el-button text @click="reset">重置</el-button>
    </div>
    <el-alert v-if="error" :title="error" type="error" show-icon :closable="false"><el-button text @click="load">重新加载</el-button></el-alert>
    <div v-loading="loading" class="alert-list">
      <el-empty v-if="!rows.length && !loading && !error" description="没有符合条件的告警" />
      <el-table v-else :data="rows" row-key="id">
        <el-table-column label="级别" width="85"><template #default="{ row }"><el-tag :type="levelType(row.level)">{{ levelLabel(row.level) }}</el-tag></template></el-table-column>
        <el-table-column label="告警" min-width="270"><template #default="{ row }"><b>{{ row.title }}</b><div class="mono muted">{{ row.entityType }} · {{ row.entityId }}</div></template></el-table-column>
        <el-table-column prop="module" label="模块" min-width="110" />
        <el-table-column label="触发时间" min-width="140"><template #default="{ row }">{{ formatTime(row.triggeredAt) }}</template></el-table-column>
        <el-table-column prop="ownerName" label="处理组" min-width="120" />
        <el-table-column label="状态" width="105"><template #default="{ row }"><el-tag :type="statusType(row.status)">{{ statusLabel(row.status) }}</el-tag></template></el-table-column>
        <el-table-column label="操作" width="90"><template #default="{ row }"><el-button link type="primary" @click="openDetail(row)">查看</el-button></template></el-table-column>
      </el-table>
    </div>
    <div class="catalog-pagination"><el-pagination :current-page="pagination.page" :page-size="pagination.size" :total="pagination.total" layout="total, prev, pager, next" @current-change="changePage" /></div>
  </section>

  <el-drawer v-model="detailOpen" v-loading="detailLoading" title="告警详情" size="min(680px, 96vw)">
    <el-alert v-if="detailError" :title="detailError" type="error" show-icon :closable="false" />
    <template v-if="detail">
      <div class="alert-detail-head"><div><el-tag :type="levelType(detail.level)">{{ levelLabel(detail.level) }}</el-tag><h2>{{ detail.title }}</h2><p class="mono muted">{{ detail.entityType }} · {{ detail.entityId }}</p></div><el-tag :type="statusType(detail.status)" size="large">{{ statusLabel(detail.status) }}</el-tag></div>
      <el-descriptions :column="1" border><el-descriptions-item label="业务模块">{{ detail.module }}</el-descriptions-item><el-descriptions-item label="触发时间">{{ formatTime(detail.triggeredAt) }}</el-descriptions-item><el-descriptions-item label="处理组">{{ detail.ownerName || '待分派' }}</el-descriptions-item><el-descriptions-item label="异常说明">{{ detail.description }}</el-descriptions-item><el-descriptions-item label="建议处理">{{ detail.suggestedAction }}</el-descriptions-item></el-descriptions>
      <h3>告警依据</h3><ul class="alert-evidence"><li v-for="item in detail.evidence" :key="item">{{ item }}</li></ul>
      <h3>处理记录</h3><el-empty v-if="!detail.history.length" description="暂无处理记录" :image-size="55" /><el-timeline v-else><el-timeline-item v-for="item in detail.history" :key="item.at" :timestamp="formatTime(item.at)">{{ item.actor }} · {{ item.action }}<p>{{ item.note }}</p></el-timeline-item></el-timeline>
      <el-alert v-if="!canHandle && detail.status !== 'RESOLVED'" title="当前身份只能查看该告警。" type="info" show-icon :closable="false" />
      <div v-if="canHandle" class="alert-actions"><el-button v-if="detail.status === 'OPEN'" :disabled="submitting" @click="acknowledge">确认并开始处理</el-button><el-button type="primary" :loading="submitting" @click="openResolve">标记已解决</el-button></div>
    </template>
  </el-drawer>
  <el-dialog v-model="resolveOpen" title="确认告警已解决" width="520px" :close-on-click-modal="false"><el-alert title="请记录实际处理结果；前端不会删除原始告警。" type="info" :closable="false" /><el-form class="resolve-form" label-position="top"><el-form-item label="处理结论 *"><el-input v-model="resolveReason" type="textarea" :rows="4" maxlength="300" show-word-limit /></el-form-item></el-form><template #footer><el-button :disabled="submitting" @click="resolveOpen = false">取消</el-button><el-button type="primary" :loading="submitting" @click="resolve">确认已解决</el-button></template></el-dialog>
</template>

<style scoped>
.alert-stats { margin-bottom: 22px; }.alert-mode { font-size: 18px !important; }.alert-panel { padding: 22px; }.alert-filters { grid-template-columns: minmax(220px, 1fr) 150px 130px 130px auto auto; margin-bottom: 18px; }.alert-list { min-height: 220px; }.alert-detail-head { display: flex; justify-content: space-between; gap: 20px; margin-bottom: 18px; }.alert-detail-head h2 { margin: 12px 0 6px; }.alert-evidence { padding: 14px 18px 14px 34px; border-radius: 8px; background: var(--app-surface-muted); line-height: 1.9; }.alert-actions { position: sticky; bottom: 0; display: flex; justify-content: flex-end; gap: 10px; margin: 24px -20px -20px; padding: 16px 20px; border-top: 1px solid var(--app-border); background: var(--app-surface); }.resolve-form { margin-top: 18px; }@media (max-width: 960px) { .alert-filters { grid-template-columns: 1fr; } }
</style>
