<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import { customerPortalApi } from "../../api/customerPortal";
import type { CustomerBilling, CustomerHome, CustomerNotification, CustomerPriceBook, CustomerSection, PortalCustomerQuote } from "../../api/customerPortal.types";
import { ApiError } from "../../domain/common";
import { formatDateTime } from "../../domain/date";

const route = useRoute();
const router = useRouter();
const section = computed(() => route.path.split("/").at(-1) as CustomerSection | "home");
const titles = { home: "客户首页", "price-book": "我的价目表", quotes: "我的报价", contracts: "我的合同", billing: "账单与余额", notifications: "通知中心" };
const loading = ref(false);
const error = ref("");
const submittingId = ref("");
const pagination = ref({ page: 1, size: 10, total: 0 });
const quoteStatus = ref("");
const home = ref<CustomerHome>();
const priceBook = ref<CustomerPriceBook>();
const quotes = ref<PortalCustomerQuote[]>([]);
const billing = ref<CustomerBilling>();
const notifications = ref<CustomerNotification[]>([]);
const message = (value: unknown) => value instanceof ApiError || value instanceof Error ? value.message : "查询失败，请稍后重试。";
const formatTime = (value?: string | null) => formatDateTime(value, "—", false);
const canAcceptQuote = (row: PortalCustomerQuote) => row.status !== "EFFECTIVE" &&
  (row.canAccept ?? row.status === "APPROVED");

function clearData() {
  home.value = undefined;
  priceBook.value = undefined;
  quotes.value = [];
  billing.value = undefined;
  notifications.value = [];
  pagination.value.total = 0;
}

async function load() {
  loading.value = true;
  error.value = "";
  clearData();
  try {
    if (section.value === "home") home.value = await customerPortalApi.home();
    else if (section.value === "price-book") priceBook.value = await customerPortalApi.priceBook();
    else if (section.value === "billing") billing.value = await customerPortalApi.billing();
    else if (section.value === "notifications") {
      const result = await customerPortalApi.notifications(pagination.value);
      notifications.value = result.list;
      pagination.value.total = result.total;
    } else {
      const expected = section.value === "contracts" ? "CONTRACT" : "QUOTE";
      const result = await customerPortalApi.quotes({ page: pagination.value.page, size: pagination.value.size, kind: expected,
        status: expected === "QUOTE" ? quoteStatus.value || undefined : undefined });
      quotes.value = result.list;
      pagination.value.total = result.total;
    }
  } catch (value) { error.value = message(value); }
  finally { loading.value = false; }
}

async function acceptQuote(row: PortalCustomerQuote) {
  if (submittingId.value || !canAcceptQuote(row)) return;
  submittingId.value = row.id;
  try {
    await ElMessageBox.confirm(`确认接受报价 #${row.id}？服务端将把报价转为合同价。`, "接受报价", { type: "warning" });
    const result = await customerPortalApi.acceptQuote(row.id);
    row.status = result.newStatus;
    row.canAccept = false;
    await load();
    ElMessage.success(`报价已接受，生成 ${result.contractCount} 条合同价格。`);
  } catch (value) { if (value !== "cancel" && value !== "close") ElMessage.error(message(value)); }
  finally { submittingId.value = ""; }
}

function changePage(page: number) { pagination.value.page = page; void load(); }
watch(section, () => { pagination.value.page = 1; void load(); }, { immediate: true });
</script>

<template>
  <section class="page-heading"><div><div class="eyebrow">CUSTOMER PORTAL</div><h1>{{ titles[section] }}</h1><p>客户数据来自真实接口；价格、状态与操作资格均由服务端决定。</p></div><el-button :loading="loading" @click="load">刷新</el-button></section>
  <el-alert v-if="error" :title="error" type="error" :closable="false"><el-button text @click="load">重新加载</el-button></el-alert>

  <section v-loading="loading" class="panel portal-panel">
    <template v-if="section === 'home' && home">
      <div class="stats"><div><span>待处理报价</span><strong>{{ home.pendingCount }}</strong></div><div><span>未读通知</span><strong>{{ home.unreadNotifications }}</strong></div><div><span>授信额度</span><strong>{{ home.balance.creditLimit }}</strong></div><div><span>已用授信</span><strong>{{ home.balance.creditUsed }}</strong></div></div>
      <h3>常用模型</h3><el-table :data="home.commonModels" border><el-table-column prop="skuCode" label="SKU" /><el-table-column prop="skuId" label="SKU ID" /><el-table-column prop="currency" label="币种" /></el-table>
      <div class="shortcuts"><el-button v-for="(title, key) in titles" v-show="key !== 'home'" :key="key" @click="router.push(`/customer/${key}`)">{{ title }}</el-button></div>
    </template>

    <template v-else-if="section === 'price-book' && priceBook">
      <div class="section-title"><div><h2>当前生效价目表详情</h2><p class="muted">客户等级 {{ priceBook.levelCode }} · 版本 V{{ priceBook.versionNo }} · {{ priceBook.items.length }} 个 SKU</p></div><el-tag>{{ priceBook.currency }}</el-tag></div>
      <el-table :data="priceBook.items" border empty-text="当前生效价目表暂无价格明细"><el-table-column prop="skuCode" label="SKU" min-width="180" /><el-table-column prop="skuId" label="SKU ID" width="110" /><el-table-column prop="unitPrice" label="客户单价" min-width="140" /><el-table-column prop="currency" label="币种" width="100" /></el-table>
    </template>

    <template v-else-if="section === 'quotes' || section === 'contracts'">
      <el-alert title="报价与合同分别按服务端类别查询，分页和总数均为当前类别的真实结果。" type="info" :closable="false" />
      <div v-if="section === 'quotes'" class="portal-filters"><el-select v-model="quoteStatus" clearable placeholder="全部状态"><el-option v-for="status in ['DRAFT','PENDING','APPROVED','EFFECTIVE','EXPIRED','REJECTED']" :key="status" :label="status" :value="status" /></el-select><el-button @click="() => { pagination.page = 1; load(); }">查询</el-button></div>
      <el-empty v-if="!quotes.length && !loading" description="暂无记录" />
      <el-table v-else :data="quotes" row-key="id"><el-table-column prop="id" label="ID" width="90" /><el-table-column label="类型 / 版本" min-width="150"><template #default="{ row }">{{ row.quoteType }} · V{{ row.versionNo }}</template></el-table-column><el-table-column prop="status" label="状态" width="120" /><el-table-column label="金额" min-width="150"><template #default="{ row }">{{ row.totalAmount }} {{ row.currency }}</template></el-table-column><el-table-column prop="itemCount" label="条目" width="80" /><el-table-column label="有效期" min-width="190"><template #default="{ row }">{{ formatTime(row.contractFrom) }} — {{ formatTime(row.contractTo || row.validUntil) }}</template></el-table-column><el-table-column v-if="section === 'quotes'" label="操作" width="110"><template #default="{ row }"><el-button v-if="canAcceptQuote(row)" link type="primary" :loading="submittingId === row.id" :disabled="Boolean(submittingId)" @click="acceptQuote(row)">接受报价</el-button><el-button v-else-if="row.status === 'EFFECTIVE'" disabled>已接受</el-button><span v-else class="muted">不可接受</span></template></el-table-column></el-table>
    </template>

    <template v-else-if="section === 'billing' && billing">
      <el-descriptions :column="2" border><el-descriptions-item label="授信额度">{{ billing.creditLimit }}</el-descriptions-item><el-descriptions-item label="已用授信">{{ billing.creditUsed }}</el-descriptions-item><el-descriptions-item label="押金">{{ billing.depositAmount }}</el-descriptions-item><el-descriptions-item label="押金状态">{{ billing.depositStatus }}</el-descriptions-item><el-descriptions-item label="账期">{{ billing.billingCycle }} 天</el-descriptions-item></el-descriptions>
      <el-alert title="账单明细仍待计费系统接入；当前接口只返回账户与授信概况。" type="info" :closable="false" />
    </template>

    <template v-else-if="section === 'notifications'">
      <el-empty v-if="!notifications.length && !loading" description="暂无通知" />
      <el-timeline v-else><el-timeline-item v-for="item in notifications" :key="item.id" :timestamp="formatTime(item.createdAt)"><b>{{ item.title }}</b><p>{{ item.content }}</p><span class="muted">{{ item.type }} · {{ item.readAt ? '已读' : '未读' }}</span></el-timeline-item></el-timeline>
    </template>

    <el-empty v-else-if="!loading && !error" description="暂无数据" />
    <el-pagination v-if="section === 'quotes' || section === 'contracts' || section === 'notifications'" :current-page="pagination.page" :page-size="pagination.size" :total="pagination.total" layout="total, prev, pager, next" @current-change="changePage" />
  </section>
</template>

<style scoped>
.portal-panel { min-height: 320px; margin-top: 18px; padding: 22px; }.section-title { display: flex; align-items: center; justify-content: space-between; margin-bottom: 18px; }.shortcuts { display: flex; flex-wrap: wrap; gap: 10px; margin-top: 20px; }.shortcuts .el-button { margin-left: 0; }.portal-filters { display: flex; gap: 10px; margin: 14px 0; }.portal-filters .el-select { width: 180px; }.el-pagination { margin-top: 18px; }
</style>
