<script setup lang="ts">
import { formatDateTime } from "../../domain/date";
import { ElMessage, ElMessageBox } from "element-plus";
import { onMounted, reactive, ref } from "vue";
import { supplierAccountApi } from "../../api/supplierAccount";
import type { SupplierPortalProfileDTO, UpdateSupplierPortalProfileRequest } from "../../api/supplierAccount.types";
import { ApiError } from "../../domain/common";
import { QUALIFICATION_STATUS, SUPPLIER_STATUS } from "../../domain/status";

const profile = ref<SupplierPortalProfileDTO>();
const loading = ref(false);
const error = ref("");
const editOpen = ref(false);
const submitting = ref(false);
const formError = ref("");
const form = reactive<UpdateSupplierPortalProfileRequest>({ contactName: "", contactPhone: "", contactEmail: "", serviceRegions: [], contactAddress: "" });

const errorMessage = (value: unknown) => value instanceof ApiError ? value.message : "操作失败，请稍后重试。";
const formatTime = (value?: string | null) => formatDateTime(value, "—", false);

async function load() {
  loading.value = true; error.value = "";
  try { profile.value = await supplierAccountApi.getProfile(); }
  catch (value) { error.value = errorMessage(value); }
  finally { loading.value = false; }
}

function startEdit() {
  if (!profile.value) return;
  Object.assign(form, { contactName: profile.value.contactName, contactPhone: "", contactEmail: profile.value.contactEmail, serviceRegions: [...profile.value.serviceRegions], contactAddress: "" });
  formError.value = "";
  editOpen.value = true;
}

function validate() {
  if (!form.contactName.trim()) return "请填写联系人。";
  if (!/^\S+@\S+\.\S+$/.test(form.contactEmail.trim())) return "请填写有效联系邮箱。";
  if (form.contactPhone && !/^1\d{10}$/.test(form.contactPhone.trim())) return "联系手机号格式不正确。";
  if (!form.serviceRegions.length) return "请至少填写一个服务区域。";
  return "";
}

async function save() {
  if (submitting.value) return;
  formError.value = validate();
  if (formError.value) return;
  const draft: UpdateSupplierPortalProfileRequest = { contactName: form.contactName.trim(), contactPhone: form.contactPhone?.trim() || undefined, contactEmail: form.contactEmail.trim(), serviceRegions: [...form.serviceRegions], contactAddress: form.contactAddress?.trim() || undefined };
  submitting.value = true;
  try {
    await ElMessageBox.confirm("本次只会更新联系与服务区域信息，不会修改法定主体或结算条件。", "确认保存", { confirmButtonText: "确认保存" });
    profile.value = await supplierAccountApi.updateProfile(draft);
    editOpen.value = false;
    ElMessage.success("联系资料已保存。");
  } catch (value) {
    if (value !== "cancel" && value !== "close") formError.value = errorMessage(value);
  } finally { submitting.value = false; }
}

onMounted(load);
</script>

<template>
  <section class="page-heading"><div><div class="eyebrow">SUPPLIER PROFILE</div><h1>主体档案</h1><p>查看供应商主体、商务与联系信息。</p></div><el-button type="primary" :disabled="!profile" @click="startEdit">维护联系资料</el-button></section>
  <el-alert v-if="error" :title="error" type="error" show-icon :closable="false"><el-button text @click="load">重新加载</el-button></el-alert>
  <section v-loading="loading" class="panel profile-panel">
    <template v-if="profile">
      <div class="profile-head"><div><div class="eyebrow">{{ profile.code }}</div><h2>{{ profile.legalName }}</h2><p class="muted">{{ profile.shortName }}</p></div><div class="profile-tags"><el-tag :type="SUPPLIER_STATUS[profile.status].type">{{ SUPPLIER_STATUS[profile.status].label }}</el-tag><el-tag :type="QUALIFICATION_STATUS[profile.qualificationStatus].type">资质{{ QUALIFICATION_STATUS[profile.qualificationStatus].label }}</el-tag></div></div>
      <el-alert title="法定主体、登记信息、结算币种和账期不允许在本页直接修改，变更流程待后端确认。" type="info" show-icon :closable="false" />
      <h3>主体与结算</h3>
      <el-descriptions :column="2" border><el-descriptions-item label="统一登记信息">{{ profile.registrationNoMasked }}</el-descriptions-item><el-descriptions-item label="登记地址">{{ profile.registeredAddressMasked }}</el-descriptions-item><el-descriptions-item label="结算币种">{{ profile.settlementCurrency }}</el-descriptions-item><el-descriptions-item label="结算条件">{{ profile.paymentTerms }}</el-descriptions-item></el-descriptions>
      <h3>联系与服务</h3>
      <el-descriptions :column="2" border><el-descriptions-item label="联系人">{{ profile.contactName }}</el-descriptions-item><el-descriptions-item label="手机">{{ profile.contactPhoneMasked }}</el-descriptions-item><el-descriptions-item label="联系邮箱">{{ profile.contactEmail }}</el-descriptions-item><el-descriptions-item label="服务区域">{{ profile.serviceRegions.join('、') }}</el-descriptions-item><el-descriptions-item label="最后更新" :span="2">{{ formatTime(profile.updatedAt) }}</el-descriptions-item></el-descriptions>
    </template>
  </section>
  <el-dialog v-model="editOpen" title="维护联系资料" width="min(720px, 96vw)" :close-on-click-modal="false" :close-on-press-escape="!submitting" :show-close="!submitting"><el-form label-position="top" class="profile-form" :disabled="submitting"><div class="profile-form-grid"><el-form-item label="联系人 *"><el-input v-model="form.contactName" /></el-form-item><el-form-item label="联系邮箱 *"><el-input v-model="form.contactEmail" /></el-form-item><el-form-item label="新联系手机"><el-input v-model="form.contactPhone" placeholder="留空表示不修改" /></el-form-item><el-form-item label="联系地址"><el-input v-model="form.contactAddress" placeholder="留空表示不修改" /></el-form-item></div><el-form-item label="服务区域 *"><el-select v-model="form.serviceRegions" multiple filterable allow-create default-first-option placeholder="输入后回车添加" style="width:100%" /></el-form-item><el-alert v-if="formError" :title="formError" type="error" show-icon :closable="false" /></el-form><template #footer><el-button :disabled="submitting" @click="editOpen = false">取消</el-button><el-button type="primary" :loading="submitting" @click="save">保存</el-button></template></el-dialog>
</template>

<style scoped>
.profile-panel { min-height: 360px; padding: 22px; }.profile-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; margin-bottom: 18px; }.profile-head h2 { margin: 8px 0 4px; }.profile-tags { display: flex; gap: 8px; }.profile-panel h3 { margin-top: 24px; }.profile-form { margin-top: 10px; }.profile-form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0 14px; }@media (max-width: 760px) { .profile-head { flex-direction: column; }.profile-form-grid { grid-template-columns: 1fr; } }
</style>
