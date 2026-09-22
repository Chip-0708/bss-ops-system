import axios, { AxiosError, type AxiosRequestConfig } from "axios";
import { ApiError } from "../domain/common";
import { useSessionStore } from "../stores/session";
import { clearIdempotency, prepareIdempotency, type IdempotencyOptions } from "./idempotency";
import { redactText, redactSensitive } from "../domain/sensitive";

interface ApiResponse<T> {
  code: number;
  message: string;
  data: T;
  requestId: string;
}

interface ApiErrorResponse {
  code?: string | number;
  message?: string;
  requestId?: string;
  fieldErrors?: Record<string, string>;
  data?: unknown;
}

export interface ApiRequestConfig extends AxiosRequestConfig {
  anonymous?: boolean;
  noContent?: boolean;
  idempotency?: IdempotencyOptions;
}

const http = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || "/api",
  timeout: 15_000,
});

http.interceptors.request.use((config) => {
  const session = useSessionStore();
  if (session.token && !(config as ApiRequestConfig).anonymous) config.headers.Authorization = `Bearer ${session.token}`;
  if (import.meta.env.DEV && import.meta.env.VITE_MOCK_ENABLED === "true") {
    config.headers["X-Mock-Identity"] = session.user?.identityKey || "VIEWER";
  }
  return config;
});

function friendlyMessage(status: number) {
  if (status === 400) return "提交内容不符合要求，请检查后重试。";
  if (status === 401) return "登录状态已失效，请重新登录。";
  if (status === 403) return "当前账号没有执行此操作的权限。";
  if (status === 404) return "数据不存在或当前账号不可见。";
  if (status === 409) return "当前数据状态已发生变化，请刷新后重新操作。";
  if (status === 423) return "账号或主体已被冻结，请联系管理员。";
  if (status === 429) return "操作过于频繁，请稍后再试。";
  return "服务暂时不可用，请稍后重试。";
}

function normalizeError(error: unknown) {
  if (error instanceof ApiError) return error;
  if (!(error instanceof AxiosError)) {
    return new ApiError(
      0,
      "请求未完成，请检查网络后重试。",
      "UNKNOWN",
      undefined,
      undefined,
      true,
    );
  }
  const status = error.response?.status || 0;
  const body = error.response?.data as ApiErrorResponse | undefined;
  return new ApiError(
    status,
    redactText(status >= 500 ? friendlyMessage(status) : body?.message || (status ? friendlyMessage(status) : "网络连接失败，请稍后重试。")),
    body?.code || error.code,
    body?.requestId,
    body?.fieldErrors ? redactSensitive(body.fieldErrors) : undefined,
    status === 0 || status >= 500 || status === 429,
    body?.data === undefined ? undefined : redactSensitive(body.data),
  );
}

export async function apiRequest<T>(config: ApiRequestConfig): Promise<T> {
  let idempotency: Awaited<ReturnType<typeof prepareIdempotency>> | null = null;
  try {
    const session = useSessionStore();
    if (config.idempotency) {
      if (!session.user || !session.portal) throw new ApiError(401, "请先登录后再提交。");
      const principal = JSON.stringify([session.portal, session.user.id]);
      idempotency = await prepareIdempotency(sessionStorage, principal, config.idempotency);
      if (principal !== JSON.stringify([session.portal, session.user?.id]))
        throw new ApiError(409, "当前账号已切换，请重新确认后提交。");
    }
    const response = await http.request<ApiResponse<T>>({
      ...config,
      headers: {
        ...config.headers,
        ...(idempotency
          ? { "Idempotency-Key": idempotency.idempotencyKey }
          : {}),
      },
    });
    if (config.noContent && response.status === 204) return undefined as T;
    const body = response.data;
    if (!body || typeof body.code !== "number" || !("data" in body)) {
      throw new ApiError(
        response.status,
        "接口响应格式不符合约定，请联系技术人员。",
        "INVALID_RESPONSE",
        undefined, undefined, true,
      );
    }
    if (body.code !== 0) {
      throw new ApiError(response.status, redactText(body.message || "业务请求失败。"), body.code, body.requestId, undefined, false,
        body.data === undefined ? undefined : redactSensitive(body.data));
    }
    if (idempotency) clearIdempotency(sessionStorage, idempotency);
    return body.data;
  } catch (error) {
    const normalized = normalizeError(error);
    if (normalized.httpStatus === 401 && !config.anonymous) useSessionStore().clear();
    if (
      idempotency &&
      !normalized.retryable &&
      normalized.code !== "IDEMPOTENCY_PROCESSING" &&
      !(normalized.code === 10005 && normalized.message === "幂等冲突")
    ) {
      clearIdempotency(sessionStorage, idempotency);
    }
    throw normalized;
  }
}

// File endpoints return a stream rather than the JSON response envelope.
export async function apiFileRequest(config: ApiRequestConfig): Promise<Blob> {
  try {
    const response = await http.request<Blob>({ ...config, responseType: "blob" });
    if (response.headers["content-type"]?.includes("application/json"))
      throw new ApiError(response.status, "下载接口返回了JSON，请核对文件接口契约。", "INVALID_RESPONSE");
    return response.data;
  } catch (error) {
    if (error instanceof AxiosError && error.response?.data instanceof Blob) {
      try { error.response.data = JSON.parse(await error.response.data.text()); } catch { /* Non-JSON failures use the HTTP fallback. */ }
    }
    const normalized = normalizeError(error);
    if (normalized.httpStatus === 401 && !config.anonymous) useSessionStore().clear();
    throw normalized;
  }
}
