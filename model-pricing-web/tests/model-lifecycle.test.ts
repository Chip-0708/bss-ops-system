import test from "node:test";
import assert from "node:assert/strict";
import { createMock } from "../src/mock.ts";
import type { Model, Role, Impact, Retirement } from "../src/types.ts";

test("verification rejects malformed writes atomically and replays a single trimmed record", () => {
  const db = createMock();
  const list = () => db.handle({ path: "/api/internal/models", method: "GET", role: "MODEL_OPS" }) as Model[];
  const before = list(), model = before[1]!;
  let counter = 0;
  const send = (body: unknown, key = String(++counter), role: Role = "MODEL_OPS", method: "POST" | "PUT" = "POST") =>
    db.handle({ path: `/api/internal/models/${model.id}/verify`, method, body, key, role });
  for (const body of [{ result: "PASS", note: 123 }, { result: "PASS", note: " " },
    { result: "PASS", note: "x".repeat(1001) }, { result: "UNKNOWN", note: "test" },
    { result: "PASS", note: "test", status: "PUBLISHED" }]) {
    assert.throws(() => send(body));
    assert.deepEqual(list(), before);
  }
  const body = { result: "PASS", note: "  checked  " };
  assert.throws(() => send(body, "put", "MODEL_OPS", "PUT"));
  assert.throws(() => send(body, "denied", "VIEWER"), /权限/);
  assert.deepEqual(list(), before);
  const result = send(body, "verify-once");
  assert.deepEqual(send(body, "verify-once"), result);
  const after = list()[1]!;
  assert.equal(after.status, "PURCHASABLE");
  assert.equal(after.verification.length, model.verification.length + 1);
  assert.equal(after.verification.at(-1)!.note, "checked");
  assert.throws(() => send(body), /待验证/);
});

test("publish rejects injected payload and wrong method before lifecycle mutation", () => {
  const db = createMock();
  const list = () => db.handle({ path: "/api/internal/models", method: "GET", role: "PRICING_OP" }) as Model[];
  const before = list(), model = before[2]!;
  for (const [method, body] of [["PUT", {}], ["POST", { status: "PUBLISHED" }], ["POST", []], ["POST", null]] as const) {
    assert.throws(() => db.handle({ path: `/api/internal/models/${model.id}/publish`, method, body, role: "PRICING_OP", key: JSON.stringify([method, body]) }));
    assert.deepEqual(list(), before);
  }
});

test("retirement validates report ownership and input atomically, with idempotent application replay", () => {
  const db = createMock();
  const list = () => db.handle({ path: "/api/internal/models", method: "GET", role: "MODEL_OPS" }) as Model[];
  const before = list(), model = before[3]!;
  const report = db.handle({ path: `/api/internal/models/${model.id}/deprecation-impact`, method: "GET", role: "MODEL_OPS" }) as Impact;
  const otherReport = db.handle({ path: `/api/internal/models/${before[2]!.id}/deprecation-impact`, method: "GET", role: "MODEL_OPS" }) as Impact;
  const body = { impactId: report.id, offlineAt: "2099-01-01", reason: "  replacement  " };
  let counter = 0;
  const send = (payload: unknown, key = String(++counter), role: Role = "MODEL_OPS", method: "POST" | "PUT" = "POST") =>
    db.handle({ path: `/api/internal/models/${model.id}/deprecate`, method, body: payload, key, role });
  for (const payload of [{ ...body, impactId: otherReport.id }, { ...body, reason: 123 },
    { ...body, reason: " " }, { ...body, reason: "x".repeat(1001) },
    { ...body, offlineAt: "2099-02-31" }, { ...body, status: "OFFLINE" }]) {
    assert.throws(() => send(payload));
    assert.deepEqual(list(), before);
  }
  assert.throws(() => send(body, "put", "MODEL_OPS", "PUT"));
  assert.throws(() => send(body, "denied", "VIEWER"), /权限/);
  db.failNext();
  assert.throws(() => send(body, "once"), /暂不可用/);
  assert.deepEqual(list(), before);
  const result = send(body, "once") as Retirement;
  assert.deepEqual(send(body, "once"), result);
  assert.equal(result.reason, "replacement");
  assert.equal(result.status, "APPROVING");
  assert.equal(list()[3]!.status, "PUBLISHED");
  assert.equal(list()[3]!.pendingRetirement, true);
  assert.throws(() => send(body), /待审批/);
  assert.throws(() => send({ ...body, reason: "changed" }, "once"), /幂等/);
});


test("retirement notice period starts at notification, not submission, and enforces the 30-day boundary", (t) => {
  t.mock.method(Date, "now", () => Date.parse("2026-09-20T00:00:00+08:00"));
  const db = createMock();
  const list = () => db.handle({ path: "/api/internal/models", method: "GET", role: "MODEL_OPS" }) as Model[];
  const before = list(), model = before[3]!;
  const report = db.handle({ path: `/api/internal/models/${model.id}/deprecation-impact`, method: "GET", role: "MODEL_OPS" }) as Impact;
  assert.equal(report.notifiedAt, "2026-09-01T00:00:00+08:00");
  const send = (offlineAt: string, reason = "通知期核验") => db.handle({ path: `/api/internal/models/${model.id}/deprecate`, method: "POST", role: "MODEL_OPS", key: crypto.randomUUID(), body: { impactId: report.id, offlineAt, reason } });
  assert.throws(() => send("2026-09-30"), /通知任务发出日/);
  assert.deepEqual(list(), before);
  assert.throws(() => send("2026-10-01", "x".repeat(501)), /500/);
  assert.deepEqual(list(), before);
  // This is 30 days after notification but only 11 after submission.
  assert.equal((send("2026-10-01", "x".repeat(500)) as Retirement).status, "APPROVING");
  assert.equal(list()[3]!.status, "PUBLISHED");
});

test("rejecting a retirement changes only the request and pending marker, and is safe to replay", () => {
  const db = createMock();
  const list = () => db.handle({ path: "/api/internal/models", method: "GET", role: "MODEL_OPS" }) as Model[];
  const original = list()[3]!;
  const report = db.handle({ path: `/api/internal/models/${original.id}/deprecation-impact`, method: "GET", role: "MODEL_OPS" }) as Impact;
  db.handle({ path: `/api/internal/models/${original.id}/deprecate`, method: "POST", role: "MODEL_OPS", key: "retire", body: { impactId: report.id, offlineAt: "2099-01-01", reason: "退役核验" } });
  const requests = db.handle({ path: "/api/internal/change-requests", method: "GET", role: "PRICING_OP" }) as Array<{ id: string; modelId: string; type: string; status: string }>;
  const request = requests.find(item => item.modelId === original.id && item.type === "DEPRECATE")!;
  assert.equal(request.status, "APPROVING");
  assert.equal(list()[3]!.status, "PUBLISHED");
  const payload = { path: `/api/internal/change-requests/${request.id}/reject`, method: "POST" as const, role: "PRICING_OP" as const, key: "reject-retirement", body: { reason: "暂不退役，保留服务" } };
  const result = db.handle(payload) as { status: string };
  assert.equal(result.status, "REJECTED");
  assert.deepEqual(db.handle(payload), result);
  const after = list()[3]!;
  assert.equal(after.status, "PUBLISHED");
  assert.equal(after.pendingRetirement, false);
  const { pendingRetirement, ...fields } = after;
  assert.deepEqual(fields, original);
});
