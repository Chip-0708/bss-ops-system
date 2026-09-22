import { apiRequest } from "./http";
import type { PortalType } from "../stores/session";

export interface LoginResult {
  token: string;
  snapshot: {
    account_id: number | string;
    portal_type: string;
    operator_id: number | string;
    roles: { code: string }[] | null;
    perms: string[] | null;
  };
}

export const login = (portal: PortalType, loginID: string, password: string) =>
  apiRequest<LoginResult>({ url: `/${portal}/auth/login`, method: "POST",
    data: { login_id: loginID, password }, anonymous: true });

export const logout = (portal: PortalType) =>
  apiRequest<void>({ url: `/${portal}/auth/logout`, method: "POST", noContent: true });
