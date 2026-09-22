<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { supplierQuotesApi } from "../../../api/supplierQuotes";
import { modelsApi } from "../../../api/models";
import type { ModelSkuContractDTO } from "../../../api/models.types";
import type { ExpiringQuoteDTO, QuoteAnomalyDTO, RetroEffectiveRequestDTO } from "../../../api/supplierQuotes.types";
import type { QuoteDiffDTO, QuotePendingDTO, QuoteWriteItem } from "../../../api/quoteContract.types";
import { QUOTE_COMPONENTS } from "../../../api/quoteContract.types";
import { ApiError } from "../../../domain/common";
import { formatDateTime } from "../../../domain/date";
import { isDecimalAmount } from "../../../domain/money";
import { PERMISSIONS } from "../../../domain/permissions";
import { usePermissionStore } from "../../../stores/permission";

const permissions = usePermissionStore();
const tab = ref("pending"), loading = ref(false), error = ref("");
const pending = ref<QuotePendingDTO[]>([]), expiring = ref<ExpiringQuoteDTO[]>([]), anomalies = ref<QuoteAnomalyDTO[]>([]);
const page = reactive({ current: 1, size: 10, total: 0 });
const supplierId = ref(""), days = ref(7), onlySingle = ref(false);
const detailOpen = ref(false), detailLoading = ref(false), selected = ref<QuotePendingDTO>(), detail = ref<QuoteDiffDTO>();
const rejectOpen = ref(false), rejectReason = ref(""), busy = ref(false), retroOpen = ref(false);
const retro = reactive({ supplier_id: "", effective_time: "", valid_to: "", audit_reason: "", items: [] as QuoteWriteItem[] });
const msg = (e: unknown) => e instanceof ApiError || e instanceof Error ? e.message : "请求失败，请重试。";
const canApprove = computed(() => permissions.canAction(PERMISSIONS.SUPPLIER_QUOTE_APPROVE));
const canRemove = computed(() => permissions.canAction(PERMISSIONS.SUPPLIER_QUOTE_EDIT));
const canRetro = computed(() => permissions.canAction(PERMISSIONS.SUPPLIER_QUOTE_PRIVILEGE));
const canReadSkus = computed(() => permissions.canAction(PERMISSIONS.MODEL_VIEW));
const skuChoices = ref<ModelSkuContractDTO[]>([]);
const selectedSkus = reactive(new Map<string, ModelSkuContractDTO>());
const skuKeyword = ref(""), skuLoading = ref(false), skuError = ref(""), skuPage = ref(1), skuTotal = ref(0);
let skuSequence = 0;
async function searchSkus(keyword = "", append = false) {
  if (!canReadSkus.value) return;
  const sequence = ++skuSequence; skuLoading.value = true; skuError.value = "";
  const page = append ? skuPage.value + 1 : 1;
  try {
    const result = await modelsApi.list({ view: "sku", keyword: keyword.trim() || undefined, page, size: 20, lifecycle_status: "PUBLISHED" });
    if (sequence !== skuSequence) return;
    const list = result.list.filter((row): row is ModelSkuContractDTO => "sku_code" in row);
    skuChoices.value = append ? [...skuChoices.value, ...list] : list;
    skuPage.value = page; skuTotal.value = result.total;
  } catch (error) { if (sequence === skuSequence) skuError.value = msg(error); }
  finally { if (sequence === skuSequence) skuLoading.value = false; }
}
function skuLabel(id: string) {
  const sku = selectedSkus.get(id) || skuChoices.value.find(row => String(row.id) === id);
  return sku ? `${sku.vendor_name} / ${sku.family_name} · ${sku.sku_code} · ID ${id}` : `SKU ID ${id}`;
}
function rememberSku(id: string) {
  const sku = skuChoices.value.find(row => String(row.id) === id);
  if (sku) selectedSkus.set(id, sku);
}
const component = () => ({ component_type: "input" as const, multiplier: null, unit_price: "" });
const addItem = () => retro.items.push({ sku_id: "", fx_tier: null, components: [component()] });

async function load() {
  loading.value = true; error.value = "";
  try {
    if (tab.value === "pending") { const r = await supplierQuotesApi.list({ page: page.current, size: page.size, supplier_id: supplierId.value || undefined }); pending.value = r.list; page.total = r.total; }
    else if (tab.value === "expiring") { const r = await supplierQuotesApi.listExpiring({ days: days.value, only_single_point: onlySingle.value || undefined, page: page.current, size: page.size }); expiring.value = r.list; page.total = r.total; }
    else { const r = await supplierQuotesApi.listAnomalies({ days: days.value, page: page.current, size: page.size }); anomalies.value = r.list; page.total = r.total; }
  } catch (e) {
    if (tab.value === "pending") pending.value = [];
    else if (tab.value === "expiring") expiring.value = [];
    else anomalies.value = [];
    page.total = 0;
    error.value = msg(e);
  } finally { loading.value = false; }
}
function switchTab() { page.current = 1; load(); }
async function openDetail(row: QuotePendingDTO) { selected.value = row; detailOpen.value = true; detailLoading.value = true; detail.value = undefined; try { detail.value = await supplierQuotesApi.get(String(row.id)); } catch (e) { ElMessage.error(msg(e)); } finally { detailLoading.value = false; } }
async function approve() { if (!selected.value || busy.value) return; busy.value = true; try { await ElMessageBox.confirm("确认通过整单报价？", "报价审批"); await supplierQuotesApi.approve(String(selected.value.id)); detailOpen.value = false; ElMessage.success("审批完成"); await load(); } catch (e) { if (e !== "cancel" && e !== "close") ElMessage.error(msg(e)); } finally { busy.value = false; } }
async function reject() { if (!selected.value || busy.value) return; const reason = rejectReason.value.trim(); if (Array.from(reason).length < 10) return void ElMessage.warning("驳回原因至少10字。"); busy.value = true; try { await supplierQuotesApi.reject(String(selected.value.id), reason); rejectOpen.value = detailOpen.value = false; ElMessage.success("已驳回"); await load(); } catch (e) { ElMessage.error(msg(e)); } finally { busy.value = false; } }
async function remove(row: ExpiringQuoteDTO) { if (busy.value) return; busy.value = true; try { const r = await ElMessageBox.prompt("请输入移除原因", "确认移除到期报价", { inputPattern: /\S+/, inputErrorMessage: "请填写原因" }); await supplierQuotesApi.confirmRemove(String(row.id), r.value); ElMessage.success("已确认移除"); await load(); } catch (e) { if (e !== "cancel" && e !== "close") ElMessage.error(msg(e)); } finally { busy.value = false; } }
function openRetro() { Object.assign(retro, { supplier_id: "", effective_time: "", valid_to: "", audit_reason: "", items: [] }); addItem(); retroOpen.value = true; }
function payload(): RetroEffectiveRequestDTO {
  if (!/^\d+$/.test(retro.supplier_id) || retro.supplier_id === "0") throw new Error("供应商ID必须是正整数。");
  if (!retro.effective_time || !retro.valid_to || Date.parse(retro.effective_time) >= Date.now() || Date.parse(retro.valid_to) <= Date.parse(retro.effective_time)) throw new Error("请填写正确的过去生效时间和结束时间。");
  if (Array.from(retro.audit_reason.trim()).length < 10) throw new Error("审计原因至少10字。");
  if (!retro.items.length) throw new Error("至少添加一个SKU。");
  retro.items.forEach(i => { if (!/^\d+$/.test(String(i.sku_id)) || !i.components.length) throw new Error("SKU ID和价格组件不能为空。"); i.components.forEach(c => { if (!isDecimalAmount(c.unit_price) || c.multiplier !== null && !isDecimalAmount(c.multiplier)) throw new Error("价格或倍率格式不正确。"); }); });
  return { supplier_id: retro.supplier_id, effective_time: new Date(retro.effective_time).toISOString(), valid_to: new Date(retro.valid_to).toISOString(), audit_reason: retro.audit_reason.trim(), items: retro.items };
}
async function submitRetro() { if (busy.value) return; try { const body = payload(); busy.value = true; const r = await supplierQuotesApi.retroEffective(body); retroOpen.value = false; await ElMessageBox.alert(`报价 ${r.id} 已生效；成本重算${r.cost_recalc_queued ? "已入队" : "未入队"}；本月补录 ${r.retro_count_this_month} 次${r.alert_created ? "，已生成告警" : ""}。`, "特权补录结果"); await load(); } catch (e) { if (e !== "close") ElMessage.error(msg(e)); } finally { busy.value = false; } }
onMounted(load);
</script>

<template>
<section class="page-heading"><div><div class="eyebrow">SUPPLIER QUOTES</div><h1>供应商报价管理</h1><p>审批普通报价，查看到期风险与异常，并处理特权补录。</p></div><el-button v-if="canRetro" type="danger" @click="openRetro">特权补录</el-button></section>
<section class="panel quote-panel">
  <el-tabs v-model="tab" @tab-change="switchTab"><el-tab-pane label="待审批" name="pending"/><el-tab-pane label="到期管理" name="expiring"/><el-tab-pane label="异常报价" name="anomalies"/></el-tabs>
  <div class="filters"><el-input v-if="tab==='pending'" v-model="supplierId" placeholder="供应商ID" clearable/><template v-else><el-input-number v-model="days" :min="1" :max="365"/><span>统计天数</span></template><el-checkbox v-if="tab==='expiring'" v-model="onlySingle">仅单点供应</el-checkbox><el-button type="primary" @click="page.current=1;load()">查询</el-button><el-button v-if="tab==='pending'" @click="supplierId='';page.current=1;load()">重置</el-button><el-button @click="load">刷新</el-button></div>
  <el-alert v-if="error" :title="error" type="error" :closable="false"/>
  <el-table v-if="tab==='pending'" v-loading="loading" :data="pending" :empty-text="error ? '加载失败' : '暂无待审批报价'"><el-table-column label="供应商 / 版本"><template #default="{row}"><b>{{row.supplier_name}}</b><div>V{{row.version_no}} · {{row.item_count}}个SKU</div></template></el-table-column><el-table-column label="来源"><template #default="{row}">{{row.source}} <el-tag v-if="row.retroactive" type="warning">补录</el-tag></template></el-table-column><el-table-column label="有效期"><template #default="{row}">{{formatDateTime(row.valid_from)}}<div>{{formatDateTime(row.valid_to)}}</div></template></el-table-column><el-table-column label="已等待"><template #default="{row}">{{row.wait_hours}}小时</template></el-table-column><el-table-column label="操作"><template #default="{row}"><el-button @click="openDetail(row)">查看并审批</el-button></template></el-table-column></el-table>
  <el-table v-else-if="tab==='expiring'" v-loading="loading" :data="expiring" :empty-text="error ? '加载失败' : '暂无到期报价'"><el-table-column prop="supplier_name" label="供应商"/><el-table-column prop="version_no" label="版本"/><el-table-column label="到期"><template #default="{row}">{{formatDateTime(row.valid_to)}}<div>{{row.days_left}}天</div></template></el-table-column><el-table-column label="风险"><template #default="{row}"><el-tag :type="row.alert_level==='URGENT'?'danger':row.alert_level==='HIGH'?'warning':'info'">{{row.alert_level}}</el-tag> <el-tag v-if="row.single_point" type="danger">单点</el-tag></template></el-table-column><el-table-column label="状态"><template #default="{row}">{{row.in_grace?'宽限期内':'宽限期结束'}}</template></el-table-column><el-table-column label="操作"><template #default="{row}"><el-button v-if="row.pending_remove&&canRemove" type="danger" :loading="busy" :disabled="busy" @click="remove(row)">确认移除</el-button><span v-else>{{row.remove_confirmed?'已移除':'无需操作'}}</span></template></el-table-column></el-table>
  <el-table v-else v-loading="loading" :data="anomalies" :empty-text="error ? '加载失败' : '暂无异常报价'"><el-table-column prop="supplier_name" label="供应商"/><el-table-column prop="sku_code" label="SKU"/><el-table-column prop="component_type" label="组件"/><el-table-column label="当前 / 上次"><template #default="{row}">{{row.unit_price}} / {{row.prev_price??'无'}}<div>{{row.delta_pct??'—'}}</div></template></el-table-column><el-table-column label="市场最低"><template #default="{row}">{{row.market_best??'无'}}<div>{{row.mkt_delta_pct??'—'}}</div></template></el-table-column><el-table-column prop="reason" label="原因"/><el-table-column label="发现时间"><template #default="{row}">{{formatDateTime(row.detected_at)}}</template></el-table-column></el-table>
  <el-pagination v-model:current-page="page.current" :page-size="page.size" :total="page.total" layout="total, prev, pager, next" @current-change="load"/>
</section>
<el-drawer v-model="detailOpen" title="报价审批" size="min(1000px,96vw)"><div v-loading="detailLoading"><template v-if="detail&&selected"><h2>{{selected.supplier_name}} · V{{selected.version_no}}</h2><el-alert v-if="detail.distortion" :title="detail.distortion_note||'组件倍率异常'" type="warning" :closable="false"/><article v-for="item in detail.items" :key="String(item.sku_id)"><h3>{{item.sku_code}} · {{item.currency}}</h3><el-table :data="item.components"><el-table-column prop="component_type" label="组件"/><el-table-column prop="unit_price" label="供应价"/><el-table-column prop="prev_price" label="上一版"/><el-table-column prop="official_price" label="官方价"/><el-table-column prop="market_best" label="市场最低"/><el-table-column label="倍率"><template #default="{row}">{{row.multiplier??'绝对价'}}</template></el-table-column></el-table><el-descriptions :column="2" border><el-descriptions-item label="floor">{{item.margin_preview.floor_price??'未提供'}}</el-descriptions-item><el-descriptions-item label="参考售价">{{item.margin_preview.reference_sell_price??'未提供'}}</el-descriptions-item><el-descriptions-item label="毛利预演">{{item.margin_preview.margin_ok===null?'无法判断':item.margin_preview.margin_ok?'满足':'不满足'}}</el-descriptions-item><el-descriptions-item label="依据">{{item.margin_preview.note}}</el-descriptions-item></el-descriptions></article><p class="muted">审批动作完成后会重新查询队列，最终生命周期以服务端状态为准。</p></template></div><template #footer><el-button v-if="canApprove" :disabled="busy" @click="rejectReason='';rejectOpen=true">驳回</el-button><el-button v-if="canApprove" type="primary" :loading="busy" :disabled="busy" @click="approve">通过整单</el-button></template></el-drawer>
<el-dialog v-model="rejectOpen" title="驳回报价"><el-input v-model="rejectReason" type="textarea" maxlength="500"/><template #footer><el-button @click="rejectOpen=false">取消</el-button><el-button type="danger" :loading="busy" :disabled="busy" @click="reject">确认驳回</el-button></template></el-dialog>
<el-dialog v-model="retroOpen" title="特权补录已生效报价" width="min(920px,96vw)" :close-on-click-modal="!busy"><el-alert title="此操作会直接生成 EFFECTIVE 报价并留下审计记录，仅限 M4:P 专门账号。" type="warning" :closable="false"/><el-form label-position="top" class="retro-form"><div class="grid"><el-form-item label="供应商ID"><el-input v-model="retro.supplier_id"/></el-form-item><el-form-item label="过去的生效时间"><el-date-picker v-model="retro.effective_time" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ"/></el-form-item><el-form-item label="结束时间"><el-date-picker v-model="retro.valid_to" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ"/></el-form-item></div><el-form-item label="审计原因"><el-input v-model="retro.audit_reason" type="textarea" maxlength="500" show-word-limit placeholder="至少10字"/></el-form-item><div v-for="(item,i) in retro.items" :key="i" class="retro-item"><div class="item-head"><b>SKU {{i+1}}</b><el-button v-if="retro.items.length>1" text type="danger" @click="retro.items.splice(i,1)">删除SKU</el-button></div><div class="grid"><el-form-item label="SKU ID"><el-select v-if="canReadSkus" v-model="item.sku_id" filterable remote clearable :remote-method="(value: string) => { skuKeyword = value; searchSkus(value); }" :loading="skuLoading" @visible-change="(open: boolean) => { if (open && !skuChoices.length) searchSkus(); }" @change="rememberSku"><el-option v-for="sku in [...skuChoices, ...selectedSkus.values()]" :key="String(sku.id)" :label="skuLabel(String(sku.id))" :value="String(sku.id)"/></el-select><el-input v-else v-model="item.sku_id"/></el-form-item><el-form-item label="汇率档位（可选）"><el-input v-model="item.fx_tier" clearable/></el-form-item></div><div v-for="(c,j) in item.components" :key="j" class="component"><el-select v-model="c.component_type"><el-option v-for="name in QUOTE_COMPONENTS" :key="name" :label="name" :value="name"/></el-select><el-input v-model="c.unit_price" placeholder="供应价"/><el-input v-model="c.multiplier" placeholder="倍率（可空）" clearable/><el-button v-if="item.components.length>1" text type="danger" @click="item.components.splice(j,1)">删除</el-button></div><el-button text type="primary" @click="item.components.push(component())">+ 添加价格组件</el-button></div><el-button @click="addItem">+ 添加SKU</el-button><p class="muted">本次 SKU：{{ retro.items.map(item => skuLabel(String(item.sku_id))).join("；") }}</p><el-alert v-if="skuError" :title="skuError" type="error" :closable="false"><el-button @click="searchSkus(skuKeyword)">重试</el-button></el-alert><el-button v-if="canReadSkus && skuChoices.length < skuTotal" text :loading="skuLoading" @click="searchSkus(skuKeyword, true)">加载更多 SKU</el-button></el-form><template #footer><el-button @click="retroOpen=false">取消</el-button><el-button type="danger" :loading="busy" :disabled="busy" @click="submitRetro">确认特权补录</el-button></template></el-dialog>
</template>
<style scoped>.quote-panel{padding:22px}.filters{display:flex;align-items:center;gap:12px;margin-bottom:18px}.filters .el-input{width:240px}article{margin:24px 0}.retro-form{margin-top:18px}.grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px}.retro-item{padding:14px;margin:12px 0;border:1px solid var(--el-border-color);border-radius:8px}.item-head,.component{display:flex;align-items:center;gap:10px;margin-bottom:10px}.item-head{justify-content:space-between}.component>*{flex:1}@media(max-width:760px){.grid{grid-template-columns:1fr}.component{flex-direction:column;align-items:stretch}}</style>
