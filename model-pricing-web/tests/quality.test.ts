import test from "node:test";
import assert from "node:assert/strict";
import { prepareIdempotency } from "../src/api/idempotency.ts";
import { isDecimalAmount } from "../src/domain/money.ts";
import { formatDateTime } from "../src/domain/date.ts";
import { redactSensitive, redactText } from "../src/domain/sensitive.ts";

test("unknown attempts retain identity across edits and are isolated by actor, scope and payload", async () => {
  const entries = new Map<string, string>();
  const storage = { getItem: (key: string) => entries.get(key) ?? null, setItem: (key: string, value: string) => { entries.set(key, value); } };
  const prepare = (name: string, actor = "user-a", scope = "quote:create") => prepareIdempotency(storage, actor, { scope, payload: { name } });
  const first = await prepare("Aa");
  const changed = await prepare("BB");
  assert.notEqual(first.idempotencyKey, changed.idempotencyKey);
  assert.deepEqual(await prepare("Aa"), first);
  assert.notEqual((await prepare("Aa", "user-b")).idempotencyKey, first.idempotencyKey);
  assert.notEqual((await prepare("Aa", "user-a", "model:create")).idempotencyKey, first.idempotencyKey);
  const parallel = await Promise.all([prepare("parallel"), prepare("parallel")]);
  assert.equal(parallel[0]!.idempotencyKey, parallel[1]!.idempotencyKey);
  assert.ok([...entries.keys()].every(key => !key.includes("Aa") && !key.includes("user-a")));
});

test("money validation rejects nonfinite/scientific/overprecision input and time display tolerates invalid data", () => {
  for (const value of ["Infinity", "NaN", "1e3", "-1", "0.000000001", "", null, 1]) assert.equal(isDecimalAmount(value), false);
  assert.equal(isDecimalAmount("0"), false);
  assert.equal(isDecimalAmount("0", true), true);
  assert.equal(isDecimalAmount("12345678901234567890.12345678"), true);
  assert.equal(formatDateTime("invalid", "未提供"), "未提供");
  assert.equal(formatDateTime(null, "未提供"), "未提供");
  assert.equal(formatDateTime("2026-09-14T00:00:00Z"), formatDateTime("2026-09-14T08:00:00+08:00"));
});

test("credential redaction covers nested objects, arrays, bearer strings and URL parameters without mutating source", () => {
  const fake = "sk-" + "synthetic-test-credential";
  const source = { api_key: fake, children: [{ refresh_token: "synthetic-refresh" }], details: `Bearer ${fake}`, url: "https://example.invalid/?token=synthetic-opaque", max_output_tokens: 100, modelName: "Token model" };
  const safe = redactSensitive(source);
  assert.equal(safe.api_key, "sk-************");
  assert.equal(source.api_key, fake);
  assert.equal(safe.max_output_tokens, 100);
  assert.equal(safe.modelName, "Token model");
  assert.ok(!JSON.stringify(safe).includes("synthetic"));
  assert.ok(!redactText('{"token":"synthetic-json"}').includes("synthetic-json"));
});
