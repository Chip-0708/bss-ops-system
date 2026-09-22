import type { GeneratedPriceBookDTO } from "../api/pricing.types";

export const enabled = import.meta.env.VITE_E2E_ENABLED === "true";
const key = "model-pricing:price-book-quote-e2e:v1";
const pendingKey = `${key}:pending-create`;
export type Stage = "CREATED" | "PUBLISHED" | "PRICING_APPROVED" | "FINANCE_APPROVED" | "COMPLETED";
export interface Run {
  type: "PRICE_BOOK_QUOTE";
  run_id: string;
  stage: Stage;
  customer_id: string;
  level_code: "GOLD";
  price_book_id: string;
  price_book_version: number | null;
  change_request_id: string | null;
  customer_quote_id: string | null;
}
export interface Fixture { customer_id: string; sku_ids: string[]; policy_id: string; currency: string }
interface Pending { operation_id: string; account_id: string; fixture: Fixture }
const id = (value: unknown): value is string => typeof value === "string" && /^[1-9]\d*$/.test(value);
function parse<T>(storage: Storage, name: string): T | null {
  const value = storage.getItem(name);
  if (!value) return null;
  return JSON.parse(value) as T;
}
export function current(): Run | null {
  const run = parse<Run>(localStorage, key);
  if (!run) return null;
  if (run.type !== "PRICE_BOOK_QUOTE" || !id(run.price_book_id) || !id(run.customer_id)) throw new Error("E2E Run 数据无效，请检查浏览器本地存储。");
  return run;
}
export function pending(): Pending | null { return parse<Pending>(localStorage, pendingKey); }
export function begin(accountId: string, fixture: Fixture): Pending {
  const existing = pending();
  if (existing) {
    if (existing.account_id !== accountId) throw new Error("上次创建结果未知，请使用原账号重试。");
    return existing;
  }
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  const record = { operation_id: Array.from(bytes, byte => byte.toString(16).padStart(2, "0")).join(""), account_id: accountId, fixture };
  localStorage.setItem(pendingKey, JSON.stringify(record));
  return record;
}
export function finish(draft: GeneratedPriceBookDTO, record: Pending): Run {
  if (!id(draft.draftId) || draft.levelCode !== "GOLD" || !(draft.itemCount > 0) || draft.blockedCount !== 0) {
    throw new Error(`草稿不可用于 E2E：draft_id=${draft.draftId}，item_count=${draft.itemCount}，blocked_count=${draft.blockedCount}。请检查生成结果。`);
  }
  const run: Run = { type: "PRICE_BOOK_QUOTE", run_id: `e2e-${record.operation_id}`, stage: "CREATED",
    customer_id: record.fixture.customer_id, level_code: "GOLD", price_book_id: draft.draftId,
    price_book_version: null, change_request_id: null, customer_quote_id: null };
  localStorage.setItem(key, JSON.stringify(run));
  localStorage.removeItem(pendingKey);
  return run;
}
export function discardPending() { localStorage.removeItem(pendingKey); }
export function advance(expected: Stage, next: Stage, matches: (run: Run) => boolean, values: Partial<Run> = {}): Run | null {
  const run = current();
  if (!run || run.stage !== expected || !matches(run)) return null;
  const updated = { ...run, ...values, stage: next };
  localStorage.setItem(key, JSON.stringify(updated));
  return updated;
}
export async function loadFixture(): Promise<Fixture> {
  const response = await fetch(`/e2e-fixture.json?t=${Date.now()}`, { cache: "no-store" });
  if (!response.ok || !response.headers.get("content-type")?.includes("application/json"))
    throw new Error("站点根目录缺少 e2e-fixture.json，请先运行准备脚本并放置测试配置。");
  const fixture = await response.json() as Fixture;
  if (!id(fixture.customer_id) || !id(fixture.policy_id) || !Number.isSafeInteger(Number(fixture.policy_id)) ||
      !Array.isArray(fixture.sku_ids) || !fixture.sku_ids.length ||
      !fixture.sku_ids.every(value => id(value) && Number.isSafeInteger(Number(value))) || !fixture.currency)
    throw new Error("E2E fixture 配置无效，请重新运行准备脚本。");
  return fixture;
}
