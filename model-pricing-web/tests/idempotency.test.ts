import test from "node:test";
import assert from "node:assert/strict";
import { clearIdempotency, prepareIdempotency } from "../src/api/idempotency.ts";

function memoryStorage() {
  const entries = new Map<string, string>();
  return {
    entries,
    storage: {
      getItem: (key: string) => entries.get(key) ?? null,
      setItem: (key: string, value: string) => { entries.set(key, value); },
      removeItem: (key: string) => { entries.delete(key); },
    },
  };
}

const options = { scope: "supplier-quote:import-confirm", payload: { valid_from: "2026-09-18T12:00:00+08:00", items: [{ sku_id: 1 }] } };

test("idempotency keeps Web Crypto digest and randomUUID when both are available", async () => {
  const { storage } = memoryStorage();
  let digestCalls = 0, uuidCalls = 0;
  const cryptoApi = {
    subtle: { digest: async (algorithm: AlgorithmIdentifier, data: BufferSource) => {
      digestCalls += 1;
      return globalThis.crypto.subtle.digest(algorithm, data);
    } },
    randomUUID: () => { uuidCalls += 1; return "11111111-1111-4111-8111-111111111111"; },
  };
  const result = await prepareIdempotency(storage, "supplier:1", options, cryptoApi);
  assert.match(result.storageKey, /^idempotency:v2:[0-9a-f]{64}$/);
  assert.equal(result.idempotencyKey, "11111111-1111-4111-8111-111111111111");
  assert.equal(digestCalls, 1);
  assert.equal(uuidCalls, 1);
});

test("idempotency uses a deterministic fingerprint when subtle is unavailable", async () => {
  const first = memoryStorage(), second = memoryStorage();
  const firstResult = await prepareIdempotency(first.storage, "supplier:1", options, {
    randomUUID: () => "22222222-2222-4222-8222-222222222222",
  });
  const secondResult = await prepareIdempotency(second.storage, "supplier:1", options, {
    randomUUID: () => "33333333-3333-4333-8333-333333333333",
  });
  assert.match(firstResult.storageKey, /^idempotency:v2:fallback-[0-9a-f]{32}$/);
  assert.equal(firstResult.storageKey, secondResult.storageKey);
});

test("idempotency fallback fingerprint changes with the payload", async () => {
  const first = memoryStorage(), second = memoryStorage();
  const cryptoApi = { randomUUID: () => "44444444-4444-4444-8444-444444444444" };
  const firstResult = await prepareIdempotency(first.storage, "supplier:1", options, cryptoApi);
  const secondResult = await prepareIdempotency(second.storage, "supplier:1", { ...options, payload: { items: [{ sku_id: 2 }] } }, cryptoApi);
  assert.notEqual(firstResult.storageKey, secondResult.storageKey);
});

test("idempotency creates an RFC 4122 v4 key with getRandomValues when randomUUID is unavailable", async () => {
  const { storage } = memoryStorage();
  const cryptoApi = {
    subtle: globalThis.crypto.subtle,
    getRandomValues: (bytes: Uint8Array) => {
      bytes.forEach((_, index) => { bytes[index] = index; });
      return bytes;
    },
  };
  const result = await prepareIdempotency(storage, "supplier:1", options, cryptoApi);
  assert.equal(result.idempotencyKey, "00010203-0405-4607-8809-0a0b0c0d0e0f");
  assert.match(result.idempotencyKey, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
});

test("idempotency supports an insecure context without subtle or randomUUID", async () => {
  const { storage } = memoryStorage();
  const result = await prepareIdempotency(storage, "supplier:1", options, {
    getRandomValues: (bytes: Uint8Array) => {
      bytes.fill(0xab);
      return bytes;
    },
  });
  assert.match(result.storageKey, /^idempotency:v2:fallback-[0-9a-f]{32}$/);
  assert.equal(result.idempotencyKey, "abababab-abab-4bab-abab-abababababab");
});

test("idempotency reuses a stored key without generating a replacement", async () => {
  const { storage } = memoryStorage();
  const first = await prepareIdempotency(storage, "supplier:1", options, {
    randomUUID: () => "55555555-5555-4555-8555-555555555555",
  });
  const second = await prepareIdempotency(storage, "supplier:1", options, {
    randomUUID: () => { throw new Error("stored key should be reused"); },
  });
  assert.deepEqual(second, first);
});

test("price-book operation changes and changes back without reviving an old key", async () => {
  const { storage } = memoryStorage();
  let next = 0;
  const cryptoApi = { randomUUID: () => `operation-${++next}` };
  const input = (revision: number, level: string) => ({
    scope: `price-book:generate:${revision}`, lifecycle: "price-book:generate",
    payload: { level_code: level, currency: "USD", policy_ids: [], sku_ids: [] },
  });
  const first = await prepareIdempotency(storage, "internal:1", input(1, "GOLD"), cryptoApi);
  const retry = await prepareIdempotency(storage, "internal:1", input(1, "GOLD"), cryptoApi);
  assert.equal(retry.idempotencyKey, first.idempotencyKey);
  const changed = await prepareIdempotency(storage, "internal:1", input(2, "SILVER"), cryptoApi);
  const restored = await prepareIdempotency(storage, "internal:1", input(3, "GOLD"), cryptoApi);
  assert.notEqual(changed.idempotencyKey, first.idempotencyKey);
  assert.notEqual(restored.idempotencyKey, first.idempotencyKey);
  clearIdempotency(storage, restored);
  const nextOperation = await prepareIdempotency(storage, "internal:1", input(3, "GOLD"), cryptoApi);
  assert.notEqual(nextOperation.idempotencyKey, restored.idempotencyKey);
});
