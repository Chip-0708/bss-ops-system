const sensitiveField = /^(?:authorization|proxyAuthorization|cookie|setCookie|password|passwd|secret|clientSecret|apiKey|accessKey|secretKey|credential|credentials|token|accessToken|refreshToken|idToken|privateKey)$/i;

export function maskCredential(value: unknown): string {
  return typeof value === "string" && value.startsWith("sk-") ? "sk-************" : "************";
}

export function redactText(value: string): string {
  return value
    .replace(/\bsk-[A-Za-z0-9_-]+/g, "sk-************")
    .replace(/\bBearer\s+[A-Za-z0-9._~+\/-]+=*/gi, "Bearer ************")
    .replace(/\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b/g, "************")
    .replace(/((?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|secret|credential)["']?\s*[=:]\s*["']?)[^\s&,;"'<>]+/gi, "$1************")
    .replace(/(https?:\/\/)[^\s/@]+:[^\s/@]+@/gi, "$1************@");
}

export function redactSensitive<T>(value: T): T {
  if (typeof value === "string") return redactText(value) as T;
  if (Array.isArray(value)) return value.map(item => redactSensitive(item)) as T;
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key,
      sensitiveField.test(key.replace(/[-_\s]/g, "")) && item != null
        ? maskCredential(item) : redactSensitive(item),
    ])) as T;
  }
  return value;
}
