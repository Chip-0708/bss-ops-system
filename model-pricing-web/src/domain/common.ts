export type IsoDateTime = string;

export interface PageResult<T> {
  list: T[];
  total: number;
  page: number;
  size: number;
}

export class ApiError extends Error {
  constructor(
    public readonly httpStatus: number,
    message: string,
    public readonly code?: string | number,
    public readonly requestId?: string,
    public readonly fieldErrors?: Record<string, string>,
    public readonly retryable = false,
    public readonly details?: unknown,
  ) {
    super(message);
    this.name = "ApiError";
  }
}
