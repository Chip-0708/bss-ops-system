<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { supplierQuoteApi } from "../../api/quoteContract";
import type { QuoteDetailDTO, QuoteHistoryDTO } from "../../api/quoteContract.types";
import { SUPPLIER_QUOTE_STATUS } from "../../domain/status";
import { formatDateTime } from "../../domain/date";
import { ApiError } from "../../domain/common";
const router = useRouter(), route = useRoute(), rows = ref<QuoteHistoryDTO[]>([]), loading = ref(false), error = ref("");
const filters = reactive({ status: "", sku_id: "", from: "", to: "" });
const pagination = reactive({ page: 1, size: 10, total: 0 });
const detailOpen = ref(false), detailLoading = ref(false), detailError = ref(""), detail = ref<QuoteDetailDTO>();
let sequence = 0;
async function load() {
    loading.value = true;
    error.value = "";
    try {
        const result = await supplierQuoteApi.history({ page: pagination.page, size: pagination.size, status: filters.status || undefined, sku_id: filters.sku_id || undefined, from: filters.from || undefined, to: filters.to || undefined });
        rows.value = result.list;
        pagination.total = result.total;
    }
    catch (value) {
        rows.value = [];
        pagination.total = 0;
        error.value = value instanceof ApiError ? value.message : "报价历史加载失败。";
    }
    finally {
        loading.value = false;
    }
}
function query() { pagination.page = 1; void load(); }
async function openDetail(id: QuoteHistoryDTO["id"]) {
    const current = ++sequence;
    detailOpen.value = true;
    detailLoading.value = true;
    detailError.value = "";
    detail.value = undefined;
    try {
        const result = await supplierQuoteApi.detail(id);
        if (current !== sequence)
            return;
        if (String(result.id) !== String(id))
            throw new Error("对象不一致");
        detail.value = result;
    }
    catch (value) {
        if (current === sequence)
            detailError.value = value instanceof ApiError ? value.message : "详情加载失败，请重新查询。";
    }
    finally {
        if (current === sequence)
            detailLoading.value = false;
    }
}
onMounted(() => {
    void load();
    const id = route.query.quote_id;
    if (typeof id === "string" && /^[1-9]\d*$/.test(id)) void openDetail(id);
});
</script>

<template>
<section class="page-heading">
  <div>
    <div class="eyebrow">MY QUOTES</div>
    <h1>报价历史</h1>
    <p>当前供应商的不可变报价版本和审批结论。</p>
  </div>
  <el-button type="primary" @click="router.push('/supplier/quotes/new')">＋ 新建报价</el-button>
</section>
<section class="panel quote-panel">
  <div class="section-title">
    <h2>
      我的报价
      <span>{{ pagination.total }}</span>
    </h2>
    <el-button @click="load">刷新</el-button>
  </div>
  <div class="filters">
    <el-input v-model="filters.sku_id" placeholder="SKU ID筛选" clearable />
    <el-select v-model="filters.status" placeholder="全部状态" clearable>
      <el-option
        v-for="(config, status) in SUPPLIER_QUOTE_STATUS"
        :key="status"
        :label="config.label"
        :value="status"
       />
    </el-select>
    <el-date-picker
      v-model="filters.from"
      type="datetime"
      value-format="YYYY-MM-DDTHH:mm:ssZ"
      placeholder="查询起始时间"
     />
    <el-date-picker
      v-model="filters.to"
      type="datetime"
      value-format="YYYY-MM-DDTHH:mm:ssZ"
      placeholder="查询结束时间"
     />
    <el-button @click="query">查询</el-button>
  </div>
  <el-alert v-if="error" :title="error" type="error" :closable="false" />
  <el-table v-loading="loading" :data="rows">
    <el-table-column label="版本">
      <template #default="{ row }">
        <b>V{{ row.version_no }}</b>
        <div class="muted">{{ row.id }} · {{ row.item_count }} 个SKU</div>
      </template>
    </el-table-column>
    <el-table-column label="状态">
      <template #default="{ row }">
        <el-tag :type="SUPPLIER_QUOTE_STATUS[row.status as keyof typeof SUPPLIER_QUOTE_STATUS].type">{{ SUPPLIER_QUOTE_STATUS[row.status as keyof typeof SUPPLIER_QUOTE_STATUS].label }}</el-tag>
      </template>
    </el-table-column>
    <el-table-column label="有效期">
      <template #default="{ row }">
        {{ formatDateTime(row.valid_from) }}
        <div>{{ formatDateTime(row.valid_to) }}</div>
      </template>
    </el-table-column>
    <el-table-column label="提交时间">
      <template #default="{ row }">{{ formatDateTime(row.submitted_at) }}</template>
    </el-table-column>
    <el-table-column label="审批结论">
      <template #default="{ row }">{{ row.decision?.reason || (row.decision?.result === 'APPROVED' ? '通过' : '尚未审批') }}</template>
    </el-table-column>
    <el-table-column label="操作">
      <template #default="{ row }">
        <el-button @click="openDetail(row.id)">查看详情</el-button>
      </template>
    </el-table-column>
  </el-table>
  <el-pagination
    v-model:current-page="pagination.page"
    :page-size="pagination.size"
    :total="pagination.total"
    layout="total, prev, pager, next"
    @current-change="load"
   />
</section>
<el-drawer v-model="detailOpen" title="报价详情" size="min(900px,96vw)">
  <div v-loading="detailLoading">
    <el-alert v-if="detailError" :title="detailError" type="error" :closable="false" />
    <template v-if="detail">
      <h2>V{{ detail.version_no }} · {{ SUPPLIER_QUOTE_STATUS[detail.status].label }}</h2>
      <p>{{ formatDateTime(detail.valid_from) }} — {{ formatDateTime(detail.valid_to) }}</p>
      <el-alert
        v-if="detail.decision?.reason"
        :title="detail.decision.reason"
        :type="detail.decision.result === 'REJECTED' ? 'error' : 'info'"
        :closable="false"
       />
      <article v-for="item in detail.items" :key="String(item.sku_id)">
        <h3>{{ item.model_name }} · {{ item.sku_code }} · {{ item.currency }}</h3>
        <p v-if="item.fx_tier">汇率档位 {{ item.fx_tier }}</p>
        <el-table :data="item.components">
          <el-table-column prop="component_type" label="组件" />
          <el-table-column prop="unit_price" label="供应价" />
          <el-table-column label="倍率">
            <template #default="{ row }">{{ row.multiplier || '绝对价' }}</template>
          </el-table-column>
        </el-table>
        <el-descriptions :column="2" border>
          <el-descriptions-item
            v-for="(value,key) in item.constraints || {}"
            :key="key"
            :label="String(key)"
          >{{ value ?? '未声明' }}</el-descriptions-item>
        </el-descriptions>
      </article>
      <p>报价说明：{{ detail.remark || detail.audit_reason || '无' }}</p>
      <el-button
        v-if="['EFFECTIVE','EXPIRED','VOIDED','REJECTED'].includes(detail.status)"
        @click="router.push(`/supplier/quotes/${detail.id}/renew`)"
      >续报 / 修订新版本</el-button>
    </template>
  </div>
</el-drawer>
</template>

<style scoped>
.quote-panel {
  padding:22px
}
.filters {
  display:flex;
  gap:12px;
  flex-wrap:wrap;
  margin:18px 0
}
.filters .el-input,.filters .el-select {
  width:180px
}
article {
  margin:24px 0
}
</style>
