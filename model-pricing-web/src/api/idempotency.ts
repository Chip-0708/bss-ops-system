export interface IdempotencyOptions {
  scope: string;
  payload?: unknown;
  lifecycle?: string;
}

type IdempotencyStorage = Pick<Storage, "getItem" | "setItem"> & Partial<Pick<Storage, "removeItem">>;

interface IdempotencyCrypto {
  subtle?: {
    digest(algorithm: AlgorithmIdentifier, data: BufferSource): Promise<ArrayBuffer>;
  };
  randomUUID?: () => string;
  getRandomValues?: (array: Uint8Array) => Uint8Array;
}

function fallbackFingerprint(bytes: Uint8Array): string {
  const seeds = [0, 0x9e3779b9, 0x85ebca6b, 0xc2b2ae35];
  return seeds.map(seed => {
    let hash = (0x811c9dc5 ^ seed) >>> 0;
    for (const byte of bytes) {
      hash ^= byte;
      hash = Math.imul(hash, 0x01000193) >>> 0;
    }
    return hash.toString(16).padStart(8, "0");
  }).join("");
}

async function fingerprint(bytes: Uint8Array<ArrayBuffer>, cryptoApi: IdempotencyCrypto | undefined): Promise<string> {
  if (!cryptoApi?.subtle) return `fallback-${fallbackFingerprint(bytes)}`;
  const digest = await cryptoApi.subtle.digest("SHA-256", bytes);
  return Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, "0")).join("");
}

function createIdempotencyKey(cryptoApi: IdempotencyCrypto | undefined): string {
  if (cryptoApi?.randomUUID) return cryptoApi.randomUUID();
  if (!cryptoApi?.getRandomValues) throw new Error("当前浏览器缺少安全随机数能力，无法生成幂等键。");
  const bytes = new Uint8Array(16);
  cryptoApi.getRandomValues(bytes);
  bytes[6] = (bytes[6]! & 0x0f) | 0x40;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  const hex = Array.from(bytes, byte => byte.toString(16).padStart(2, "0"));
  return `${hex.slice(0, 4).join("")}-${hex.slice(4, 6).join("")}-${hex.slice(6, 8).join("")}-${hex.slice(8, 10).join("")}-${hex.slice(10).join("")}`;
}

// Store fingerprints, never request bodies or credentials. Unknown outcomes retain their key.
export async function prepareIdempotency(
  storage: IdempotencyStorage,
  principal: string,
  options: IdempotencyOptions,
  cryptoApi: IdempotencyCrypto | undefined = globalThis.crypto,
) {
  const bytes = new TextEncoder().encode(JSON.stringify([principal, options.scope, options.payload ?? null]));
  const fingerprintValue = await fingerprint(bytes, cryptoApi);
  const storageKey = `idempotency:v2:${fingerprintValue}`;
  let lifecycleStorageKey: string | undefined;
  if (options.lifecycle) {
    const lifecycleBytes = new TextEncoder().encode(JSON.stringify([principal, options.lifecycle]));
    const lifecycleFingerprint = await fingerprint(lifecycleBytes, cryptoApi);
    lifecycleStorageKey = `idempotency:v2:lifecycle:${lifecycleFingerprint}`;
    const previousStorageKey = storage.getItem(lifecycleStorageKey);
    if (previousStorageKey && previousStorageKey !== storageKey) storage.removeItem?.(previousStorageKey);
    storage.setItem(lifecycleStorageKey, storageKey);
  }
  const idempotencyKey = storage.getItem(storageKey) || createIdempotencyKey(cryptoApi);
  storage.setItem(storageKey, idempotencyKey);
  return { storageKey, idempotencyKey, lifecycleStorageKey };
}

export function clearIdempotency(
  storage: IdempotencyStorage,
  prepared: Awaited<ReturnType<typeof prepareIdempotency>>,
) {
  storage.removeItem?.(prepared.storageKey);
  if (prepared.lifecycleStorageKey && storage.getItem(prepared.lifecycleStorageKey) === prepared.storageKey) {
    storage.removeItem?.(prepared.lifecycleStorageKey);
  }
}
