import { apiRequest } from "./http";

export interface OrganizationNodeDTO {
  id: string;
  code: string;
  name: string;
  type: "COMPANY" | "DEPARTMENT" | "TEAM";
  memberCount: number;
  ownerName?: string;
  children?: OrganizationNodeDTO[];
}

export interface RolePermissionDTO {
  code: string;
  module: string;
  action: string;
}

export interface RoleSummaryDTO {
  id: string;
  name: string;
  description: string;
  portal: "internal" | "supplier" | "customer";
  memberCount: number;
  dataScope: "SELF" | "DEPT" | "DEPT_SUB" | "ALL";
  permissions: RolePermissionDTO[];
  fieldRestrictions: string[];
}

// PROVISIONAL: organization source, role DTO and data-scope vocabulary need backend confirmation.
const basePath = "/internal/org-permissions";

export const orgPermissionsApi = {
  getOrganizations: () => apiRequest<OrganizationNodeDTO[]>({ url: `${basePath}/organizations`, method: "GET" }),
  getRoles: () => apiRequest<RoleSummaryDTO[]>({ url: `${basePath}/roles`, method: "GET" }),
};
