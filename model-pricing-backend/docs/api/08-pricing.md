# 定价与价目表 E（内部门户 M6 / M7）

> **状态：草案。阶段 8 未开始，接口全部未实现。**
> 依据：设计 §8.8、需求 §8。通用约定见 `README.md`。

---

## 0. 数据字典

### 0.1 `pricing_policy`（定价策略）

| 字段 | 说明 |
|---|---|
| `id` / `name` | 策略 |
| `scope_type` / `scope_id` | 作用范围：`GLOBAL` / `FAMILY` / `SKU` / `CUSTOMER_LEVEL` |
| `priority` | 优先级，数字越小越优先 |
| `markup_type` / `markup_value` | 加价方式：百分比 / 固定额 / 阶梯 |
| `floor_rule` | 红线策略：低于 floor 时 `BLOCK` / `WARN` / `ALLOW` |
| `status` | `DRAFT` / `ACTIVE` / `ARCHIVED` |

### 0.2 `price_book` / `price_book_item` / `price_book_component`（价目表，**不可变版本**）

| 字段 | 说明 |
|---|---|
| `price_book.version_no` / `is_current` | 版本号 + 部分唯一索引（最多一个当前版本） |
| `level_code` | 客户等级（如 `STANDARD` / `GOLD` / `DIAMOND`） |
| `currency` | 币种（价目表按**模型币种**出，不做跨币种折算） |
| `price_book_item.floor_price` | **floor = 完全成本 /(1 − 15%)**，销售侧唯一下限 |
| `price_book_item.cost_baseline_id` | **记录了"由哪个成本基线算出"**（可追溯，红线） |
| `price_book_component.unit_price` | 逐组件售价 |

> 结果表必须记录「由哪条策略 + 哪个成本基线算出」——否则事后无法解释这个价是怎么来的。

---

## 1. 策略配置

```
GET  /api/internal/pricing/policies?page=1&size=20
POST /api/internal/pricing/policies
PUT  /api/internal/pricing/policies/{id}
```

- 权限：`M6:V` / `M6:E`（定价运营）。
- POST/PUT 幂等（**必填** `Idempotency-Key`）。
- 请求字段：见 0.1。`markup_value` 是**金额字符串**。
- 响应 `data`：`{ id, name, scope_type, scope_id, priority, markup_type, markup_value, floor_rule, status }`

## 2. 生成价目表草稿

```
POST /api/internal/price-books
```

- 权限：`M7:E`；幂等（**必填** `Idempotency-Key`）。
- 请求：`{ "level_code": "STANDARD", "currency": "CNY", "policy_ids": [...], "sku_ids": [...] }`
  （`sku_ids` 为空 = 全量在架 SKU）
- 行为：**不落正式版本**，生成草稿 + 差异报告。对每个 SKU：
  取当前 `cost_baseline` → 套用优先级最高的命中策略 → 算售价 → 算 floor → 红线校验。
- 响应 `data`：
  - `draft_id` / `level_code` / `currency` / `item_count`
  - `diff_report[]`：`{ sku_id, sku_code, old_price, new_price, delta_pct, floor_price, floor_violation }`
  - `blocked_count`：红线被 `BLOCK` 的条数（>0 时不允许发布）

## 3. 发布价目表

```
POST /api/internal/price-books/{id}/publish
```

- 权限：`M7:E` + **双人审批**；幂等（**必填** `Idempotency-Key`）。
- 请求：`{ "effective_time": "...", "mode": "IMMEDIATE"|"SCHEDULED"|"GRAY", "gray_percent": 10 }`
- 约束：草稿里存在 `floor_violation = BLOCK` 的条目 → `409`，必须先处理。
- 行为：创建 `change_request` + 2 步审批；审批全过才在 `effective_time` 生效：
  新版本 INSERT + 旧版本关闭 + `event_outbox('price.effective')` + `cache_version + 1`。
- 响应 `data`：`{ price_book_id, version_no, change_request_id, step_count: 2, effective_time, status }`

## 4. 回滚

```
POST /api/internal/price-books/{id}/rollback
```

- 权限：`M7:E` + 双人审批；幂等。
- **回滚不是删除，是"上一版快照重新发一个新版本"**，走**与发布完全相同的流程**
  （同样的红线校验、同样的审批、同样的事件）。这样历史可追溯。
- 请求：`{ "target_version_no": 3, "reason": "..." }`，`reason` 必填。

## 5. 涨价传导决策队列

> 成本涨了，售价要不要跟涨？系统**不自动改价**，只生成决策队列让人来定。

```
GET /api/internal/price-upconduction?page=1&size=20&status=
```

- 权限：`M7:V`。
- 响应 `data.list[]`：
  - `sku_id` / `sku_code` / `level_code`
  - `cost_before` / `cost_after` / `cost_delta_pct`
  - `price_current` / `price_suggested` / `floor_price`
  - `margin_before` / `margin_after`（**仅对有成本权限的角色返回**）
  - `status`：`PENDING` / `FOLLOWED` / `NOT_FOLLOWED`
  - `frozen_until`：**冻结期**，超过则自动按默认规则处理

```
POST /api/internal/price-upconduction/{id}/decide
```

- 权限：`M7:E`；幂等。
- 请求：`{ "decision": "FOLLOW"|"NOT_FOLLOW", "override_price": "0.00001234", "reason": "..." }`
- `NOT_FOLLOW` 且售价低于 floor → `409`，必须走特价审批（见 `09-customer.md`）。

---

## 6. 待裁决点

1. 策略优先级的冲突消解：多条策略同时命中时，只取最高优先级还是叠加？
2. 灰度发布（`GRAY`）的判定放网关还是本系统？本系统是否要记录灰度白名单？
3. 涨价传导队列的"冻结期"默认多长、超时后的默认动作是什么。
4. `margin` 字段的剔除规则（`field_mask`）是否对所有非定价角色一律剔除。
