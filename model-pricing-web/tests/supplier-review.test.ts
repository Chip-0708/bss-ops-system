import test from "node:test";
import assert from "node:assert/strict";
import { createSupplierReviewWorkflow } from "../src/mocks/data/supplierReviewWorkflow.ts";
import { qualifications } from "../src/mocks/data/supplierReviews.ts";

test("internal qualification review writes the supplier record and replays idempotently", () => {
  const rows = structuredClone(qualifications); const workflow = createSupplierReviewWorkflow(rows);
  const payload = { result: "REJECTED", reason: "请补充盖章文件" };
  const result = workflow.review("MODEL_OPS", "qualifications", "supplier-qual-003", "review-1", payload);
  assert.equal(rows.find(row => row.id === result.id)?.reviewComment, payload.reason);
  assert.equal(result.status, "REJECTED"); assert.equal(result.history.length, 1);
  assert.deepEqual(workflow.review("MODEL_OPS", "qualifications", result.id, "review-1", payload), result);
  assert.throws(() => workflow.review("MODEL_OPS", "qualifications", result.id, "other", payload), /已处理/);
  assert.throws(() => workflow.review("MODEL_OPS", "qualifications", result.id, "review-1", { result: "APPROVED", reason: "" }), /其他审核/);
});
