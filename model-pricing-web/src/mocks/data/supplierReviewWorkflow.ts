import { ApiError } from "../../types.ts";
import type { ReviewKind, SupplierReview } from "../../api/supplierReviews";
import type { SupplierQualificationSubmissionDTO } from "../../api/supplierAccount.types";

export function createSupplierReviewWorkflow(qualifications: SupplierQualificationSubmissionDTO[]) {
  const histories = new Map<string, SupplierReview["history"]>();
  const keys = new Map<string, { signature: string; response: SupplierReview }>();
  const records = (_kind: ReviewKind) => qualifications;
  const project = (kind: ReviewKind, record: (typeof qualifications)[number]): SupplierReview => structuredClone({ id: record.id, supplierName: "云桥科技", name: record.qualificationName, status: record.status, submittedAt: record.submittedAt, material: record, history: histories.get(`${kind}:${record.id}`) || [] });
  function authorize(identity: string | null) { if (identity !== "MODEL_OPS") throw new ApiError(403, "当前身份没有供应商申请审核权限"); }
  return {
    list(identity: string | null, kind: ReviewKind) { authorize(identity); return records(kind).map(record => project(kind, record)); },
    review(identity: string | null, kind: ReviewKind, id: string, key: string | null, payload: { result: string; reason: string }) {
      authorize(identity);
      if (!key || !payload || !["APPROVED", "REJECTED"].includes(payload.result) || typeof payload.reason !== "string" || payload.reason.length > 300 || (payload.result === "REJECTED" && !payload.reason.trim())) throw new ApiError(400, "请提供幂等 Key、正确的审核结果和驳回原因（最多300字）");
      const signature = JSON.stringify({ kind, id, ...payload });
      const cached = keys.get(key);
      if (cached) { if (cached.signature !== signature) throw new ApiError(409, "幂等 Key 已用于其他审核"); return structuredClone(cached.response); }
      const record = records(kind).find(row => row.id === id);
      if (!record) throw new ApiError(404, "申请不存在或不可见");
      if (record.status !== "SUBMITTED" && record.status !== "REVIEWING") throw new ApiError(409, "申请已处理，请刷新后查看结果");
      const at = new Date().toISOString();
      record.status = payload.result as "APPROVED" | "REJECTED";
      record.reviewComment = payload.reason.trim();
      record.reviewedAt = at;
      histories.set(`${kind}:${id}`, [{ result: record.status, reason: payload.reason.trim(), at, reviewer: "林悦（模型运营）" }]);
      const response = project(kind, record);
      keys.set(key, { signature, response });
      return structuredClone(response);
    },
  };
}
