export const statuses = {
  DRAFT: "草稿",
  PENDING_VERIFY: "待验证",
  PURCHASABLE: "可采购",
  PUBLISHED: "已上架",
  DEPRECATING: "即将下线",
  OFFLINE: "已下线",
} as const;
export type Status = keyof typeof statuses;
export type Role = "MODEL_OPS" | "PRICING_OP" | "VIEWER";
export interface Verification {
  by: string;
  at: string;
  result: "PASS" | "FAIL";
  note: string;
}
export interface Model {
  id: string;
  code: string;
  name: string;
  vendor: string;
  family: string;
  type: string;
  context: number | null;
  capability?: import("./api/models.types").ModelCapabilityDTO | null;
  capabilities: string[];
  protocol: string;
  tier: string;
  sensitive: boolean;
  crossBorder: boolean;
  currency: string;
  input: string;
  output: string;
  status: Status;
  aliases: string[];
  verification: Verification[];
  cost?: string;
  margin?: string;
  pendingRetirement?: boolean;
}
export interface Impact {
  id: string;
  modelId: string;
  books: string[];
  contracts: string[];
  quotes: string[];
  alternatives: string[];
  notifiedAt: string;
}
export interface Retirement {
  replacementId?: string;
  id: string;
  modelId: string;
  status: "APPROVING";
  firstApprover: string;
  offlineAt: string;
  reason: string;
}
export interface BatchResult {
  id: string;
  code: string;
  ok: boolean;
  reason: string;
}
export interface Request {
  method: "GET" | "POST" | "PUT";
  path: string;
  body?: unknown;
  key?: string;
  role: Role;
}
export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}
