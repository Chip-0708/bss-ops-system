# 工作台与审计 F（内部门户）

> **状态：草案。阶段 10 未开始，接口全部未实现。**
> 依据：设计 §8.10、需求 §9。通用约定见 `README.md`。

> 阶段 0–5 已经往 `todo_task` / `alert` / `audit_log` 里写数据了
> （报价待审批、到期预警、异常告警、补录超限…），**但没有任何查询接口**。
> 所以数据是有的、看不见。本阶段至少要把"看得见"补上。

---

## 1. 数据字典（已有表）

| 表 | 说明 |
|---|---|
| `todo_task` | 待办。`biz_type`：`QUOTE`（报价待审批）/ `QUOTE_EXPIRE`（到期预警）；`status`：`OPEN` / `DONE`；`priority`：`LOW` / `MID` / `HIGH` |
| `alert` | 告警。`alert_type`：`QUOTE_EXPIRE` / `QUOTE_ANOMALY` / `QUOTE_ACTIVATE_FAILED` / `RETRO_LIMIT`；`severity`：`LOW` / `MID` / `HIGH` / `CRITICAL`；`status`：`OPEN` / `HANDLING` / `RESOLVED` / `IGNORED` |
| `audit_log` | 审计日志，含 `action` / `target_type` / `target_id` / `before_value` / `after_value` / `reason` / `source_type` / `request_id` |

> **去重口径**（阶段 5d 已定稿，本阶段查询时沿用）：
> 待办按 `(biz_type, biz_id, priority)` 去重；告警按 `(alert_type, target_type, target_id, severity)` 去重。

---

## 2. 待办聚合

```
GET /api/internal/workbench/todos?page=1&size=20&biz_type=&status=OPEN
```

- 权限：已认证即可（**按角色自动裁剪**，不声明模块权限点）。
- 行级过滤：按数据域过滤（采购只看自己引入的供应商相关待办）。
- 响应 `data.list[]`：
  - `id` / `biz_type` / `biz_id` / `title` / `priority` / `status` / `created_at`
  - `deeplink`：前端跳转路径（如 `/quotes/28/approval`）
  - `assignee_name`

## 3. 指标卡

```
GET /api/internal/workbench/metrics
```

- 权限：已认证；**按角色裁剪**返回不同的卡片集合。
- 响应 `data.cards[]`：`{ key, title, value, unit, trend, deeplink }`

| 角色 | 卡片 |
|---|---|
| 采购 | 待审批报价数、本月生效报价数、30 天内到期数、单点依赖数 |
| 定价运营 | 待发布价目表、破 floor 项数、成本上涨待决策数 |
| 销售 | 我的客户数、待确认报价数、本季度成交额 |
| 财务 | 待锁定汇率月份、待处理押金/授信 |

## 4. 告警列表与处理

```
GET  /api/internal/alerts?page=1&size=20&severity=&status=&alert_type=
POST /api/internal/alerts
```

- 权限：GET `F:V`；POST `F:E`。
- POST（标记处理 / 转工单）幂等（**必填** `Idempotency-Key`）：
  ```json
  { "alert_id": 7, "action": "HANDLE"|"RESOLVE"|"IGNORE"|"TO_TICKET", "note": "...", "create_todo": true }
  ```
- 响应 `data`：`{ alert_id, status, todo_id }`（`create_todo=true` 时返回新建待办 id）。

## 5. 审计查询

```
GET /api/internal/audit-logs?page=1&size=20&action=&target_type=&target_id=
    &operator_id=&source_type=&from=&to=
```

- 权限：`F:V`（审计只读角色也有）。
- 支持**多维筛选 + 前后值对比 + 影响追溯**。
- `source_type`：`HUMAN` / `WORKER` / `AGENT` / `SYSTEM`
  —— **Agent 只能写 `staging_price` 与建议类结果，绝不能直接改 `price_version` /
  `cost_baseline` / `price_book`**；查 `source_type=AGENT` 就是查 Agent 干了什么。
- 响应 `data.list[]`：
  - `id` / `action` / `target_type` / `target_id` / `operator_name` / `operator_role`
  - `before_value` / `after_value`（**JSON 对象**，前端做 diff 展示）
  - `reason` / `source_type` / `request_id` / `created_at`

## 6. 审计导出

```
GET /api/internal/audit-logs/export?from=&to=&format=xlsx|csv
```

- 权限：`F:V`（审计只读）。
- ⚠️ 直返文件流，不是统一响应信封。

---

## 7. 本阶段要一并处理的遗留

1. **`audit_log.operator_id` 命名空间撞号**：`internal_staff` 与 `subject_operator` 都有 `id=1`，
   现在靠 `operator_role` 字段区分。本阶段统一治理（建议拆成 `internal_operator_id` +
   `subject_operator_id` 两列，或引入统一主体表）。
2. **定时任务 ticker 骨架**（阶段 6 引入 worker 时一起做）：
   `ACTIVATE_QUOTE` 分钟级；`quote-expire-scan` 每日 07:00、`quote-expire-final` 07:10、
   `quote-anomaly-scan` 07:20、`cost-recalc` 每日 06:00、`qual-expire-scan` 每日 08:00。
   需要 `cron_lock` 做单实例防重。

---

## 8. 待裁决点

1. 待办是否需要"指派给他人"（当前 `assignee_id` 在创建时固定）。
2. 指标卡的口径是否要缓存（当前是实时聚合，数据量大后可能慢）。
3. 审计日志保留多久、是否需要分区表（写入量会很大）。
4. `operator_id` 撞号治理的改法（加列 vs 统一主体表）。
