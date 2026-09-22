import Decimal from "decimal.js";

export type Money = string;

export function isDecimalAmount(value: unknown, allowZero = false): value is string {
  if (typeof value !== "string" || !/^\d+(\.\d{1,8})?$/.test(value.trim())) return false;
  const amount = new Decimal(value.trim());
  return amount.isFinite() && (allowZero ? amount.greaterThanOrEqualTo(0) : amount.greaterThan(0));
}
