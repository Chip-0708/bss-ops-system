import test from "node:test";
import assert from "node:assert/strict";
import { createMock } from "../src/mock.ts";
import type {
  Model,
  Impact,
  Retirement,
  Role,
} from "../src/types.ts";
import type { BatchResult } from "../src/api/models.types.ts";
const base = "/api/internal/models";
test("GET cannot mutate state", () => {
  const db = createMock();
  assert.throws(
    () =>
      db.handle({
        path: base + "/9007199254741001/verify",
        method: "GET",
        role: "MODEL_OPS",
        body: { result: "PASS", note: "invalid method" },
      }),
    /GET/,
  );
});
function setup() {
  const db = createMock();
  let n = 0;
  return {
    db,
    send: (
      path = "",
      method: "GET" | "POST" | "PUT" = "GET",
      body?: unknown,
      role: Role = "MODEL_OPS",
      key?: string,
    ) =>
      db.handle({
        path: base + path,
        method,
        body,
        role,
        key: method === "GET" ? undefined : key || String(++n),
      }),
  };
}
test("role redaction and write authorization apply at the transport boundary", () => {
  const { send } = setup();
  const list = send() as Model[];
  assert.equal(list.length, 6);
  assert.equal(Object.hasOwn(list[0]!, "cost"), false);
  assert.ok(
    Object.hasOwn(
      (send("", "GET", undefined, "PRICING_OP") as Model[])[0]!,
      "cost",
    ),
  );
  assert.throws(
    () =>
      send(
        "/batch",
        "POST",
        { ids: [list[0]!.id], action: "tier", value: "旗舰" },
        "VIEWER",
      ),
    /权限/,
  );
  assert.throws(() => send(`/${list[2]!.id}/publish`, "POST", {}), /定价运营/);
});
test("create/edit preserves string identifiers and price precision; duplicate code conflicts", () => {
  const { send } = setup();
  const draft = {
    code: "test-new",
    name: "测试",
    vendor: "测试厂商",
    family: "测试系列",
    type: "对话",
    context: 128000,
    capabilities: ["文本"],
    protocol: "OpenAI",
    tier: "主力",
    sensitive: false,
    crossBorder: false,
  };
  const result = send("", "POST", draft) as { id: string };
  assert.equal(typeof result.id, "string");
  assert.throws(() => send("", "POST", draft), /已被/);
  send("", "PUT", {
    ...draft,
    id: result.id,
    name: "更新",
    status: "PUBLISHED",
  });
  const row = (send() as Model[]).find((m) => m.id === result.id)!;
  assert.equal(row.name, "更新");
  assert.equal(row.status, "DRAFT");
  assert.equal(row.input, "0.00000000");
});
test("verification requires pending status; publishing requires pricing role and purchasable", () => {
  const { send } = setup();
  const list = send() as Model[];
  const m = list[1]!;
  assert.throws(
    () => send(`/${list[0]!.id}/verify`, "POST", { result: "PASS", note: "a" }),
    /待验证/,
  );
  send(`/${m.id}/verify`, "POST", { result: "FAIL", note: "连接失败" });
  assert.equal((send() as Model[])[1]!.status, "PENDING_VERIFY");
  send(`/${m.id}/verify`, "POST", { result: "PASS", note: "人工检查通过" });
  send(`/${m.id}/publish`, "POST", {}, "PRICING_OP");
  assert.equal((send() as Model[])[1]!.status, "PUBLISHED");
  assert.throws(
    () => send(`/${m.id}/publish`, "POST", {}, "PRICING_OP"),
    /可采购/,
  );
});
test("batch reports partial failure and cannot bypass publishing", () => {
  const { send } = setup();
  const rows = send() as Model[];
  const results = send("/batch", "POST", {
    ids: [rows[0]!.id, rows[3]!.id],
    action: "status",
    value: "PENDING_VERIFY",
  }) as BatchResult[];
  assert.deepEqual(
    results.map((r) => r.ok),
    [true, false],
  );
  const invalid = send("/batch", "POST", {
    ids: [rows[0]!.id],
    action: "status",
    value: "PUBLISHED",
  }) as BatchResult[];
  assert.equal(invalid[0]!.ok, false);
});
test("alias uniqueness is global, merge attaches new name without deleting SKU", () => {
  const { send } = setup();
  const rows = send() as Model[];
  send(`/${rows[0]!.id}/aliases`, "POST", { alias: "new-alias" });
  assert.throws(
    () => send(`/${rows[1]!.id}/aliases`, "POST", { alias: "NEW-ALIAS" }),
    /已被/,
  );
  send("/aliases/merge", "POST", {
    targetId: rows[1]!.id,
    alias: "another-alias",
  });
  assert.equal((send() as Model[]).length, 6);
  assert.ok((send() as Model[])[1]!.aliases.includes("another-alias"));
});
test("retirement requires report, future date, reason and published status; first approver is pricing", () => {
  const { send } = setup();
  const m = (send() as Model[])[3]!;
  assert.throws(
    () =>
      send(`/${m.id}/deprecate`, "POST", {
        offlineAt: "2099-01-01",
        reason: "替代",
      }),
    /报告/,
  );
  const report = send(`/${m.id}/deprecation-impact`) as Impact;
  assert.throws(() => send(`/${m.id}/deprecate`, "POST", {
    impactId: report.id, offlineAt: "2099-02-31", reason: "无效日历日期",
  }), /下线日/);
  assert.throws(
    () =>
      send(`/${m.id}/deprecate`, "POST", {
        impactId: report.id,
        offlineAt: "2026-09-02",
        reason: "替代",
      }),
    /下线日/,
  );
  const result = send(`/${m.id}/deprecate`, "POST", {
    impactId: report.id,
    offlineAt: "2099-01-01",
    reason: "替代",
  }) as Retirement;
  assert.equal(result.firstApprover, "定价运营");
  assert.equal(result.status, "APPROVING");
  assert.equal((send() as Model[])[3]!.status, "PUBLISHED");
  assert.throws(
    () =>
      send(`/${m.id}/deprecate`, "POST", {
        impactId: report.id,
        offlineAt: "2099-01-01",
        reason: "替代",
      }),
    /待审批/,
  );
});
test("contract batches enforce limits and return partial failure summaries", () => {
  const { send } = setup();
  const models = send() as Model[];
  const body = { sku_ids: [models[0]!.id, models[3]!.id], action: "SUBMIT_VERIFY" };
  const first = send("/batch", "POST", body, "MODEL_OPS", "contract-batch");
  assert.deepEqual(first, { total: 2, succeeded: 1, failed: [{ sku_id: models[3]!.id, code: 10001, message: "仅演示草稿提交待验证；上架、退役需走独立流程" }] });
  assert.deepEqual(send("/batch", "POST", body, "MODEL_OPS", "contract-batch"), first);
  assert.equal((send() as Model[])[0]!.status, "PENDING_VERIFY");
  assert.equal((send() as Model[])[3]!.status, "PUBLISHED");
  assert.throws(() => send("/batch", "POST", { sku_ids: [], action: "SET_TIER", payload: { tier_tag: "旗舰" } }), /1~200/);
  assert.throws(() => send("/batch", "POST", { sku_ids: Array.from({ length: 201 }, (_, index) => String(index + 1)), action: "SUBMIT_VERIFY" }), /1~200/);
  assert.throws(() => send("/batch", "POST", { sku_ids: [models[0]!.id, models[0]!.id], action: "SUBMIT_VERIFY" }), /不重复/);
  assert.throws(() => send("/batch", "POST", { sku_ids: [9007199254740992], action: "SUBMIT_VERIFY" }), /安全整数/);
  assert.throws(() => send("/batch", "POST", { sku_ids: [models[0]!.id], action: "PUBLISH" }), /上架和退役/);
  const tier = send("/batch", "POST", { sku_ids: [models[1]!.id], action: "SET_TIER", payload: { tier_tag: "旗舰" } });
  assert.deepEqual(tier, { total: 1, succeeded: 1, failed: [] });
  assert.equal((send() as Model[])[1]!.tier, "旗舰");
});
test("publish contract response preserves its timestamp on idempotent replay", () => {
  const { send } = setup();
  const model = (send() as Model[])[2]!;
  const result = send(`/${model.id}/publish`, "POST", {}, "PRICING_OP", "publish-contract") as { sku_id: string; lifecycle_status: string; published_at: string };
  assert.equal(result.sku_id, model.id);
  assert.equal(result.lifecycle_status, "PUBLISHED");
  assert.ok(!Number.isNaN(Date.parse(result.published_at)));
  assert.deepEqual(send(`/${model.id}/publish`, "POST", {}, "PRICING_OP", "publish-contract"), result);
});
test("full alias replacement is atomic, supports clearing and rejects duplicate ownership", () => {
  const { send } = setup();
  const models = send() as Model[];
  const model = models[0]!;
  const replace = (aliases: string[], key?: string) =>
    send(`/${model.id}/aliases`, "POST", { aliases, source: "MANUAL" }, "MODEL_OPS", key);
  const first = replace(["alias-a", "alias-b"], "replace-first");
  assert.deepEqual(replace(["alias-a", "alias-b"], "replace-first"), first);
  replace(["alias-b", "alias-c"]);
  assert.deepEqual((send() as Model[])[0]!.aliases, ["alias-b", "alias-c"]);
  assert.throws(() => replace(["ok", models[1]!.code]), /其他模型/);
  assert.deepEqual((send() as Model[])[0]!.aliases, ["alias-b", "alias-c"]);
  assert.throws(() => replace(["duplicate", "DUPLICATE"]), /重复/);
  assert.throws(() => replace(["x".repeat(129)]), /1~128/);
  replace([]);
  assert.deepEqual((send() as Model[])[0]!.aliases, []);
});
test("idempotent retry returns same result, semantic conflict rejected, transient failure does not mutate", () => {
  const { send, db } = setup();
  const m = (send() as Model[])[0]!;
  const body = { alias: "retry-alias" };
  const first = send(`/${m.id}/aliases`, "POST", body, "MODEL_OPS", "same");
  assert.deepEqual(
    send(`/${m.id}/aliases`, "POST", body, "MODEL_OPS", "same"),
    first,
  );
  assert.throws(
    () =>
      send(
        `/${m.id}/aliases`,
        "POST",
        { alias: "different" },
        "MODEL_OPS",
        "same",
      ),
    /幂等/,
  );
  db.failNext();
  assert.throws(
    () => send(`/${m.id}/aliases`, "POST", { alias: "failed" }),
    /暂不可用/,
  );
  assert.equal((send() as Model[])[0]!.aliases.includes("failed"), false);
  db.reset();
  assert.equal((send() as Model[])[0]!.aliases.length, 0);
});
