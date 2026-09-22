import { http, HttpResponse } from "msw";
import type { OrganizationNodeDTO, RoleSummaryDTO } from "../../api/orgPermissions";
import { ApiError } from "../../domain/common";

const basePath = "/api/internal/org-permissions";
const readableIdentities = new Set(["ADMIN_OP", "VIEWER"]);

const organizations: OrganizationNodeDTO[] = [
  {
    id: "org-company", code: "ORG-001", name: "模型业务中心", type: "COMPANY", memberCount: 26, ownerName: "顾言",
    children: [
      { id: "org-model", code: "DEPT-MODEL", name: "模型运营部", type: "DEPARTMENT", memberCount: 6, ownerName: "林悦", children: [{ id: "org-model-data", code: "TEAM-MODEL-DATA", name: "主数据组", type: "TEAM", memberCount: 3, ownerName: "林悦" }] },
      { id: "org-purchase", code: "DEPT-PURCHASE", name: "采购与供应商部", type: "DEPARTMENT", memberCount: 7, ownerName: "陈昊" },
      { id: "org-pricing", code: "DEPT-PRICING", name: "定价与客户部", type: "DEPARTMENT", memberCount: 8, ownerName: "周婷" },
      { id: "org-finance", code: "DEPT-FINANCE", name: "财务运营部", type: "DEPARTMENT", memberCount: 4, ownerName: "许宁" },
    ],
  },
];

const roles: RoleSummaryDTO[] = [
  { id: "role-model", name: "模型运营", description: "维护模型主数据并处理官方价格差异。", portal: "internal", memberCount: 6, dataScope: "ALL", permissions: [{ code: "M1:V", module: "模型管理", action: "查看" }, { code: "M1:E", module: "模型管理", action: "编辑" }, { code: "M2:V", module: "官方价格", action: "查看" }, { code: "M2:E", module: "官方价格", action: "处理" }], fieldRestrictions: ["不返回客户授信、押金和成本敏感字段"] },
  { id: "role-purchase", name: "采购审批", description: "查看供应商档案、审批报价并查询成本。", portal: "internal", memberCount: 7, dataScope: "DEPT_SUB", permissions: [{ code: "M3:V", module: "供应商管理", action: "查看" }, { code: "M4:V", module: "供应商报价", action: "查看" }, { code: "M4:A", module: "供应商报价", action: "审批" }, { code: "M5:V", module: "成本管理", action: "查看" }], fieldRestrictions: ["不返回客户财务账户字段"] },
  { id: "role-pricing", name: "定价运营", description: "维护策略、价目表、客户与客户报价。", portal: "internal", memberCount: 8, dataScope: "ALL", permissions: [{ code: "M6:E", module: "定价策略", action: "编辑" }, { code: "M7:E", module: "价目表", action: "编辑" }, { code: "M8:E", module: "客户报价", action: "编辑" }, { code: "M9:V", module: "客户管理", action: "查看" }], fieldRestrictions: ["不返回完整税号和非本域财务流水"] },
  { id: "role-finance", name: "财务运营", description: "查询财务账户、汇率、授信和押金。", portal: "internal", memberCount: 4, dataScope: "ALL", permissions: [{ code: "M10:V", module: "财务信息", action: "查看" }, { code: "M10:E", module: "财务信息", action: "维护" }, { code: "M12:V", module: "审计日志", action: "查看" }], fieldRestrictions: ["联系方式默认脱敏，敏感标识按字段策略返回"] },
  { id: "role-supplier", name: "供应商操作员", description: "维护本供应商报价和主体资料。", portal: "supplier", memberCount: 12, dataScope: "SELF", permissions: [], fieldRestrictions: ["仅返回所属供应商数据", "不返回成本、floor 和其他供应商报价"] },
  { id: "role-customer", name: "客户操作员", description: "查看本客户价目表、报价和合同。", portal: "customer", memberCount: 15, dataScope: "SELF", permissions: [], fieldRestrictions: ["仅返回所属客户数据", "不返回成本与内部审批信息"] },
];

const result = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data: structuredClone(data), requestId: `mock-${crypto.randomUUID()}` });
const failure = (error: unknown) => { const candidate = error as { httpStatus?: number; message?: string }; const status = candidate.httpStatus || 500; return HttpResponse.json({ code: `HTTP_${status}`, message: candidate.message || "Mock 服务异常", requestId: `mock-${crypto.randomUUID()}` }, { status }); };
const ensureReadable = (request: Request) => { const identity = request.headers.get("X-Mock-Identity") || "VIEWER"; if (!readableIdentities.has(identity)) throw new ApiError(403, "当前身份无权查看组织与权限"); };

export const orgPermissionHandlers = [
  http.get(`${basePath}/organizations`, ({ request }) => { try { ensureReadable(request); return result(organizations); } catch (error) { return failure(error); } }),
  http.get(`${basePath}/roles`, ({ request }) => { try { ensureReadable(request); return result(roles); } catch (error) { return failure(error); } }),
];
