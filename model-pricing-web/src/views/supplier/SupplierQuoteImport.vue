<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { ElMessage, ElMessageBox, type UploadFile } from "element-plus";
import { supplierQuoteApi } from "../../api/quoteContract";
import type { QuoteComponentType, QuoteImportPreviewDTO, QuoteWriteItem, SupplierSkuDTO } from "../../api/quoteContract.types";
import { QUOTE_FX_TIERS } from "../../api/quoteContract.types";
import { quoteFormError } from "../../domain/quoteForm";
import { ApiError } from "../../domain/common";
import { effectiveTimeWillClamp, filterPreviewByScope, importedPrice, includedPreviewItems, quoteScopeOptions } from "../../domain/quoteImport";
const router = useRouter(), scope = ref<"history" | "active" | "vendor" | "family">("active");
const preview = ref<QuoteImportPreviewDTO>(), error = ref(""), file = ref<File>();
const validFrom = ref(""), validTo = ref(""), remark = ref(""), downloading = ref(false), validating = ref(false), committing = ref(false);
const skus = ref<SupplierSkuDTO[]>([]), officialPriceError = ref(""), excludedSkuIds = ref(new Set<string>());
const scopeSkus = ref<SupplierSkuDTO[]>([]), scopeOptionsLoading = ref(false), scopeOptionsError = ref("");
const selectedVendorId = ref(""), selectedFamilyId = ref("");
const sourceRowCount = ref(0), scopeExcludedCount = ref(0);
const scopeOptions = computed(() => quoteScopeOptions(scopeSkus.value));
const familyOptions = computed(() => scopeOptions.value.families.filter(option => String(option.vendorId) === selectedVendorId.value));
const selectedVendor = computed(() => scopeOptions.value.vendors.find(option => String(option.id) === selectedVendorId.value));
const selectedFamily = computed(() => scopeOptions.value.families.find(option => String(option.id) === selectedFamilyId.value));
const templateRange = computed(() => scope.value === "vendor" ? selectedVendor.value?.name : scope.value === "family" && selectedFamily.value
    ? `${selectedFamily.value.vendorName} / ${selectedFamily.value.name}` : "");
const includedItems = computed(() => includedPreviewItems(preview.value?.preview_items || [], excludedSkuIds.value));
const excludedCount = computed(() => (preview.value?.preview_items.length || 0) - includedItems.value.length);
const skuFor = (id: string | number) => skus.value.find(sku => String(sku.id) === String(id));
const official = (item: QuoteWriteItem, type: QuoteComponentType) => skuFor(item.sku_id)?.official_price?.components.find(component => component.component_type === type)?.unit_price;
watch(scope, () => {
    preview.value = undefined;
    sourceRowCount.value = 0;
    scopeExcludedCount.value = 0;
    selectedVendorId.value = "";
    selectedFamilyId.value = "";
});
watch(selectedVendorId, () => {
    preview.value = undefined;
    sourceRowCount.value = 0;
    scopeExcludedCount.value = 0;
    if (selectedFamily.value && String(selectedFamily.value.vendorId) !== selectedVendorId.value)
        selectedFamilyId.value = "";
});
watch(selectedFamilyId, () => {
    preview.value = undefined;
    sourceRowCount.value = 0;
    scopeExcludedCount.value = 0;
});
async function loadScopeOptions() {
    if (scopeOptionsLoading.value) return;
    scopeOptionsLoading.value = true;
    scopeOptionsError.value = "";
    try {
        const collected: SupplierSkuDTO[] = [];
        let page = 1, total = 0;
        do {
            const result = await supplierQuoteApi.skus({ page, size: 100 });
            collected.push(...result.list);
            total = result.total;
            if (!result.list.length) break;
            page += 1;
        } while (collected.length < total);
        scopeSkus.value = collected;
    }
    catch (value) {
        scopeSkus.value = [];
        scopeOptionsError.value = value instanceof ApiError ? value.message : "厂商与系列选项加载失败。";
    }
    finally {
        scopeOptionsLoading.value = false;
    }
}
onMounted(() => void loadScopeOptions());
function exclude(skuId: string | number) {
    excludedSkuIds.value = new Set([...excludedSkuIds.value, String(skuId)]);
}
function restore(skuId: string | number) {
    const next = new Set(excludedSkuIds.value);
    next.delete(String(skuId));
    excludedSkuIds.value = next;
}
function calculate(item: QuoteWriteItem, index: number) {
    const component = item.components[index], price = official(item, component.component_type);
    if (component.multiplier === null || !price) return;
    component.unit_price = importedPrice(price, component.multiplier);
}
function setMode(item: QuoteWriteItem, index: number, mode: "MULTIPLIER" | "ABSOLUTE") {
    const component = item.components[index];
    if (mode === "ABSOLUTE") { component.multiplier = null; return; }
    if (!official(item, component.component_type)) return;
    component.multiplier = component.multiplier || "1";
    calculate(item, index);
}
async function loadOfficialPrices() {
    skus.value = [];
    officialPriceError.value = "";
    if (!preview.value) return;
    const failed: string[] = [];
    for (const item of preview.value.preview_items) {
        const row = preview.value.rows.find(candidate => candidate.level !== "ERROR" && String(candidate.sku_id) === String(item.sku_id));
        if (!row?.sku_code) { failed.push(String(item.sku_id)); continue; }
        try {
            const result = await supplierQuoteApi.skus({ page: 1, size: 100, keyword: row.sku_code });
            const sku = result.list.find(candidate => String(candidate.id) === String(item.sku_id));
            if (sku) skus.value.push(sku);
            else failed.push(row.sku_code);
        } catch {
            failed.push(row.sku_code);
        }
    }
    if (failed.length) officialPriceError.value = `部分当前官方价加载失败（${failed.join("、")}），对应组件暂时只能按绝对价编辑；可重新预检后再试。`;
}
function selectFile(upload: UploadFile) {
    if (validating.value || committing.value)
        return;
    preview.value = undefined;
    sourceRowCount.value = 0;
    scopeExcludedCount.value = 0;
    excludedSkuIds.value = new Set();
    error.value = "";
    file.value = undefined;
    if (!upload.raw || !upload.name.toLowerCase().endsWith(".csv") || upload.raw.size > 2 * 1024 * 1024) {
        error.value = "请选择2MB以内的CSV文件。";
        return;
    }
    file.value = upload.raw;
}
async function download() {
    if (downloading.value)
        return;
    if (scope.value === "vendor" && !selectedVendorId.value) {
        error.value = "请选择厂商。";
        return;
    }
    if (scope.value === "family" && (!selectedVendorId.value || !selectedFamilyId.value)) {
        error.value = "请先选择厂商和系列。";
        return;
    }
    error.value = "";
    downloading.value = true;
    try {
        const blob = await supplierQuoteApi.template({ scope: scope.value, vendor_id: scope.value === "vendor" ? selectedVendorId.value : undefined, family_id: scope.value === "family" ? selectedFamilyId.value : undefined });
        const url = URL.createObjectURL(blob), anchor = document.createElement("a");
        anchor.href = url;
        anchor.download = "supplier-quotes.csv";
        anchor.click();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
    }
    catch (value) {
        error.value = value instanceof ApiError ? value.message : "模板下载失败。";
    }
    finally {
        downloading.value = false;
    }
}
async function validate() {
    if (!file.value || validating.value || committing.value)
        return;
    validating.value = true;
    error.value = "";
    preview.value = undefined;
    try {
        const rawPreview = await supplierQuoteApi.preview(file.value);
        sourceRowCount.value = rawPreview.total;
        const filtered = filterPreviewByScope(rawPreview, scopeSkus.value, scope.value, selectedVendorId.value, selectedFamilyId.value);
        preview.value = filtered.preview;
        scopeExcludedCount.value = filtered.excluded;
        excludedSkuIds.value = new Set();
        if ((scope.value === "vendor" || scope.value === "family") && !preview.value.rows.length) {
            error.value = `上传文件中没有属于“${templateRange.value}”的 SKU。`;
            return;
        }
        await loadOfficialPrices();
    }
    catch (value) {
        error.value = value instanceof ApiError ? value.message : "预检失败。";
    }
    finally {
        validating.value = false;
    }
}
async function confirm() {
    if (!preview.value || validating.value || committing.value)
        return;
    const payload = { valid_from: validFrom.value, valid_to: validTo.value, remark: remark.value.trim() || undefined,
        items: includedItems.value.map(item => ({ sku_id: item.sku_id, fx_tier: item.currency === 'USD' ? item.fx_tier : null,
            constraints: { ...item.constraints }, components: item.components.map(component => ({ ...component, unit_price: component.unit_price.trim() })) })) };
    error.value = quoteFormError(payload);
    if (payload.items.some(item => preview.value!.preview_items.find(row => String(row.sku_id) === String(item.sku_id))?.currency === 'USD' && !item.fx_tier))
        error.value = "USD模型必须选择汇率档位。";
    if (error.value)
        return;
    committing.value = true;
    try {
        const pastNotice = effectiveTimeWillClamp(payload.valid_from) ? " 生效时间早于当前时间，后端会将其调整为提交时刻。" : "";
        await ElMessageBox.confirm(`确认提交${payload.items.length}个SKU进入审批？预览警告${preview.value.warn_count}条，错误${preview.value.error_count}条不会提交，手动排除${excludedCount.value}条。${pastNotice}`, "确认导入", { confirmButtonText: "确认提交" });
        const result = await supplierQuoteApi.confirm(payload);
        if (result.clamped) await ElMessageBox.alert(`后端已将生效时间调整为 ${result.valid_from}。请以该时间为准。`, "生效时间已调整", { confirmButtonText: "知道了", showClose: false, closeOnClickModal: false, closeOnPressEscape: false });
        else ElMessage.success("导入已进入审批。");
        await router.push("/supplier/quotes");
    }
    catch (value) {
        if (value !== "cancel" && value !== "close")
            error.value = value instanceof ApiError ? value.message : "导入提交失败。";
    }
    finally {
        committing.value = false;
    }
}
</script>

<template>
<section class="page-heading">
  <div>
    <div class="eyebrow">QUOTE IMPORT</div>
    <h1>批量导入</h1>
    <p>下载模板 → 上传CSV → 逐行预检与编辑 → 提交审批。</p>
  </div>
  <el-button :disabled="committing" @click="router.push('/supplier/quotes')">返回报价历史</el-button>
</section>
<section class="panel import-panel">
  <el-alert title="一个文件对应一个报价版本；可上传包含多个厂商或系列的 CSV，预检会按当前范围筛选。有效期在此填写，不写入CSV。错误行不会提交。" type="info" :closable="false" />
  <el-alert v-if="error" :title="error" type="error" :closable="false" />
  <el-alert v-if="scopeOptionsError" :title="`${scopeOptionsError} 厂商和系列范围暂不可选。`" type="warning" :closable="false" />
  <div class="actions">
    <el-select v-model="scope">
      <el-option label="全部可报价SKU" value="active" />
      <el-option label="历史报价SKU" value="history" />
      <el-option label="指定厂商" value="vendor" />
      <el-option label="指定系列" value="family" />
    </el-select>
    <el-select v-if="scope === 'vendor' || scope === 'family'" v-model="selectedVendorId" filterable placeholder="选择厂商" :loading="scopeOptionsLoading" :disabled="!!scopeOptionsError">
      <el-option v-for="option in scopeOptions.vendors" :key="String(option.id)" :label="option.name" :value="String(option.id)" />
    </el-select>
    <el-select v-if="scope === 'family'" v-model="selectedFamilyId" filterable placeholder="选择系列" :loading="scopeOptionsLoading" :disabled="!selectedVendorId || !!scopeOptionsError">
      <el-option v-for="option in familyOptions" :key="String(option.id)" :label="option.name" :value="String(option.id)" />
    </el-select>
    <el-button v-if="scopeOptionsError" :loading="scopeOptionsLoading" @click="loadScopeOptions">重新加载选项</el-button>
    <span v-if="templateRange" class="range-label">当前范围：{{ templateRange }}</span>
    <el-button :loading="downloading" :disabled="scopeOptionsLoading || (scope === 'vendor' && !selectedVendorId) || (scope === 'family' && !selectedFamilyId)" @click="download">下载CSV模板</el-button>
    <el-upload
      accept=".csv"
      :disabled="validating || committing"
      :auto-upload="false"
      :show-file-list="false"
      :on-change="selectFile"
    >
      <el-button>选择CSV文件</el-button>
    </el-upload>
    <span>{{ file?.name }}</span>
    <el-button :disabled="!file || committing" :loading="validating" @click="validate">开始预检</el-button>
  </div>
  <template v-if="preview">
    <el-alert v-if="scopeExcludedCount" :title="`已按“${templateRange}”筛选：原文件 ${sourceRowCount} 行，保留 ${preview.total} 行，排除其他范围 ${scopeExcludedCount} 行。`" type="success" :closable="false" />
    <p>总行数 {{ preview.total }} · 通过 {{ preview.ok_count }} · 警告 {{ preview.warn_count }} · 错误 {{ preview.error_count }} · 手动排除 {{ excludedCount }} · 最终提交 {{ includedItems.length }}</p>
    <el-alert v-if="preview.warn_count" title="预检发现参考列或价格基准变化，请查看每行说明；警告行仍可提交，系统以库内当前值为准。" type="warning" :closable="false" />
    <el-alert v-if="preview.error_count" title="错误行不会提交；请核对下表后提交其余有效行，或修改原文件重新预检。" type="warning" :closable="false" />
    <el-alert v-if="officialPriceError" :title="officialPriceError" type="warning" :closable="false" />
    <el-table :data="preview.rows">
      <el-table-column prop="line" label="行" />
      <el-table-column prop="sku_code" label="SKU" />
      <el-table-column label="结果"><template #default="{ row }"><el-tag :type="row.level === 'ERROR' ? 'danger' : row.level === 'WARN' ? 'warning' : 'success'">{{ row.level }}</el-tag></template></el-table-column>
      <el-table-column label="说明">
        <template #default="{ row }">{{ row.messages.join('；') || '通过' }}</template>
      </el-table-column>
    </el-table>
    <el-form :disabled="committing || validating" label-position="top">
      <div class="actions">
        <el-form-item label="生效时间" required>
          <el-date-picker v-model="validFrom" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" />
        </el-form-item>
        <el-form-item label="结束时间" required>
          <el-date-picker v-model="validTo" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" />
        </el-form-item>
      </div>
      <article v-for="item in preview.preview_items" :key="String(item.sku_id)" :class="{ excluded: excludedSkuIds.has(String(item.sku_id)) }">
        <div class="item-heading"><h3>SKU {{ item.sku_id }} · {{ item.currency }}</h3><el-button v-if="excludedSkuIds.has(String(item.sku_id))" @click="restore(item.sku_id)">恢复提交</el-button><el-button v-else type="danger" plain @click="exclude(item.sku_id)">排除此 SKU</el-button></div>
        <el-select v-if="item.currency === 'USD'" v-model="item.fx_tier" placeholder="汇率档位">
          <el-option v-for="tier in QUOTE_FX_TIERS" :key="tier" :value="tier" :label="tier" />
        </el-select>
        <el-table :data="item.components" :class="{ disabled: excludedSkuIds.has(String(item.sku_id)) }">
          <el-table-column prop="component_type" label="组件" />
          <el-table-column label="报价方式" min-width="180">
            <template #default="{ row, $index }">
              <el-radio-group :model-value="row.multiplier === null ? 'ABSOLUTE' : 'MULTIPLIER'" :disabled="committing || excludedSkuIds.has(String(item.sku_id))" @update:model-value="setMode(item, $index, $event as 'MULTIPLIER' | 'ABSOLUTE')"><el-radio-button value="MULTIPLIER" :disabled="!official(item, row.component_type)">倍率</el-radio-button><el-radio-button value="ABSOLUTE">绝对价</el-radio-button></el-radio-group>
            </template>
          </el-table-column>
          <el-table-column label="当前官方价"><template #default="{ row }">{{ official(item, row.component_type) || '无基准' }}</template></el-table-column>
          <el-table-column label="倍率"><template #default="{ row, $index }"><el-input v-if="row.multiplier !== null" v-model="row.multiplier" :disabled="committing || excludedSkuIds.has(String(item.sku_id))" @input="calculate(item, $index)" /><span v-else>—</span></template></el-table-column>
          <el-table-column label="供应价">
            <template #default="{ row }">
              <el-input v-model="row.unit_price" :disabled="committing || excludedSkuIds.has(String(item.sku_id)) || row.multiplier !== null" />
            </template>
          </el-table-column>
        </el-table>
      </article>
      <el-form-item label="报价说明">
        <el-input v-model="remark" type="textarea" maxlength="500" />
      </el-form-item>
      <el-alert v-if="error" :title="error" type="error" :closable="false" class="submit-error" />
      <el-button
        type="primary"
        :disabled="!includedItems.length"
        :loading="committing"
        @click="confirm"
      >确认导入并提交审批</el-button>
    </el-form>
  </template>
</section>
</template>

<style scoped>
.import-panel {
  padding:22px
}
.actions {
  display:flex;
  gap:12px;
  align-items:center;
  flex-wrap:wrap;
  margin:18px 0
}
.actions>.el-select,.actions>.el-input {
  width:180px
}
article {
  margin:22px 0
}
.item-heading { display:flex; align-items:center; justify-content:space-between; gap:12px }
article.excluded { opacity:.58 }
.submit-error { margin-bottom:14px }
.range-label { color:var(--el-text-color-secondary) }
</style>
