// Current read-only baseline list from the Go backend. Field masks may remove unit_cost.
export interface CurrentCostBaselineDTO {
  sku_id: number | string;
  sku_code: string;
  version: number;
  valid_from: string;
  currency: string;
  primary_supplier_id: number | string;
  primary_supplier_name: string;
  unit_cost?: string;
  unit_cost_basis?: string;
  supplier_count: number;
  single_point: boolean;
  floor_price?: string | null;
}

export interface CurrentCostListParams {
  page: number;
  size: number;
  keyword?: string;
  only_single_point?: boolean;
}
