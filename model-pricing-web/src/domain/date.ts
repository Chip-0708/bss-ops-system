export function formatDateTime(value?: string | null, empty = "—", seconds = false): string {
  if (!value || !Number.isFinite(Date.parse(value))) return empty;
  return new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", ...(seconds ? { second: "2-digit" as const } : {}),
  }).format(new Date(value));
}
