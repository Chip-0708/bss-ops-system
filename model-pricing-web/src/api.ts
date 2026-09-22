import { mock } from "./mock";
import { role } from "./session";
import { ApiError } from "./types";
import type { Request } from "./types";
type Transport = (request: Request) => Promise<unknown>;
// Replace this adapter with fetch once the backend contract is confirmed.
let transport: Transport = async (request) => {
  await new Promise((resolve) => setTimeout(resolve, 220));
  return mock.handle(request);
};
export function setTransport(next: Transport) {
  transport = next;
}
export async function request<T>(
  path: string,
  method: Request["method"] = "GET",
  body?: unknown,
): Promise<T> {
  return internalRequest<T>(`/api/internal/models${path}`, method, body);
}
export async function internalRequest<T>(path:string, method:Request['method']='GET', body?:unknown):Promise<T> {
  const signature = JSON.stringify([role.value, method, path, body]);
  const storageKey = `model-demo:request:${signature}`;
  let key: string | undefined;
  if (method !== "GET") {
    key = sessionStorage.getItem(storageKey) || crypto.randomUUID();
    sessionStorage.setItem(storageKey, key);
  }
  try {
    const result = await transport({
      path,
      method,
      body,
      key,
      role: role.value,
    });
    if (key) sessionStorage.removeItem(storageKey);
    return result as T;
  } catch (error) {
    // A definitive validation rejection ends this attempt; uncertain failures retain the key.
    if (error instanceof ApiError && error.status < 500 && error.status !== 409)
      sessionStorage.removeItem(storageKey);
    throw error;
  }
}
