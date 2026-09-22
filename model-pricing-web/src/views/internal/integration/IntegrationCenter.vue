<script setup lang="ts">
import { formatDateTime } from "../../../domain/date";
import { computed, onMounted, ref } from "vue";
import { integrationApi, type IntegrationChannelDTO, type IntegrationOverviewDTO, type McpCapabilityDTO } from "../../../api/integration";
import { ApiError } from "../../../domain/common";
import { INTEGRATION_STATUS, JOB_RUN_STATUS, type IntegrationStatus, type JobRunStatus } from "../../../domain/status";

const activeTab = ref("channels");
const data = ref<IntegrationOverviewDTO>();
const loading = ref(false);
const error = ref("");
const channelStatusFilter = ref<IntegrationStatus | "">("");
const jobStatusFilter = ref<JobRunStatus | "">("");
const errorMessage = (value: unknown) => value instanceof ApiError ? value.message : value instanceof Error ? value.message : "系统对接信息加载失败，请稍后重试。";
const formatTime = (value?: string | null) => formatDateTime(value, "暂无执行记录", true);
const integrationStatus = (status: IntegrationStatus) => INTEGRATION_STATUS[status];
const jobStatus = (status: JobRunStatus) => JOB_RUN_STATUS[status];
const categoryLabel = (category: IntegrationChannelDTO["category"]) => ({ BACKEND: "业务后端", MODEL_VENDOR: "模型厂商", NOTIFICATION: "通知通道", MCP: "MCP" })[category];
const directionLabel = (direction: "INBOUND" | "OUTBOUND") => direction === "INBOUND" ? "流入" : "流出";
const capabilityMode = (mode: McpCapabilityDTO["mode"]) => mode === "READ" ? "只读" : "写操作";
const filteredChannels = computed(() => (data.value?.channels || []).filter((item) => !channelStatusFilter.value || item.status === channelStatusFilter.value));
const filteredJobs = computed(() => (data.value?.jobs || []).filter((item) => !jobStatusFilter.value || item.status === jobStatusFilter.value));

async function load() {
  loading.value = true; error.value = "";
  try { data.value = await integrationApi.getOverview(); }
  catch (value) { data.value = undefined; error.value = errorMessage(value); }
  finally { loading.value = false; }
}
onMounted(load);
</script>

<template>
  <section class="page-heading"><div><div class="eyebrow">SYSTEM INTEGRATION</div><h1>系统对接</h1><p>查看接口通道、同步任务、业务事件和 MCP 能力边界。</p></div><el-button :loading="loading" @click="load">刷新状态</el-button></section>
  <el-alert title="本页是开发环境状态说明，不代表生产服务已经连通；重跑任务、密钥管理和外部调用均未开放。" type="warning" show-icon :closable="false" />
  <el-alert v-if="error" class="integration-error" :title="error" type="error" show-icon :closable="false"><el-button text @click="load">重新加载</el-button></el-alert>

  <div v-loading="loading" class="integration-body">
    <template v-if="data">
      <div class="stats integration-stats"><div><span>对接通道</span><strong>{{ data.channels.length }}<small>个</small></strong></div><div><span>可用通道</span><strong>{{ data.channels.filter((item) => item.status === 'AVAILABLE').length }}<small class="green">开发检查</small></strong></div><div><span>同步任务</span><strong>{{ data.jobs.length }}<small>项</small></strong></div><div><span>失败任务</span><strong>{{ data.jobs.filter((item) => item.status === 'FAILED').length }}<small>需后端处理</small></strong></div></div>
      <el-tabs v-model="activeTab" class="integration-tabs">
        <el-tab-pane label="接口通道" name="channels">
          <section class="panel integration-panel"><div class="section-title"><h2>通道状态 <span>{{ filteredChannels.length }}</span></h2><el-select v-model="channelStatusFilter" clearable aria-label="通道状态筛选" placeholder="全部状态" style="width: 150px"><el-option v-for="(config, status) in INTEGRATION_STATUS" :key="status" :label="config.label" :value="status" /></el-select></div><el-empty v-if="!filteredChannels.length" description="没有符合条件的对接通道" /><el-table v-else :data="filteredChannels" row-key="id"><el-table-column label="通道" min-width="200"><template #default="{ row }"><b>{{ row.name }}</b><div class="muted">{{ row.description }}</div></template></el-table-column><el-table-column label="类型" width="110"><template #default="{ row }">{{ categoryLabel(row.category) }}</template></el-table-column><el-table-column prop="basePath" label="入口" min-width="130"><template #default="{ row }"><span class="mono">{{ row.basePath || '由服务端配置' }}</span></template></el-table-column><el-table-column prop="authentication" label="鉴权" min-width="155" /><el-table-column label="状态" width="105"><template #default="{ row }"><el-tag :type="integrationStatus(row.status).type">{{ integrationStatus(row.status).label }}</el-tag></template></el-table-column><el-table-column label="最近检查 / 说明" min-width="220"><template #default="{ row }">{{ formatTime(row.lastCheckedAt) }}<div class="muted">{{ row.note }}</div></template></el-table-column></el-table></section>
        </el-tab-pane>

        <el-tab-pane label="同步任务" name="jobs">
          <section class="panel integration-panel"><div class="section-title"><h2>任务运行状态 <span>{{ filteredJobs.length }}</span></h2><el-select v-model="jobStatusFilter" clearable aria-label="任务状态筛选" placeholder="全部状态" style="width: 150px"><el-option v-for="(config, status) in JOB_RUN_STATUS" :key="status" :label="config.label" :value="status" /></el-select></div><el-empty v-if="!filteredJobs.length" description="没有符合条件的同步任务" /><el-table v-else :data="filteredJobs" row-key="id"><el-table-column label="任务" min-width="210"><template #default="{ row }"><b>{{ row.name }}</b><div class="mono muted">{{ row.id }}</div></template></el-table-column><el-table-column prop="ownerModule" label="业务模块" min-width="125" /><el-table-column prop="schedule" label="调度" min-width="150" /><el-table-column label="状态" width="105"><template #default="{ row }"><el-tag :type="jobStatus(row.status).type">{{ jobStatus(row.status).label }}</el-tag></template></el-table-column><el-table-column label="最近执行" min-width="175"><template #default="{ row }">{{ formatTime(row.lastStartedAt) }}<div class="muted">完成 {{ formatTime(row.lastFinishedAt) }}</div></template></el-table-column><el-table-column label="结果" min-width="220"><template #default="{ row }">{{ row.affectedRecords === undefined ? '—' : `${row.affectedRecords} 条记录` }}<div class="muted">{{ row.message }}</div></template></el-table-column></el-table><el-alert class="integration-note" title="页面不会直接重跑任务；生产重跑需要后端权限、幂等和审计接口。" type="info" :closable="false" /></section>
        </el-tab-pane>

        <el-tab-pane label="业务事件" name="events">
          <section class="panel integration-panel"><div class="section-title"><h2>事件订阅 <span>{{ data.events.length }}</span></h2><span class="muted">状态机之间的服务端事件</span></div><el-empty v-if="!data.events.length" description="暂无事件定义" /><el-table v-else :data="data.events" row-key="id"><el-table-column label="事件类型" min-width="220"><template #default="{ row }"><span class="mono">{{ row.eventType }}</span><div class="muted">{{ row.description }}</div></template></el-table-column><el-table-column label="方向" width="85"><template #default="{ row }">{{ directionLabel(row.direction) }}</template></el-table-column><el-table-column prop="producer" label="生产方" min-width="125" /><el-table-column label="消费方" min-width="190"><template #default="{ row }"><el-tag v-for="consumer in row.consumers" :key="consumer" class="consumer-tag" effect="plain">{{ consumer }}</el-tag></template></el-table-column><el-table-column label="状态" width="105"><template #default="{ row }"><el-tag :type="integrationStatus(row.status).type">{{ integrationStatus(row.status).label }}</el-tag></template></el-table-column></el-table></section>
        </el-tab-pane>

        <el-tab-pane label="MCP 能力" name="mcp">
          <section class="panel integration-panel"><div class="section-title"><h2>MCP 能力边界 <span>{{ data.mcpCapabilities.length }}</span></h2><span class="muted">当前均未接入生产</span></div><el-empty v-if="!data.mcpCapabilities.length" description="暂无 MCP 能力定义" /><el-table v-else :data="data.mcpCapabilities" row-key="id"><el-table-column label="能力" min-width="190"><template #default="{ row }"><b>{{ row.name }}</b><div class="mono muted">{{ row.id }}</div></template></el-table-column><el-table-column label="模式" width="100"><template #default="{ row }"><el-tag :type="row.mode === 'WRITE' ? 'warning' : 'info'">{{ capabilityMode(row.mode) }}</el-tag></template></el-table-column><el-table-column label="所需权限" min-width="150"><template #default="{ row }"><span class="mono">{{ row.permission }}</span></template></el-table-column><el-table-column label="状态" width="105"><template #default="{ row }"><el-tag :type="integrationStatus(row.status).type">{{ integrationStatus(row.status).label }}</el-tag></template></el-table-column><el-table-column prop="boundary" label="安全边界" min-width="280" /></el-table><el-alert class="integration-note" title="MCP 写操作必须使用真实调用方身份、后端权限校验、幂等键和审计，不能信任客户端传入角色。" type="warning" show-icon :closable="false" /></section>
        </el-tab-pane>
      </el-tabs>
      <p class="integration-generated">状态生成时间：{{ formatTime(data.generatedAt) }} · {{ data.environment }}</p>
    </template>
    <el-empty v-else-if="!loading && !error" description="暂无系统对接信息" />
  </div>
</template>

<style scoped>
.integration-error { margin-top: 14px; }.integration-body { min-height: 430px; }.integration-stats { margin: 20px 0 8px; }.integration-tabs { margin-top: 12px; }.integration-panel { padding: 22px; }.integration-note { margin-top: 18px; }.consumer-tag { margin: 2px 5px 2px 0; }.integration-generated { color: var(--app-text-muted); font-size: 11px; text-align: right; }
</style>
