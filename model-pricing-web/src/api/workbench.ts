import type { IsoDateTime } from "../domain/common";
import { apiRequest } from "./http";

export interface WorkbenchMetricDTO {
  id: string;
  label: string;
  value: string;
  suffix?: string;
  hint: string;
  tone: "NORMAL" | "SUCCESS" | "WARNING" | "DANGER";
}

export interface WorkbenchTaskDTO {
  id: string;
  title: string;
  module: string;
  description: string;
  count: number;
  level: "NORMAL" | "WARNING" | "URGENT";
  routeKey: string;
  routePath: string;
}

export interface WorkbenchNoticeDTO {
  id: string;
  title: string;
  description: string;
  occurredAt: IsoDateTime;
}

export interface WorkbenchDTO {
  metrics: WorkbenchMetricDTO[];
  tasks: WorkbenchTaskDTO[];
  notices: WorkbenchNoticeDTO[];
  generatedAt: IsoDateTime;
}

interface TodoWireDTO {
  id: string | number;
  biz_type: string;
  biz_id: string | number;
  title: string;
  priority: "LOW" | "MID" | "HIGH";
  status: "OPEN" | "DONE";
  created_at: IsoDateTime;
  deeplink: string;
  assignee_name: string;
}

interface MetricWireDTO {
  key: string;
  title: string;
  value: string;
  unit: string;
  trend: "UP" | "DOWN" | "FLAT" | "NA";
  deeplink: string;
}

function internalDeeplink(path: string) {
  if (path.startsWith("/quotes") || path.startsWith("/supplier/quotes")) return "/internal/supplier-quotes";
  if (path.startsWith("/customer-quotes") || path.startsWith("/customers")) return "/internal/customers";
  if (path.startsWith("/price-books")) return "/internal/price-books";
  if (path.startsWith("/pricing")) return "/internal/pricing/policies";
  if (path.startsWith("/suppliers")) return "/internal/suppliers";
  if (path.startsWith("/fx")) return "/internal/finance";
  return "/internal/workbench";
}

export const workbenchApi = {
  get: async (): Promise<WorkbenchDTO> => {
    const [todos, metrics] = await Promise.all([
      apiRequest<{ list: TodoWireDTO[] }>({ url: "/internal/workbench/todos", method: "GET", params: { page: 1, size: 20, status: "OPEN" } }),
      apiRequest<{ cards: MetricWireDTO[] }>({ url: "/internal/workbench/metrics", method: "GET" }),
    ]);
    return {
      metrics: metrics.cards.map(card => ({
        id: card.key,
        label: card.title,
        value: card.value,
        suffix: card.unit,
        hint: card.trend === "NA" ? "实时统计" : `趋势 ${card.trend}`,
        tone: card.trend === "UP" ? "WARNING" : card.trend === "DOWN" ? "SUCCESS" : "NORMAL",
      })),
      tasks: todos.list.map(todo => ({
        id: String(todo.id),
        title: todo.title,
        module: todo.biz_type,
        description: `${todo.assignee_name || "待分派"} · ${todo.status}`,
        count: 1,
        level: todo.priority === "HIGH" ? "URGENT" : todo.priority === "MID" ? "WARNING" : "NORMAL",
        routeKey: "",
        routePath: internalDeeplink(todo.deeplink),
      })),
      notices: [],
      generatedAt: new Date().toISOString(),
    };
  },
};
