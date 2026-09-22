<script setup lang="ts">
import { onMounted, ref } from "vue";
import Decimal from "decimal.js";
import { ElMessage, ElMessageBox } from "element-plus";
import { useRoute, useRouter } from "vue-router";
import { supplierQuoteApi } from "../../api/quoteContract";
import { QUOTE_COMPONENTS, QUOTE_FX_TIERS } from "../../api/quoteContract.types";
import type { QuoteWriteItem, SupplierSkuDTO, QuoteComponentType } from "../../api/quoteContract.types";
import { quoteFormError } from "../../domain/quoteForm";
import { ApiError } from "../../domain/common";
import { isDecimalAmount } from "../../domain/money";
import { effectiveTimeWillClamp } from "../../domain/quoteImport";
const router = useRouter(), route = useRoute();
const skus = ref<SupplierSkuDTO[]>([]), items = ref<QuoteWriteItem[]>([]);
const selected = ref(""), loading = ref(false), submitting = ref(false), error = ref("");
const validFrom = ref(""), validTo = ref(""), remark = ref("");
const skuFor = (id: string | number) => skus.value.find(sku => String(sku.id) === String(id));
const official = (item: QuoteWriteItem, type: QuoteComponentType) => skuFor(item.sku_id)?.official_price?.components.find(component => component.component_type === type)?.unit_price;
async function load() {
    loading.value = true;
    error.value = "";
    skus.value = [];
    items.value = [];
    try {
        let page = 1, total = 0;
        do {
            const result = await supplierQuoteApi.skus({ page: page++, size: 100 });
            total = result.total;
            skus.value.push(...result.list);
            if (!result.list.length && skus.value.length < total)
                throw new Error("SKU分页不完整");
        } while (skus.value.length < total);
        if (typeof route.params.id === "string") {
            if (!route.path.endsWith("/renew"))
                throw new ApiError(409, "接口未提供持久草稿编辑，请返回历史创建新版本。");
            const source = await supplierQuoteApi.detail(route.params.id);
            if (String(source.id) !== route.params.id || !["EFFECTIVE", "EXPIRED", "VOIDED", "REJECTED"].includes(source.status))
                throw new ApiError(409, "当前版本不能续报。");
            remark.value = source.remark || source.audit_reason || "";
            items.value = source.items.map(item => ({ sku_id: item.sku_id, fx_tier: item.fx_tier, constraints: item.constraints ? { ...item.constraints } : {}, components: item.components.map(component => ({ ...component })) }));
        }
    }
    catch (value) {
        error.value = value instanceof ApiError ? value.message : "SKU或历史版本加载失败，请重试。";
    }
    finally {
        loading.value = false;
    }
}
function add() {
    const sku = skuFor(selected.value);
    if (!sku || items.value.some(item => String(item.sku_id) === String(sku.id)))
        return;
    items.value.push({ sku_id: sku.id, fx_tier: sku.native_currency === "USD" ? "6.8" : null, constraints: {}, components: (sku.official_price?.components || [{ component_type: "input" as const, unit_price: "" }]).map(component => ({ component_type: component.component_type, multiplier: null, unit_price: "" })) });
    selected.value = "";
}
function calculate(item: QuoteWriteItem, index: number) {
    const component = item.components[index], price = official(item, component.component_type);
    if (component.multiplier === null || !price)
        return;
    try {
        const multiplier = new Decimal(component.multiplier);
        component.unit_price = multiplier.isFinite() && multiplier.gt(0) ? new Decimal(price).times(multiplier).toFixed(8) : "";
    }
    catch {
        component.unit_price = "";
    }
}
const bulkMultiplier = ref("");
async function applyBulkMultiplier() {
    if (submitting.value || loading.value || !items.value.length) return;
    if (!isDecimalAmount(bulkMultiplier.value)) { error.value = "请填写有效整单倍率。"; return; }
    const multiplier = bulkMultiplier.value;
    submitting.value = true;
    try {
        await ElMessageBox.confirm("按此倍率替换所有有官方基准的组件价格？无基准组件保留绝对价。", "整单倍率", { confirmButtonText: "确认应用" });
        for (const item of items.value) item.components.forEach((component, index) => {
            if (official(item, component.component_type)) { component.multiplier = multiplier; calculate(item, index); }
        });
    } catch (value) { if (value !== "cancel" && value !== "close") error.value = "倍率应用失败，请逐项核对。"; }
    finally { submitting.value = false; }
}
async function submit() {
    if (loading.value || submitting.value)
        return;
    const payload = { valid_from: validFrom.value, valid_to: validTo.value, remark: remark.value.trim() || undefined,
        items: items.value.map(item => ({ sku_id: item.sku_id, fx_tier: skuFor(item.sku_id)?.native_currency === "USD" ? item.fx_tier : null, constraints: { ...item.constraints }, components: item.components.map(component => ({ ...component, unit_price: component.unit_price.trim() })) })) };
    error.value = quoteFormError(payload);
    if (items.value.some(item => !skuFor(item.sku_id)))
        error.value = "历史SKU已不可报价，请移除后重新选择。";
    if (items.value.some(item => skuFor(item.sku_id)?.native_currency === "USD" && !item.fx_tier))
        error.value = "USD模型必须选择汇率档位。";
    if (error.value)
        return;
    submitting.value = true;
    try {
        const pastNotice = effectiveTimeWillClamp(payload.valid_from) ? " 生效时间早于当前时间，后端会将其调整为提交时刻。" : "";
        await ElMessageBox.confirm(`提交${payload.items.length}个SKU的新版本？提交后进入审批，不能直接修改。${pastNotice}`, "提交报价", { confirmButtonText: "确认提交" });
        const result = await supplierQuoteApi.submit(payload);
        if (result.clamped) await ElMessageBox.alert(`后端已将生效时间调整为 ${result.valid_from}。请以该时间为准。`, "生效时间已调整", { confirmButtonText: "知道了", showClose: false, closeOnClickModal: false, closeOnPressEscape: false });
        else ElMessage.success("已提交审批。");
        await router.push("/supplier/quotes");
    }
    catch (value) {
        if (value !== "cancel" && value !== "close")
            error.value = value instanceof ApiError ? value.message : "提交失败，请重试。";
    }
    finally {
        submitting.value = false;
    }
}
onMounted(load);
</script>

<template>
<section class="page-heading">
  <div>
    <div class="eyebrow">SUPPLIER QUOTE</div>
    <h1>{{ route.params.id ? '续报新版本' : '新建报价' }}</h1>
    <p>选择已有模型，仅维护供应商商业条件。提交后进入采购审批。</p>
  </div>
  <el-button :disabled="submitting" @click="router.push('/supplier/quotes')">返回报价历史</el-button>
</section>
<section v-loading="loading" class="panel quote-form">
  <el-alert v-if="error" :title="error" type="error" :closable="false" />
  <el-button v-if="error && !skus.length" @click="load">重新加载</el-button>
  <el-alert title="当前接口不提供持久草稿保存；离开页面不会保存未提交内容。" type="info" :closable="false" />
  <el-form label-position="top" :disabled="submitting || loading">
    <div class="dates">
      <el-form-item label="生效时间" required>
        <el-date-picker v-model="validFrom" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" />
      </el-form-item>
      <el-form-item label="结束时间" required>
        <el-date-picker v-model="validTo" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" />
      </el-form-item>
    </div>
    <div class="picker">
      <el-select v-model="selected" filterable placeholder="选择可报价SKU">
        <el-option
          v-for="sku in skus.filter(sku => !items.some(item => String(item.sku_id) === String(sku.id)))"
          :key="String(sku.id)"
          :label="`${sku.model_name} · ${sku.sku_code}`"
          :value="String(sku.id)"
         />
      </el-select>
      <el-button @click="add">添加SKU</el-button>
    </div>
    <div class="picker">
      <el-input v-model="bulkMultiplier" aria-label="整单倍率" placeholder="整单倍率" />
      <el-button @click="applyBulkMultiplier">应用整单倍率</el-button>
    </div>
    <article v-for="(item, index) in items" :key="String(item.sku_id)" class="sku-quote">
      <div class="section-title">
        <h2>
          {{ skuFor(item.sku_id)?.model_name || '已不可报价SKU' }}
          <small>{{ skuFor(item.sku_id)?.native_currency }}</small>
        </h2>
        <el-button @click="items.splice(index, 1)">移除SKU</el-button>
      </div>
      <el-form-item v-if="skuFor(item.sku_id)?.native_currency === 'USD'" label="汇率档位" required>
        <el-select v-model="item.fx_tier">
          <el-option v-for="tier in QUOTE_FX_TIERS" :key="tier" :value="tier" :label="tier" />
        </el-select>
      </el-form-item>
      <el-table :data="item.components">
        <el-table-column label="组件">
          <template #default="{ row }">
            <el-select v-model="row.component_type" :disabled="submitting">
              <el-option v-for="type in QUOTE_COMPONENTS" :key="type" :value="type" :label="type" />
            </el-select>
          </template>
        </el-table-column>
        <el-table-column label="官方价（只读）">
          <template #default="{ row }">{{ official(item, row.component_type) || '无基准，仅绝对价' }}</template>
        </el-table-column>
        <el-table-column label="倍率（空为绝对价）">
          <template #default="{ row, $index }">
            <el-input
              :model-value="row.multiplier || ''"
              :disabled="submitting || !official(item, row.component_type)"
              @update:model-value="row.multiplier = $event || null; calculate(item, $index)"
             />
          </template>
        </el-table-column>
        <el-table-column label="供应价">
          <template #default="{ row }">
            <el-input v-model="row.unit_price" :disabled="submitting" @input="row.multiplier = null" />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="90">
          <template #default="{ $index }">
            <el-button :disabled="submitting" @click="item.components.splice($index, 1)">移除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-button
        @click="item.components.push({ component_type: 'input', multiplier: null, unit_price: '' })"
      >添加组件</el-button>
      <div v-if="item.constraints" class="constraints">
        <el-form-item
          v-for="key in (['rpm','tpm','concurrency','daily_quota','actual_context'] as const)"
          :key="key"
          :label="key"
        >
          <el-input-number v-model="item.constraints[key]" :min="1" :precision="0" :value-on-clear="null" />
        </el-form-item>
        <el-form-item label="兼容说明">
          <el-input
            :model-value="item.constraints.compatibility || ''"
            @update:model-value="item.constraints.compatibility = $event"
           />
        </el-form-item>
      </div>
    </article>
    <el-form-item label="报价说明">
      <el-input v-model="remark" type="textarea" maxlength="500" show-word-limit />
    </el-form-item>
    <el-button type="primary" :loading="submitting" @click="submit">提交审批</el-button>
  </el-form>
</section>
</template>

<style scoped>
.quote-form {
  padding:22px
}
.dates,.picker,.constraints {
  display:flex;
  gap:16px;
  flex-wrap:wrap;
  margin:18px 0
}
.picker .el-select {
  width:440px
}
.sku-quote {
  border:1px solid var(--app-border);
  padding:18px;
  margin:18px 0;
  border-radius:10px
}
.constraints .el-form-item {
  width:180px
}
</style>
