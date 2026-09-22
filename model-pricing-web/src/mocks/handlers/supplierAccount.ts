import { http, HttpResponse } from "msw";
import type {
  CreateSupplierQualificationRequest,
  SupplierPortalProfileDTO,
  SupplierQualificationSubmissionDTO,
  UpdateSupplierPortalProfileRequest,
} from "../../api/supplierAccount.types";
import { ApiError } from "../../domain/common";

const basePath = "/api/supplier";
let profile: SupplierPortalProfileDTO = {
  id: "supplier-cloud",
  code: "SUP-CLOUD-001",
  legalName: "深圳云桥科技有限公司",
  shortName: "云桥科技",
  registrationNoMasked: "9144**********8X",
  status: "ACTIVE",
  qualificationStatus: "VALID",
  contactName: "李经理",
  contactPhoneMasked: "139****3186",
  contactEmail: "li.manager@cloudbridge.example",
  registeredAddressMasked: "深圳市南山区科****88号",
  serviceRegions: ["中国大陆", "新加坡"],
  settlementCurrency: "USD",
  paymentTerms: "月结 30 天",
  updatedAt: "2026-09-09T16:20:00+08:00",
};
import { qualifications } from "../data/supplierReviews";
const idempotencyResults = new Map<string, { signature: string; value: unknown }>();

const copy = <T>(value: T): T => structuredClone(value);
const requestId = () => `mock-${crypto.randomUUID()}`;
const result = (data: unknown) => HttpResponse.json({ code: 0, message: "ok", data, requestId: requestId() });
function failure(error: unknown) { const candidate = error as { httpStatus?: number; status?: number; message?: string }; const status = candidate.httpStatus || candidate.status || 500; return HttpResponse.json({ code: status === 409 ? "STATE_CONFLICT" : `HTTP_${status}`, message: candidate.message || "Mock 服务异常", requestId: requestId() }, { status }); }
function ensureSupplier(request: Request) { if ((request.headers.get("X-Mock-Identity") || "") !== "SUPPLIER") throw new ApiError(403, "当前身份无权访问供应商账户资料"); }
function requireIdempotency(request: Request, signature: string) { const key = request.headers.get("Idempotency-Key"); if (!key) throw new ApiError(400, "缺少 Idempotency-Key"); const cached = idempotencyResults.get(key); if (cached && cached.signature !== signature) throw new ApiError(409, "幂等键已用于其他操作"); return { key, cached }; }
const maskPhone = (value: string) => value.length >= 7 ? `${value.slice(0, 3)}****${value.slice(-4)}` : "***";

export const supplierAccountHandlers = [
  http.get(`${basePath}/profile`, ({ request }) => { try { ensureSupplier(request); return result(copy(profile)); } catch (error) { return failure(error); } }),
  http.put(`${basePath}/profile`, async ({ request }) => {
    try {
      ensureSupplier(request);
      const body = (await request.json()) as UpdateSupplierPortalProfileRequest;
      if (!body.contactName?.trim() || !/^\S+@\S+\.\S+$/.test(body.contactEmail?.trim() || "")) throw new ApiError(400, "请填写联系人和有效联系邮箱");
      if (body.contactPhone && !/^1\d{10}$/.test(body.contactPhone.trim())) throw new ApiError(400, "联系手机号格式不正确");
      if (!body.serviceRegions?.length) throw new ApiError(400, "请至少选择一个服务区域");
      const signature = JSON.stringify(body);
      const { key, cached } = requireIdempotency(request, signature);
      if (cached) return result(copy(cached.value));
      profile = { ...profile, contactName: body.contactName.trim(), contactPhoneMasked: body.contactPhone ? maskPhone(body.contactPhone.trim()) : profile.contactPhoneMasked, contactEmail: body.contactEmail.trim(), serviceRegions: [...body.serviceRegions], registeredAddressMasked: body.contactAddress?.trim() ? `${body.contactAddress.trim().slice(0, 6)}****` : profile.registeredAddressMasked, updatedAt: new Date().toISOString() };
      idempotencyResults.set(key, { signature, value: copy(profile) });
      return result(copy(profile));
    } catch (error) { return failure(error); }
  }),
  http.get(`${basePath}/qualifications`, ({ request }) => { try { ensureSupplier(request); return result(copy(qualifications)); } catch (error) { return failure(error); } }),
  http.post(`${basePath}/qualifications`, async ({ request }) => {
    try {
      ensureSupplier(request);
      const body = (await request.json()) as CreateSupplierQualificationRequest;
      if (!body.qualificationType?.trim() || !body.qualificationName?.trim() || !body.documentNo?.trim()) throw new ApiError(400, "请填写资质类型、名称和证件编号");
      if (!body.attachmentName?.trim()) throw new ApiError(400, "请选择资质附件");
      if (body.validTo && Number.isNaN(Date.parse(body.validTo))) throw new ApiError(400, "资质有效期格式不正确");
      const signature = JSON.stringify(body);
      const { key, cached } = requireIdempotency(request, signature);
      if (cached) return result(copy(cached.value));
      const documentNo = body.documentNo.trim();
      const item: SupplierQualificationSubmissionDTO = { id: `supplier-qual-${crypto.randomUUID()}`, qualificationType: body.qualificationType.trim(), qualificationName: body.qualificationName.trim(), documentNoMasked: documentNo.length > 6 ? `${documentNo.slice(0, 3)}****${documentNo.slice(-3)}` : "******", validFrom: body.validFrom, validTo: body.validTo, status: "SUBMITTED", attachmentName: body.attachmentName.trim(), submittedAt: new Date().toISOString() };
      qualifications.unshift(item);
      idempotencyResults.set(key, { signature, value: copy(item) });
      return result(copy(item));
    } catch (error) { return failure(error); }
  }),
];
