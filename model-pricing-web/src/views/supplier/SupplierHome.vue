<script setup lang="ts">
import { onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import { supplierQuoteApi } from "../../api/quoteContract";
import { QUOTE_FX_TIERS, type QuoteHistoryDTO } from "../../api/quoteContract.types";
import { ApiError } from "../../domain/common";
import { formatDateTime } from "../../domain/date";
import { SUPPLIER_QUOTE_STATUS } from "../../domain/status";

const router = useRouter();
const recent = ref<QuoteHistoryDTO[]>([]);
const totals = ref<Partial<Record<"APPROVING" | "EFFECTIVE" | "REJECTED", number>>>({});
const loading = ref(false);
const error = ref("");
const countError = ref(false);
const statusCards = [
  { status: "APPROVING", label: "待审批" },
  { status: "EFFECTIVE", label: "已生效" },
  { status: "REJECTED", label: "被驳回" },
] as const;
let loadSequence = 0;

async function load() {
  const sequence = ++loadSequence;
  loading.value = true;
  error.value = "";
  countError.value = false;
  try {
    const page = await supplierQuoteApi.history({ page: 1, size: 5 });
    if (sequence !== loadSequence) return;
    recent.value = page.list;
    const results = await Promise.allSettled(statusCards.map(card =>
      supplierQuoteApi.history({ page: 1, size: 1, status: card.status })));
    if (sequence !== loadSequence) return;
    totals.value = {};
    results.forEach((result, index) => {
      if (result.status === "fulfilled") totals.value[statusCards[index]!.status] = result.value.total;
      else countError.value = true;
    });
  } catch (value) {
    if (sequence === loadSequence) {
      recent.value = [];
      totals.value = {};
      error.value = value instanceof ApiError ? value.message : "最近报价加载失败，请稍后重试。";
    }
  } finally {
    if (sequence === loadSequence) loading.value = false;
  }
}

function openQuote(row: QuoteHistoryDTO) {
  void router.push({ path: "/supplier/quotes", query: { quote_id: String(row.id) } });
}

onMounted(load);
</script>

<template>
  <section class="page-heading">
    <div><div class="eyebrow">SUPPLIER WORKSPACE</div><h1>供应商首页</h1><p>从已有报价入口开始，并查看本企业最近的报价进度。</p></div>
    <el-button :loading="loading" @click="load">查询</el-button>
  </section>

  <div class="supplier-home-links">
    <el-button type="primary" @click="router.push('/supplier/quotes/new')">新建报价</el-button>
    <el-button @click="router.push('/supplier/quotes/import')">批量导入</el-button>
    <el-button @click="router.push('/supplier/quotes')">报价历史</el-button>
  </div>

  <el-alert v-if="error" :title="error" type="error" show-icon :closable="false"><el-button text @click="load">重试</el-button></el-alert>
  <el-alert v-if="countError" title="部分状态数量暂不可用；下方仍可查看最近报价。" type="warning" show-icon :closable="false" />

  <div class="supplier-home-metrics">
    <button v-for="card in statusCards" :key="card.status" type="button" class="supplier-home-metric" @click="router.push('/supplier/quotes')">
      <span>{{ card.label }}</span><strong>{{ totals[card.status] ?? '—' }}</strong><em>来自报价历史的状态总数 →</em>
    </button>
  </div>

  <section v-loading="loading" class="panel supplier-home-panel">
    <div class="section-title"><h2>最近报价</h2><el-button text @click="router.push('/supplier/quotes')">查看全部 →</el-button></div>
    <el-empty v-if="!recent.length && !error && !loading" description="暂无报价" />
    <el-table v-else :data="recent" row-key="id">
      <el-table-column label="报价" min-width="160"><template #default="{ row }">V{{ row.version_no }} · #{{ row.id }}</template></el-table-column>
      <el-table-column label="状态" min-width="130"><template #default="{ row }"><el-tag :type="SUPPLIER_QUOTE_STATUS[row.status as keyof typeof SUPPLIER_QUOTE_STATUS].type">{{ SUPPLIER_QUOTE_STATUS[row.status as keyof typeof SUPPLIER_QUOTE_STATUS].label }}</el-tag></template></el-table-column>
      <el-table-column prop="item_count" label="SKU 数" width="100" />
      <el-table-column label="提交时间" min-width="180"><template #default="{ row }">{{ formatDateTime(row.submitted_at, '尚未提交', false) }}</template></el-table-column>
      <el-table-column label="操作" width="110"><template #default="{ row }"><el-button link type="primary" @click="openQuote(row)">查看详情</el-button></template></el-table-column>
    </el-table>
  </section>

  <section class="panel supplier-home-panel">
    <div class="section-title"><h2>支持的汇率档位</h2><el-button text @click="router.push('/supplier/quotes/new')">前往新建报价 →</el-button></div>
    <p class="muted">USD 模型报价从以下八档中选择；CNY 模型不填写汇率档位。</p>
    <div class="supplier-home-tiers"><el-tag v-for="tier in QUOTE_FX_TIERS" :key="tier" type="info">{{ tier }}</el-tag></div>
  </section>
</template>

<style scoped>
.supplier-home-links { display: flex; flex-wrap: wrap; gap: 10px; margin-bottom: 20px; }
.supplier-home-metrics { display: grid; grid-template-columns: repeat(3, 1fr); gap: 17px; margin: 20px 0; }
.supplier-home-metric { padding: 20px 22px; border: 1px solid var(--app-border); border-top: 3px solid var(--app-accent); border-radius: 12px; background: var(--app-surface); color: var(--app-text-primary); text-align: left; cursor: pointer; }
.supplier-home-metric span,.supplier-home-metric em { color: var(--app-text-muted); font-size: 12px; font-style: normal; }
.supplier-home-metric strong { display: block; margin: 13px 0 8px; font-size: 29px; }
.supplier-home-panel { padding: 22px; margin-bottom: 20px; }.section-title { display: flex; align-items: center; justify-content: space-between; gap: 12px; }.section-title h2 { margin: 0; }
.supplier-home-tiers { display: flex; flex-wrap: wrap; gap: 8px; }
@media (max-width: 760px) { .supplier-home-metrics { grid-template-columns: 1fr; } }
</style>
