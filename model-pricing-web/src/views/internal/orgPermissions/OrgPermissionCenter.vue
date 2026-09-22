<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { orgPermissionsApi, type OrganizationNodeDTO, type RoleSummaryDTO } from "../../../api/orgPermissions";
import { ApiError } from "../../../domain/common";

const activeTab = ref("organizations");
const loading = ref(false);
const error = ref("");
const organizations = ref<OrganizationNodeDTO[]>([]);
const roles = ref<RoleSummaryDTO[]>([]);
const selectedOrganization = ref<OrganizationNodeDTO>();
const selectedRole = ref<RoleSummaryDTO>();
const roleDetailOpen = ref(false);
const errorMessage = (value: unknown) => value instanceof ApiError ? value.message : value instanceof Error ? value.message : "组织与权限加载失败，请稍后重试。";
const orgType = (type: OrganizationNodeDTO["type"]) => ({ COMPANY: "公司", DEPARTMENT: "部门", TEAM: "团队" })[type];
const portalLabel = (portal: RoleSummaryDTO["portal"]) => ({ internal: "内部运营", supplier: "供应商门户", customer: "客户门户" })[portal];
const dataScopeLabel = (scope: RoleSummaryDTO["dataScope"]) => ({ SELF: "仅本人/本主体", DEPT: "本部门", DEPT_SUB: "本部门及下级", ALL: "全部数据" })[scope];
const dataScopeType = (scope: RoleSummaryDTO["dataScope"]) => scope === "ALL" ? "warning" : scope === "SELF" ? "info" : "success";
const totalMembers = computed(() => organizations.value.reduce((total, item) => total + item.memberCount, 0));

async function load() {
  loading.value = true; error.value = "";
  try {
    const [organizationRows, roleRows] = await Promise.all([orgPermissionsApi.getOrganizations(), orgPermissionsApi.getRoles()]);
    organizations.value = organizationRows; roles.value = roleRows; selectedOrganization.value = organizationRows[0];
  } catch (value) { organizations.value = []; roles.value = []; selectedOrganization.value = undefined; error.value = errorMessage(value); }
  finally { loading.value = false; }
}
function selectOrganization(node: OrganizationNodeDTO) { selectedOrganization.value = node; }
function openRole(role: RoleSummaryDTO) { selectedRole.value = role; roleDetailOpen.value = true; }
onMounted(load);
</script>

<template>
  <section class="page-heading"><div><div class="eyebrow">ORGANIZATION & ACCESS</div><h1>组织与权限</h1><p>查看组织层级、角色功能权限、数据域与字段限制。</p></div><el-tag type="info" effect="plain">只读基础版</el-tag></section>
  <el-alert title="前端菜单和按钮仅用于交互提示；真实权限、数据域、行级过滤与字段物理剔除必须由后端执行。" type="warning" show-icon :closable="false" />
  <el-alert v-if="error" class="org-error" :title="error" type="error" show-icon :closable="false"><el-button text @click="load">重新加载</el-button></el-alert>

  <div v-loading="loading" class="org-body">
    <div v-if="organizations.length || roles.length" class="stats org-stats"><div><span>组织人数</span><strong>{{ totalMembers }}<small>人</small></strong></div><div><span>角色</span><strong>{{ roles.length }}<small>个</small></strong></div><div><span>内部角色</span><strong>{{ roles.filter((role) => role.portal === 'internal').length }}<small>个</small></strong></div><div><span>外部门户角色</span><strong>{{ roles.filter((role) => role.portal !== 'internal').length }}<small>个</small></strong></div></div>
    <el-tabs v-if="organizations.length || roles.length" v-model="activeTab" class="org-tabs">
      <el-tab-pane label="组织结构" name="organizations">
        <div class="org-grid">
          <section class="panel org-panel"><div class="section-title"><h2>组织树</h2><el-button :loading="loading" @click="load">刷新</el-button></div><el-tree :data="organizations" node-key="id" default-expand-all highlight-current :props="{ label: 'name', children: 'children' }" @node-click="selectOrganization"><template #default="{ data }"><span class="org-node"><span>{{ data.name }}</span><small>{{ data.memberCount }} 人</small></span></template></el-tree></section>
          <section class="panel org-panel"><div class="section-title"><h2>组织信息</h2><span class="muted">当前节点</span></div><el-empty v-if="!selectedOrganization" description="请选择组织节点" /><template v-else><div class="org-selected"><span class="org-mark">◇</span><div><div class="eyebrow">{{ selectedOrganization.code }}</div><h2>{{ selectedOrganization.name }}</h2><p>{{ orgType(selectedOrganization.type) }}</p></div></div><el-descriptions :column="1" border><el-descriptions-item label="组织类型">{{ orgType(selectedOrganization.type) }}</el-descriptions-item><el-descriptions-item label="成员数量">{{ selectedOrganization.memberCount }} 人</el-descriptions-item><el-descriptions-item label="负责人">{{ selectedOrganization.ownerName || '暂缺' }}</el-descriptions-item><el-descriptions-item label="下级节点">{{ selectedOrganization.children?.length || 0 }} 个</el-descriptions-item></el-descriptions><el-alert class="org-note" title="成员清单、岗位与人员分配接口尚未开放。" type="info" :closable="false" /></template></section>
        </div>
      </el-tab-pane>

      <el-tab-pane label="角色与权限" name="roles">
        <section class="panel org-panel"><div class="section-title"><h2>角色权限矩阵 <span>{{ roles.length }}</span></h2><span class="muted">只读 · 服务端返回</span></div><el-empty v-if="!roles.length" description="暂无角色权限数据" /><el-table v-else :data="roles" row-key="id"><el-table-column label="角色" min-width="180"><template #default="{ row }"><b>{{ row.name }}</b><div class="muted">{{ row.description }}</div></template></el-table-column><el-table-column label="Portal" width="120"><template #default="{ row }">{{ portalLabel(row.portal) }}</template></el-table-column><el-table-column label="成员" width="85"><template #default="{ row }">{{ row.memberCount }} 人</template></el-table-column><el-table-column label="数据域" min-width="145"><template #default="{ row }"><el-tag :type="dataScopeType(row.dataScope)">{{ dataScopeLabel(row.dataScope) }}</el-tag></template></el-table-column><el-table-column label="功能权限" width="100"><template #default="{ row }">{{ row.permissions.length }} 项</template></el-table-column><el-table-column label="字段限制" min-width="170"><template #default="{ row }">{{ row.fieldRestrictions.length ? `${row.fieldRestrictions.length} 项限制` : '未返回限制' }}</template></el-table-column><el-table-column label="操作" width="95"><template #default="{ row }"><el-button link type="primary" @click="openRole(row)">查看矩阵</el-button></template></el-table-column></el-table></section>
      </el-tab-pane>
    </el-tabs>
    <el-empty v-else-if="!loading && !error" description="暂无组织与权限数据" />
  </div>

  <el-drawer v-model="roleDetailOpen" title="角色权限详情" size="min(720px, 96vw)"><template v-if="selectedRole"><div class="role-detail-head"><div><div class="eyebrow">{{ portalLabel(selectedRole.portal) }}</div><h2>{{ selectedRole.name }}</h2><p class="muted">{{ selectedRole.description }}</p></div><el-tag :type="dataScopeType(selectedRole.dataScope)">{{ dataScopeLabel(selectedRole.dataScope) }}</el-tag></div><el-descriptions :column="2" border><el-descriptions-item label="Portal">{{ portalLabel(selectedRole.portal) }}</el-descriptions-item><el-descriptions-item label="成员数量">{{ selectedRole.memberCount }} 人</el-descriptions-item><el-descriptions-item label="DataScope" :span="2"><span class="mono">{{ selectedRole.dataScope }}</span> · {{ dataScopeLabel(selectedRole.dataScope) }}</el-descriptions-item></el-descriptions><h3>功能权限</h3><el-empty v-if="!selectedRole.permissions.length" description="外部门户权限编码待正式契约确认" :image-size="64" /><el-table v-else :data="selectedRole.permissions" border><el-table-column prop="module" label="模块" min-width="160" /><el-table-column prop="action" label="操作" width="110" /><el-table-column label="权限编码" min-width="130"><template #default="{ row }"><span class="mono">{{ row.code }}</span></template></el-table-column></el-table><h3>字段与数据限制</h3><ul class="field-restrictions"><li v-for="restriction in selectedRole.fieldRestrictions" :key="restriction">{{ restriction }}</li></ul><el-alert title="Router Guard 不处理 SELF、DEPT、DEPT_SUB、ALL 或 FieldMask；这些结果必须由后端落实。" type="info" show-icon :closable="false" /></template></el-drawer>
</template>

<style scoped>
.org-error { margin-top: 14px; }.org-body { min-height: 420px; }.org-stats { margin: 20px 0 8px; }.org-tabs { margin-top: 12px; }.org-grid { display: grid; grid-template-columns: 0.9fr 1.1fr; gap: 20px; }.org-panel { padding: 22px; }.org-node { display: flex; min-width: 250px; align-items: center; justify-content: space-between; gap: 20px; }.org-node small { color: var(--app-text-muted); }.org-selected { display: flex; align-items: center; gap: 14px; margin: 14px 0 20px; }.org-selected h2 { margin: 7px 0 3px; }.org-selected p { margin: 0; color: var(--app-text-muted); }.org-mark { display: grid; width: 42px; height: 42px; place-items: center; border-radius: 10px; background: var(--app-hover-bg); color: var(--app-accent); font-size: 20px; }.org-note { margin-top: 16px; }.role-detail-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 18px; margin-bottom: 18px; }.role-detail-head h2 { margin: 8px 0 4px; }.field-restrictions { padding: 14px 18px 14px 34px; border-radius: 8px; background: var(--app-surface-muted); line-height: 1.9; }@media (max-width: 900px) { .org-grid { grid-template-columns: 1fr; }.role-detail-head { flex-direction: column; } }
</style>
