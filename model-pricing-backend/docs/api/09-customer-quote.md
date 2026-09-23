# 客户报价 E（内部门户 M8/M9 + 客户门户）

> **状态：阶段 9a 已实现。** §1 客户列表/移交与 §2 报价生成已注册真实路由；§3—§6 仍为草案、尚未实现。
> 依据：设计 §8.8 / §8.9、需求 §8。通用约定见 `README.md`。

---

## 0. 数据字典

### 0.1 `customer_quote` / `customer_quote_item`（客户报价，**不可变版本**）

| 字段 | 说明 |
|---|---|
| `version_no` / `is_current` | 版本号 + 部分唯一索引 |
| `customer_id` / `level_code` | 客户与客户等级 |
| `quote_type` | `APPLY`（套用标准价）/ `CLONE`（克隆）/ `TEMP`（临时报价）/ `SPECIAL`（特价）/ `CONTRACT`（合同价） |
| `valid_from` / `valid_to` | 有效期；`TEMP` 类型必须有限期 |
| `status` | `DRAFT` / `PENDING` / `APPROVED` / `EFFECTIVE` / `EXPIRED` / `REJECTED` |
| `floor_price` / `below_floor` | 快照 floor 与是否破线 |
| `price_book_id` / `cost_baseline_id` | 由哪个价目表 / 成本基线算出 |

### 0.2 `customer_price_book`（合同价）

合同期内价格**受保护**：成本上涨时不自动跟涨，除非合同里有跟涨条款。

---

## 1. 客户列表（内部）

```
GET /api/internal/customers?page=1&size=20&keyword=
```

- 权限：`M8:V`；**行级过滤**：销售为 `SELF`（只看自己的客户），主管可见部门及下级。

```
POST /api/internal/customers/{id}/transfer
```

- 权限：`M8:E` + 仅主管；幂等。
- 请求：`{ "to_operator_id": 7, "reason": "...", "confirm": true }`（双确认，同供应商移交）。
- **历史报价随迁**，原归属留档。

## 2. 生成客户报价

### 2.1 报价创建上下文

```
GET /api/internal/customers/{id}/quote-context?page=1&size=100
```

- 权限：`M9:V`；沿用客户销售归属数据域。
- 返回客户 `level_code`、当前 `EFFECTIVE` 价目表及 SKU 售价项，以及该客户历史报价分页列表和售价项。
- 用于 APPLY 提交前预览及 CLONE 来源下拉；不返回成本、floor 或毛利。
- 当前等级没有生效价目表时 `price_book=null`，历史报价仍正常返回。

```
POST /api/internal/customer-quotes
```

- 权限：`M9:E`；幂等（**必填** `Idempotency-Key`）。
- 请求：
  ```json
  {
    "customer_id": 3,
    "quote_type": "APPLY",
    "source_quote_id": null,
    "valid_to": "2026-12-31T00:00:00+08:00",
    "items": [ { "sku_id": 40, "unit_price": "0.00001234" } ]
  }
  ```
  - `quote_type=APPLY`：套用当前生效价目表，`items` 可省略（全量带出）。
  - `quote_type=CLONE`：克隆 `source_quote_id` 的价格。
  - `quote_type=TEMP`：`valid_to` **必填**（临时报价必须有到期日）。
- **floor 硬性校验**：任一 `unit_price < floor_price` → `409`，错误响应 `data.floor_violations[]` 返回 `sku_id / sku_code / unit_price / floor_price`；特价审批（§3）尚未实现。
- 响应 `data`：`{ id, customer_id, version_no, status, quote_type, item_count, below_floor_count, floor_violations[] }`

## 3. 申请特价

```
POST /api/internal/customer-quotes/{id}/special-price
```

- 权限：`M9:E`；幂等（**必填** `Idempotency-Key`）。
- 请求：`{ "reason": "战略客户，首年补贴", "expected_margin": "0.08" }`，`reason` 必填。
- 行为：创建 `change_request` + 审批（低于 floor 越多，审批链越长）。
- 响应 `data`：`{ quote_id, change_request_id, margin_impact: {...}, status: "PENDING" }`
- **成本 / 毛利明细对销售角色不可见**（`field_mask` 剔除），只给 `margin_impact` 的聚合结论。

## 4. 刷新客户报价

```
POST /api/internal/customer-quotes/{id}/refresh
```

- 权限：`M9:E`；幂等。
- 行为：按最新成本基线重算，生成**新版本**（不可变版本语义）。
- 请求：`{ "reason": "成本上涨，季度刷新" }`
- 响应 `data`：`{ id, version_no, changed_items[], status }`；值未变则**不产生新版本**，
  返回 `changed_items: []`。

## 5. 导出

```
GET /api/internal/customer-quotes/{id}/export?format=pdf|xlsx
```

- 权限：`M9:V`。
- **临时报价（`TEMP`）导出的文件带水印**。
- ⚠️ 本接口**直返文件流**，不是统一响应信封（与 CSV 模板下载一致，README §3 需补一句例外说明）。

---

## 6. 客户门户 `/api/customer/*`

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/price-book` | 当前生效价目表（本等级，分币种） |
| GET | `/quotes` | 我的报价与合同 |
| POST | `/quotes/{id}/accept` | 接受报价 → 转合同价（幂等） |
| GET | `/billing` | 账单 / 余额 / 授信占用 / 押金状态（**计费系统只读透传**） |
| GET | `/notifications` | 涨价 / 退役通知 |
| GET | `/home` | H5 首页聚合：余额 + 待处理 + 未读通知 + 常用模型 |

- 客户门户账号与内部/供应商门户隔离（`portal_type` 唯一约束）。
- **成本、毛利、他人价格一律不可见**。

---

## 7. 待裁决点

1. 特价审批链长度怎么按"破线幅度"分档。
2. 合同价在成本上涨时的处理：是否要生成涨价传导决策项。
3. 客户接受报价后是否立即转合同价、还是需内部确认。
4. H5 首页聚合接口是否要拆成多个并行请求（前端并发）以缩短首屏时间。
