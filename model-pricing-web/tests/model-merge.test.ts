import test from "node:test";
import assert from "node:assert/strict";
import { createMock } from "../src/mock.ts";
import type { Model } from "../src/types.ts";

test("wire alias merge validates source, preserves lifecycle and replays without duplicate writes", () => {
  const db = createMock();
  const list = () => db.handle({ path: "/api/internal/models", method: "GET", role: "MODEL_OPS" }) as Model[];
  const before = list();
  const source = before[0]!, target = before[1]!;
  const body = { target_sku_id: target.id, source_sku_id: source.id, alias: "merged-preview" };
  let counter = 0;
  const send = (payload: unknown, key = String(++counter), role: "MODEL_OPS" | "VIEWER" = "MODEL_OPS") =>
    db.handle({ path: "/api/internal/models/aliases/merge", method: "POST", body: payload, key, role });
  for (const payload of [
    { ...body, source_sku_id: target.id }, { ...body, source_sku_id: "missing" },
    { ...body, source_sku_id: 9007199254741000 }, { ...body, alias: " " },
    { ...body, alias: "x".repeat(129) }, { ...body, alias: source.code },
    { ...body, lifecycle_status: "OFFLINE" },
  ]) {
    assert.throws(() => send(payload));
    assert.deepEqual(list(), before);
  }
  assert.throws(() => send(body, "denied", "VIEWER"), /权限/);
  assert.deepEqual(list(), before);
  const result = send(body, "once") as { target_sku_id: string; merged_sku_id: string; alias_id: string; alias: string };
  assert.equal(result.target_sku_id, target.id);
  assert.equal(result.merged_sku_id, source.id);
  assert.equal(result.alias, body.alias);
  assert.ok(result.alias_id);
  assert.deepEqual(send(body, "once"), result);
  const after = list();
  assert.equal(after.length, before.length);
  assert.deepEqual(after[0], source);
  assert.equal(after[1]!.status, target.status);
  assert.equal(after[1]!.aliases.filter(alias => alias === body.alias).length, 1);
  assert.throws(() => send({ ...body, alias: "changed" }, "once"), /幂等/);
  assert.throws(() => send(body), /已/);
  assert.deepEqual(list(), after);
});
