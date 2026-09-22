import { ref } from "vue";
import type { Role } from "./types";
export const role = ref<Role>("MODEL_OPS");
export const roleNames: Record<Role, string> = {
  MODEL_OPS: "林悦 · 模型运营",
  PRICING_OP: "周婷 · 定价运营",
  VIEWER: "只读观察员",
};
