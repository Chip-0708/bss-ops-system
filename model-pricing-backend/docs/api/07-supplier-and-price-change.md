# 供应商管理 M3 + 官方价变更 B（内部门户）

> **状态：§5/§6 已实现（阶段 7a）；§1-3 与 §7-9 未实现。**
> 依据：设计 §8.6 / §2.4 / §11 时序图、需求 §6。
> 通用约定见 `README.md`。

> ⚠️ **本节 1–3 是「缺口补录」，不是阶段 7 的原计划。**
> 设计 §8.6 定义了供应商管理 M3 的内部接口，但**代码里一个都没实现**
> （`grep suppliers internal/api/router.go` 零命中）。
> 也就是说现在**没法在内部增删改供应商，也没法移交归属**，供应商只能靠 migration 种子造。
> 前端对接前必须先决定：补实现，还是明确砍掉。

---

## 1. 供应商列表

```
GET /api/internal/suppliers?page=1&size=20&keyword=&qual_status=&status=
```

- 权限：`M3:V` + **行级过滤**：`PROCUREMENT` 角色按 `supplier_profile.owner_procurement_operator_id`
  只可见自己引入的（`SELF`）；主管可见本部门及下级；职能角色可见全量。

**响应 `data.list[]`**

| 字段 | 说明 |
|---|---|
| `id` / `legal_name` | 供应商档案与法人主体名称 |
| `qual_status` | `VALID` / `EXPIRING` / `FROZEN` |
| `settle_status` | 结算状态 |
| `status` | `ACTIVE` / `INACTIVE` |
| `owner_procurement_name` | 归属采购 |
| `sku_count` / `effective_quote_count` | 在供模型数 / 有效报价数 |
| `expiring_soon` | 30 天内到期的报价数 |

## 2. 供应商档案与商务信息

```
GET /api/internal/suppliers/{id}
PUT /api/internal/suppliers/{id}
```

- 权限：`M3:V` / `M3:E`。
- **字段剔除**：商务字段（付款方式、授信额度、押金）对**非财务、非归属采购**的角色剔除。
- PUT 分两段权限：基础信息 `M3:E`；**结算类字段只有财务可写**，采购写了返回 `403`。

## 3. 采购资源移交

```
POST /api/internal/suppliers/{id}/transfer
```

- 权限：`M3:E` + 仅限主管；幂等（**必填** `Idempotency-Key`）。
- 请求：`{ "to_operator_id": 5, "reason": "人员调整", "confirm": true }`
  —— **双确认**：首次 `confirm=false` 返回影响面（在供 SKU、在途报价、待办数），
  二次 `confirm=true` 才执行。
- 行为：改 `owner_procurement_operator_id`；历史报价**不随迁**（仅新数据走新归属），原归属留档。

---

## 4. 官方价变更 B：整体流程

```
采集 sync_job → 暂存 staging_price → 差异比对 → 提交 change_request
   → 两步审批 → 生效：price_version 新版本 → 连锁：报价静默版本 + 成本重算
```

**状态机（`price_version`）**：`DRAFT → PENDING_VERIFY → PUBLISHED`（现行）/
`SUPERSEDED`（被新版本替代）。
**不可变版本**：改价 = 新版本 INSERT + 旧版本 `is_current=false` 并关闭生效期。

## 5. 采集任务

> **状态：已实现（阶段 7a）。**

```
GET  /api/internal/price-sync/jobs?page=1&size=20
POST /api/internal/price-sync/jobs
```

- 权限：GET `M2:V`；POST `M2:E`（模型运营）。
- POST 请求：`{ "job_type": "SYNC_PRICES", "source": "OFFICIAL_SITE", "sku_ids": [...] }`
  - `job_type` 枚举：`SYNC_MODELS` / `SYNC_PRICES` / `SYNC_COMMUNITY`（DDL 注释，非法值 400）。
  - `sku_ids` 可选；仅留 `item_count` 痕迹（`sync_job` 表无 payload 列）。
- 响应 `data`：`{ id, job_type, source, status, started_at, finished_at, error_msg, item_count }`
- **MVP 裁决（7a 裁决 2）**：人工录入模式无采集过程，**创建即完成**——
  `status="SUCCESS"`（DDL 枚举 RUNNING/SUCCESS/FAILED，不自创 "DONE"）、
  `started_at = finished_at = now`、`error_msg = null`。
  P1 自动采集时补状态机（CLAUDE.md 遗留 7a-①）。

## 6. 暂存区

> **状态：已实现（阶段 7a）。**

```
GET  /api/internal/staging-prices?sync_job_id=&page=1&size=20
POST /api/internal/staging-prices
```

- 权限：GET `M2:V`；POST `M2:E` + 幂等（**必填** `Idempotency-Key`，红线 5）。

### 6.1 手工录入（POST，7a 裁决 3 新增契约）

- 请求：
```json
{
  "sync_job_id": 3,
  "items": [
    { "sku_id": 40, "currency": "USD", "payload": {"input": "2.50", "output": "75.00"} },
    { "raw_sku_code": "unknown-sku", "currency": "USD", "payload": {"input": "1.00"} }
  ]
}
```
- `payload` = `{component_type: 十进制字符串}` 扁平 map（7a 裁决 1，遗留 7a-②）；
  key 限 12 种 component_type（§0.3.1 同集合），值必须合法非负 decimal 字符串。
- `sku_id` 与 `raw_sku_code` 二选一（`sku_id` 优先；都给了以 `sku_id` 为准并忽略
  `raw_sku_code`）。只给 `raw_sku_code` 时按 `sku_code` 精确匹配：匹配到回填 `sku_id`
  且保留原始码；匹配不到 `sku_id=null`、`match_status=UNMATCHED`（7a 裁决 6）。
- 校验失败（`sku_id` 不存在 / payload 空或非法 / currency 非 3 位 / 批次不存在）→
  整批 400（批次不存在 404），**不落库、不部分成功**。
- 响应 `data`：`{ created_count, staging_ids: [...] }`；同 key 重放返回首次结果。

### 6.2 列表（GET）

- 响应 `data.list[]`（按 id ASC = 录入序）：
  - `id` / `sync_job_id` / `sku_id` / `raw_sku_code`（未匹配到 SKU 时只有原始码）
  - `currency` / `payload`（逐组件原始采集值）/ `match_status` / `processed` / `created_at`
  - `diff_status`：`NEW` / `CHANGED` / `UNCHANGED` / `UNMATCHED`
  - `diff_detail[]`：`{ component_type, old_price, new_price, delta_pct }`
- **diff 为读侧实时计算**（表无 diff 列），口径（7a 裁决 4）：
  - `UNMATCHED`：`sku_id=null`，`diff_detail=[]`；
  - `NEW`：无「`is_current=true` 且同币种」的当前官方价，`diff_detail` 列出 payload
    全部组件（`old_price=null`、`delta_pct=null`）；
  - `CHANGED`/`UNCHANGED`：只比对 payload 里出现且当前版本也有的组件（payload 多出的
    组件跳过）；数值比较（`"2.50"≡"2.50000000"`），非字符串相等；
  - `old_price` 为 DB `numeric(20,8)` 原文（如 `"2.50000000"`）、`new_price` 为录入原文、
    `delta_pct=(new−old)/old` 6 位小数（如 `"0.120000"`）；`old=0` → `delta_pct=null` 不除零。
  - **前端注意**：价格字符串勿按字符串相等比较（同 06-cost §7 的尾零纪律）。
- `processed` 恒 `false`（7a 裁决 5）；§7 confirm（7b）时置 `true`。

## 7. 确认入正式版本

```
POST /api/internal/staging-prices/confirm
```

- 权限：`M2:E`；幂等（**必填** `Idempotency-Key`）。
- 请求：`{ "sync_job_id": 3, "staging_ids": [...], "effective_time": "2026-10-01T00:00:00+08:00" }`
- 行为：创建 `change_request` + 2 步 `approval_step`；**不直接改 `price_version`**。
- 响应 `data`：`{ change_request_id, step_count: 2 }`

## 8. 审批

复用通用审批入口（**也适用于模型退役**）：

```
POST /api/internal/approvals/{id}/decision
```

- 权限：**不声明模块权限点**——审批权由 `approval_step.required_role` 决定，服务层校验。
  （若在此处硬编码权限点，会与 `required_role` 机制冲突。）
- 幂等（**必填** `Idempotency-Key`）。
- 请求：`{ "action": "APPROVE"|"REJECT", "reason": "..." }`，驳回时 `reason` 必填。
- 响应 `data`：`{ approval_id, step_no, action, next_step_no, status, all_approved }`

> ⚠️ 此接口**已实现**（router.go:125），但 `04-models.md` 里没写，属文档缺口。

## 9. 官方价生效后的连锁

审批全通过 → `price_version` 新版本生效，同事务：

1. 旧版本 `is_current=false`、`valid_to = effective_time`
2. `INSERT task_job(COST_RECALC, reason='OFFICIAL_PRICE_CHANGE')`
3. `INSERT event_outbox('official_price.changed')`
4. **倍率模式报价自动跟随**：生成"静默新版本"（不打扰供应商，记录 `change_reason`）
5. `cache_version + 1`

---

## 10. 待裁决点

1. 供应商管理 M3 到底补不补、补在哪个阶段。
2. "静默新版本"是否需要对供应商可见 / 是否需要通知。
3. 官方价下降时是否也要走完整审批（可能可以走简化流程）。
4. 暂存区 `UNMATCHED`（匹配不到 SKU）的行怎么处理：丢弃、还是走别名申报。
