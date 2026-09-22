export function assertContractId(id: unknown): asserts id is string | number {
  if (typeof id === "number" && !Number.isSafeInteger(id))
    throw new Error("接口返回的ID超出安全范围，请后端以字符串返回大ID。");
  if ((typeof id !== "string" && typeof id !== "number") || !/^[1-9]\d*$/.test(String(id)))
    throw new Error("接口返回的ID格式不正确。");
}
