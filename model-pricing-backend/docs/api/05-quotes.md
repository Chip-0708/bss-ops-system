# 供应商与报价 C（供应商门户 + 内部门户 M3/M4）

> 阶段 5 接口契约。字段以已落库的 DDL 为准：`migrations/000002_subject.up.sql`（`supplier_profile`）、
> `migrations/000004_quote_cost.up.sql`（`quote_sheet` / `quote_item` / `quote_component`）。
> 通用约定（响应包 / 分页 / 错误码 / 字段剔除）见 [`README.md`](./README.md)。
>
> **本文件是契约源头**，后端实现以本文件为准；`openapi/swagger.json` 是代码注解生成的**产物**。
>
> 覆盖范围：`C3 提交报价` / `C4 批量导入` / `C7 报价历史` / `C8 审批` / `C9 到期闭环` / `C11 特权补录` / `D4 异常检测`。
> 不在本阶段：供应商注册与资质（C1/C2/C10）、新模型申请（C5/C12）、成本重算本体（M5）、价目表（M7）。

---

## 0. 数据字典

### 0.1 `quote_sheet`（报价单：不可变版本）

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | int64 | 主键 |
| `supplier_id` | int64 | 供应商 ID → `supplier_profile.id` |
| `version_no` | int | 版本号，同一供应商内单调递增（服务端计算，不接受入参） |
| `status` | string(24) | `DRAFT` / `SUBMITTED` / `APPROVING` / `APPROVED_PENDING` / `EFFECTIVE` / `EXPIRED` / `VOIDED` / `REJECTED` |
| `valid_from` | string | 生效时间（ISO 8601 带时区），必填 |
| `valid_to` | string | 有效期止，必填，必须 `> valid_from` |
| `source` | string(16) | `MANUAL` 手工 / `IMPORT` 批量导入 / `RETRO` 特权补录 / `SILENT_FOLLOW` 官方调价静默跟随 |
| `retroactive` | bool | 是否补录（仅 `RETRO` 为 true） |
| `audit_reason` | string \| null | 审计理由（`RETRO` 必填） |
| `change_request_id` | int64 \| null | 静默跟随的触发变更单（本阶段恒为 null） |
| `submitted_by` | int64 \| null | 提交人 `subject_operator.id`；系统生成为 null |
| `submitted_at` | string \| null | 提交时间 |

**本阶段新增列（migration `000012`）**：`reject_reason`、`approved_by`、`approved_at`、`activated_at`、`remove_confirmed`、`grace_until`。见 §11.1。

### 0.2 `quote_item`（报价明细）

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | int64 | 主键 |
| `quote_sheet_id` | int64 | 所属报价单 |
| `sku_id` | int64 | SKU ID → `model_sku.id` |
| `currency` | string(3) | **锁定 = SKU 的 `native_currency`**，接口不接受改值 |
| `fx_tier` | string \| null | 汇率档位，仅 USD 模型；见 §0.4 |
| `constraints_` | object \| null | 非价格约束，结构见 §0.5 |

> 唯一约束 `UNIQUE(quote_sheet_id, sku_id)`：**一张报价单里同一 SKU 只能一行**。

### 0.3 `quote_component`（逐组件报价）

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | int64 | 主键 |
| `quote_item_id` | int64 | 所属明细行 |
| `component_type` | string(32) | 组件类型，固定集合见 §0.3.1 |
| `multiplier` | string \| null | 倍率（相对官方价）；**绝对价模式为 null** |
| `unit_price` | string | 折算后单价（每百万 token），**金额一律字符串** |

#### 0.3.1 `component_type` 固定集合（已定稿）

`input` / `output` / `cached_input` / `cache_write_5m` / `cache_write_1h` / `reasoning` /
`embedding` / `request` / `image_input` / `image_output` / `audio_input` / `audio_output`

- **未知 key 服务端拒绝写入**（避免 jsonb 变成黑盒，与 `capability` 同一纪律）。
- 一张明细行至少要有 **1 个组件**。
- 计价口径统一为**每百万 token 单价**；`request`（按次调用）沿用同字段，语义为「每次调用单价」。

### 0.4 汇率档位（仅 USD 模型）

固定 8 档，传其他值一律 400：

```
6.5 / 6.7 / 6.75 / 6.8 / 6.85 / 6.9 / 6.95 / 7.0
```

- USD 模型：`fx_tier` **必填**，服务端取当月 `fx_rate_lock` 值作为默认值建议（前端可带出，用户仍须选择）。
- CNY 模型：**不接受** `fx_tier`，传了服务端置 `null`（不报错，不回显）。
- 本阶段不实现 `fx_rate_lock` 的锁定接口（M5·财务），默认档位取 `6.8`。
- **匹配按数值等价，不按字符串**：`"6.8"` / `"6.80"` / `"6.800"` 全部命中同一档位，
  响应回显规范形式 `"6.8"`；`"6.86"` 这类真不在档位里的仍返回 400。
  （理由：5c 的 CSV 导入是人工填写，`6.80` 被拒会很莫名；列本身是 `numeric(6,3)`，
  数值本就等价。判定用 decimal 比较，不做浮点运算。）

### 0.5 `constraints_` 结构（已定稿）

固定 key 集合，**未知 key 服务端拒绝写入**：

| key | 类型 | 说明 |
|---|---|---|
| `rpm` | int \| null | 每分钟请求数上限 |
| `tpm` | int \| null | 每分钟 token 数上限 |
| `concurrency` | int \| null | 并发数上限 |
| `daily_quota` | int \| null | 日配额 |
| `actual_context` | int \| null | 实际可用上下文（可能小于标称） |
| `compatibility` | string \| null | 兼容度说明，如 `"OpenAI 兼容"` |

示例：

```json
{ "rpm": 3000, "tpm": 2000000, "concurrency": 64, "actual_context": 128000, "compatibility": "OpenAI 兼容" }
```

### 0.6 `supplier_profile`（供应商档案，本阶段只读）

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | int64 | 主键 |
| `subject_id` | int64 | 主体 ID → `legal_subject.id` |
| `channel_type` | string(24) | 官方直连 / 官方转售 / 中转聚合 / 逆向 |
| `settle_type` | string(16) | `PREPAID` / `MONTHLY` |
| `billing_cycle` | int | 账期（天） |
| `min_recharge` | string | 最低起充 |
| `credit_line` | string | 后付额度 |
| `credit_used` | string | 已用额度 |
| `deposit_amount` | string | 保证金 |
| `settle_status` | string(16) | `NORMAL` / `WARNING` / `FROZEN` |
| `qual_status` | string(16) | `VALID` / `EXPIRING` / `FROZEN` |
| `owner_procurement_operator_id` | int64 | 归属采购（行级过滤键） |
| `status` | string(16) | `ACTIVE` / `INACTIVE` |

> **字段剔除**：`settle_type` / `billing_cycle` / `min_recharge` / `credit_line` / `credit_used` / `deposit_amount`
> 属商务敏感字段，按设计 §3.2 对**非财务且非归属采购**的角色物理剔除（key 不存在，不是 null）。

---

## 1. 状态机（报价单）

```
DRAFT ──提交──> SUBMITTED ──进入待办──> APPROVING ──通过──> APPROVED_PENDING ──到达生效时间──> EFFECTIVE
                                            │                                                      │
                                            └──驳回──> REJECTED（终态）                    到期/剔除/被覆盖
                                                                                                   ↓
                                                                                               EXPIRED
```

| 迁移 | 触发方 | 约束 |
|---|---|---|
| `* → SUBMITTED` | 供应商提交（§3）/ 导入确认（§6） | 幂等；非终态互斥（见 §1.1） |
| `SUBMITTED → APPROVING` | 提交事务内立即完成 | 同事务 INSERT `todo_task` |
| `APPROVING → APPROVED_PENDING` | 采购通过（§8） | 幂等；`M4:A` + 归属校验 |
| `APPROVING → REJECTED` | 采购驳回（§9） | 终态；原因必填，供应商可见 |
| `APPROVED_PENDING → EFFECTIVE` | 激活（§10） | **同事务**关闭旧 `EFFECTIVE` |
| `EFFECTIVE → EXPIRED` | 到期剔除（§12）/ 被新版本覆盖（§10） | 人工确认，禁止自动剔除 |

### 1.1 非终态互斥（业务规则，DB 无唯一索引兜底）

同一供应商存在 `SUBMITTED` / `APPROVING` / `APPROVED_PENDING` 任一条时，**再提交新版本返回 409**。

理由：审批队列里同时挂多条互相冲突的报价，采购无从判断哪条有效；且这类"堆版本"会让
`uk_quote_pending` 部分唯一索引在并发下成为唯一防线，报错信息对供应商不可读。

> DB 层另有 `uk_quote_effective`（最多一条 EFFECTIVE）与 `uk_quote_pending`（最多一条 APPROVED_PENDING）
> 两个部分唯一索引做并发兜底，命中时返回 409。

---

## 2. 供应商：可报价 SKU 列表

```
GET /api/supplier/skus?page=1&size=20&keyword=&vendor_id=&family_id=
```

- 权限：仅 AuthN（SUPPLIER 角色无内部权限点），服务层按登录主体隔离。
- 返回 `lifecycle_status ∈ ('PUBLISHED','PURCHASABLE','PENDING_VERIFY')` 的 SKU；已 `DEPRECATING` / `OFFLINE` 的不出现。
- **不含任何他人报价 / 平台成本 / 毛利**（隔离红线）。

**响应 `data.list[]`**

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | int64 | SKU ID（提交时用它，禁止自由填写模型名） |
| `sku_code` | string | SKU 编码 |
| `model_name` | string | 展示名（family 名 + SKU） |
| `vendor_id` | int64 | 厂商 ID（模板按厂商范围下载时使用） |
| `vendor_name` | string | 厂商 |
| `family_id` | int64 | 系列 ID（模板按系列范围下载时使用） |
| `family_name` | string | 系列 |
| `model_type` | string | 对话 / 推理 / … |
| `native_currency` | string(3) | `USD` / `CNY`（**报价币种锁定为此值**） |
| `context_window` | int \| null | 上下文窗口 |
| `tier_tag` | string \| null | 旗舰 / 主力 / 经济 / 长尾 |
| `official_price` | object \| null | 当前官方价基准，**只读**，结构见下 |
| `has_official_price` | bool | false ⇒ 该 SKU **只能走绝对价模式**（`multiplier` 必须 null） |

```json
"official_price": {
  "version_no": 3,
  "currency": "USD",
  "tax_basis": "NET",
  "components": [
    { "component_type": "input",  "unit_price": "2.50000000" },
    { "component_type": "output", "unit_price": "10.00000000" }
  ]
}
```

> `official_price` 为 `null` 时前端必须禁用倍率输入框，只留绝对价。

---

## 3. 供应商：提交报价

```
POST /api/supplier/quotes
Idempotency-Key: <uuid>
```

**请求体**

```json
{
  "valid_from": "2026-09-20T00:00:00+08:00",
  "valid_to":   "2026-12-19T00:00:00+08:00",
  "remark":     "季度续报",
  "items": [
    {
      "sku_id": 12,
      "fx_tier": "6.8",
      "constraints": { "rpm": 3000, "tpm": 2000000, "concurrency": 64 },
      "components": [
        { "component_type": "input",  "multiplier": "0.800000", "unit_price": "2.00000000" },
        { "component_type": "output", "multiplier": "0.750000", "unit_price": "7.50000000" }
      ]
    }
  ]
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `valid_from` | 是 | 生效时间。< **now 一律钳制为提交时刻**（防改账，供应商无补录特权） |
| `valid_to` | 是 | 有效期止，必须 `> valid_from` |
| `remark` | 否 | 备注，写入 `audit_reason`（≤500 字） |
| `items` | 是 | 至少 1 行，最多 500 行 |
| `items[].sku_id` | 是 | 必须在 §2 返回的可报价集合内 |
| `items[].fx_tier` | USD 必填 | CNY 模型传了会被置 null |
| `items[].constraints` | 否 | 见 §0.5，未知 key → 400 |
| `items[].components` | 是 | 至少 1 个；`component_type` 必须在固定集合内且行内不重复 |
| `components[].multiplier` | 条件 | 倍率模式必填；绝对价模式必须 `null`（SKU 无官方价时强制 null） |
| `components[].unit_price` | 是 | 折算后单价，字符串 |

**服务端校验顺序**（任一失败即 400，错误信息可直接展示）

1. 幂等占位（同 key 同 body 重放返回首次结果；同 key 异 body 返回 400）。
2. `items` 非空、行数上限、`sku_id` 无重复、全部在可报价集合内。
3. `valid_from < now` → 钳制为 `now()`（**不报错，静默钳制**，并在响应 `clamped: true` 告知）。
4. `valid_to > valid_from`。
5. 币种锁定：`quote_item.currency = sku.native_currency`（不接受入参）。
6. `fx_tier` 档位校验（§0.4）。
7. **`unit_price` 与 `multiplier` 双向自洽复核**：取该 SKU 当前 `price_version`（`is_current=true` 且同币种）
   的组件价 `official`，校验 `|unit_price − official × multiplier| ≤ 1e-4`（绝对容差）。不自洽 → 400。
   绝对价模式（`multiplier=null`）跳过此校验。
8. 非终态互斥（§1.1）→ 409。
9. `constraints` / `component_type` 未知 key → 400。

**落库（单事务）**

- `quote_sheet`：`version_no = max(同供应商)+1`，`status = APPROVING`，`source = MANUAL`，
  `submitted_by = 当前 subject_operator.id`，`submitted_at = now()`。
- `quote_item` / `quote_component` 逐行逐组件写入。
- `todo_task`：`biz_type='QUOTE'`, `biz_id=quote_sheet.id`, `assignee_id = supplier.owner_procurement_operator_id`,
  `title='报价待审批：<供应商名> v<version_no>'`, `priority='MID'`。
- 幂等表 `MarkResult`（与业务同事务，见 §8.0.1）。

**响应 `data`**

```json
{
  "id": 31,
  "supplier_id": 5,
  "version_no": 2,
  "status": "APPROVING",
  "valid_from": "2026-09-20T00:00:00+08:00",
  "valid_to": "2026-12-19T00:00:00+08:00",
  "source": "MANUAL",
  "retroactive": false,
  "clamped": false,
  "item_count": 1,
  "submitted_at": "2026-09-11T21:30:00+08:00"
}
```

---

## 4. 供应商：报价历史

```
GET /api/supplier/quotes/history?page=1&size=20&status=&sku_id=&from=&to=
```

- 只返回**登录主体自己**的报价（服务层按 `supplier_id` 硬过滤，不接受入参指定）。
- 含被覆盖 / 过期 / 驳回的全部历史版本（可追溯倍率、价格与审批结论）。
- **不含任何他人报价 / 平台售价 / 毛利**（接口层物理剔除）。

**响应 `data.list[]`**

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | int64 | 报价单 ID |
| `version_no` | int | 版本号 |
| `status` | string | 见 §1 |
| `valid_from` / `valid_to` | string | 生效区间 |
| `source` | string | `MANUAL` / `IMPORT` / `RETRO` |
| `item_count` | int | 明细行数 |
| `submitted_at` | string \| null | 提交时间 |
| `decision` | object \| null | 审批结论，见下 |

```json
"decision": {
  "result": "REJECTED",
  "reason": "output 组件高于市场最低价 32%，请复核后重新提交",
  "decided_at": "2026-09-12T10:00:00+08:00"
}
```

> `result` 取值 `APPROVED` / `REJECTED`；未审批时为 `null`。
> **`decision.reason` 供应商可见**——驳回原因必须能指导供应商改价，不做脱敏。
> `decided_at` 取值：`APPROVED` → `approved_at`；`REJECTED` → `rejected_at`（migration 000015 新增列）。

---

## 5. 供应商：报价详情

```
GET /api/supplier/quotes/{id}
```

- 越权（不是自己的报价单）→ **404**（不返回 403，避免探测他人 ID 是否存在）。

**响应 `data`**：报价单头 + `items[]`（每个 item 含 `sku_code`、`model_name`、`currency`、`fx_tier`、
`constraints`、`components[]`）+ `decision`。**不含官方价以外任何成本信息**。

---

## 6. 批量导入（C4）

四步式：**模板下载 → 上传解析 → 逐行校验预览 → 确认入库**。

### 6.1 下载模板

```
GET /api/supplier/quotes/template?scope=history|vendor|family|active&vendor_id=&family_id=
```

| `scope` | 含义 |
|---|---|
| `history` | 本供应商报过价的 SKU |
| `vendor` | 指定厂商的全部可报价 SKU（需 `vendor_id`） |
| `family` | 指定系列的全部可报价 SKU（需 `family_id`） |
| `active` | 全部当前可报价 SKU（默认） |

- **格式：CSV（UTF-8 带 BOM）**。MVP 不引入 xlsx 生成依赖；`?format=xlsx` 预留但本阶段返回 400。
- **列按可报价 SKU 的官方价组件并集动态生成**（component_type 排序后输出，列顺序稳定）：
  - 固定锁定列：`sku_id`、`sku_code`、`model_name`（只读）、`currency`（锁定）
  - 官方价只读列：`official_<type>` × N 组件（动态并集）
  - 可填列：`fx_tier`（USD 行必填）、`multiplier_<type>` / `price_<type>` × N 组件（动态并集）、
    `rpm` / `tpm` / `concurrency` / `daily_quota` / `actual_context` / `compatibility`
  - 只读参考列：`last_valid_to`（该 SKU 上次报价的到期日）
  - **`valid_from` / `valid_to` 不在 CSV 里**——「一个文件 = 一个版本」，生效区间是整单的，
    由 confirm 请求体传入（见 §6.3）。
- **绝不含他人报价 / 平台成本 / 毛利**（隔离红线）。

### 6.2 上传校验预览

```
POST /api/supplier/quotes/import/preview
Content-Type: multipart/form-data
```

- 字段 `file`（CSV，≤ 2 MB，≤ 500 行）。
- **服务端不保存文件、不落库**，逐行校验后把结果返回前端；`confirm` 时前端把**修正后的行**整体回传，
  服务端**重新全量校验**（不信任前端）。

**响应 `data`**

```json
{
  "token": "c1f8...（= 本文件 sha256，仅用于前端幂等展示）",
  "total": 3,
  "ok_count": 1,
  "warn_count": 1,
  "error_count": 1,
  "rows": [
    { "line": 2, "sku_id": 12, "sku_code": "gpt-5-2026-04-11", "level": "OK",   "messages": [] },
    { "line": 3, "sku_id": 15, "sku_code": "claude-x",        "level": "WARN", "messages": ["官方价基准已变动，已按最新官方价重算"] },
    { "line": 4, "sku_id": 999, "sku_code": "not-exist",      "level": "ERROR","messages": ["SKU 不存在或不可报价"] }
  ],
  "preview_items": [
    {
      "sku_id": 12, "currency": "USD", "fx_tier": "6.8",
      "constraints": { "rpm": 3000 },
      "components": [
        { "component_type": "input", "multiplier": "0.800000", "unit_price": "2.00000000" }
      ]
    }
  ]
}
```

- `level`：`OK` / `WARN`（可入库，需提示）/ `ERROR`（**不得入库**）。
- `preview_items` 只含 `OK` + `WARN` 行，供前端二次编辑后回传。
- 校验项与 §3 一致（SKU 命中 / 币种锁定 / 汇率档位 / 组件类型白名单 / 约束 key 白名单），
  **但倍率与价格的填法比 §3 宽松**（见下）。

**⚠️ 倍率与价格的填法（导入专用，与 §3 手工提交不同）**

手工录入时前端实时互转，所以 §3 要求两值都传。批量导入是**离线填表，没有互转能力**，
要求用户手算 `price = official × multiplier` 是不现实的 —— 那样导入功能就废了。
故导入允许以下三种填法：

| 填法 | 处理 |
|---|---|
| 两值都传 | 复核自洽（容差 1e-4），不自洽 → **ERROR** |
| **只传 `multiplier_<type>`** | **服务端按当前官方价算出 `price = official × multiplier`**（导入的主要用法） |
| **只传 `price_<type>`** | **绝对价模式**，`multiplier` 置 null；无官方价的 SKU 只能这样填 |
| 两个都不传 | 该组件不参与（跳过该组件） |

- 「官方价基准已变动」的 WARN **只在倍率模式下判定**：绝对价模式没有官方价基准，不触发。
- 重算口径：`price = 当前官方价 × multiplier`（`multiplier` 保留不变）。

### 6.3 确认入库

```
POST /api/supplier/quotes/import/confirm
Idempotency-Key: <uuid>
```

请求体与 §3 同构（`valid_from` / `valid_to` / `remark` / `items[]`）。
`valid_from` / `valid_to` **不在 CSV 里**，由本请求体传入（一个文件 = 一个版本，
生效区间必然是整单的；CSV 模板不包含这两列）。

校验：

- **与 §3 手工提交共用同一套九步校验**（不信任前端，全量重校验）。
- 不做「必须是 preview_items 的 sku 子集」校验 —— 该约束技术上无法闭环：
  preview 不落库、confirm 请求体也不携带 preview 结果，后端无从得知 preview 的 sku 集合。
  且九步校验本身已拒绝任何非法行；若某行合法但前端未预览过，入库并无危害。
  「confirm 的 items 应来自 preview_items」属**前端纪律**，不由后端强制。
- 落库：复用 §3 的事务，仅 `source = 'IMPORT'`，审计 `action = 'QUOTE_IMPORT'`。
- **不跳过审批**：同样进入 `APPROVING` + `todo_task`。

---

## 7. 内部：待审批报价

```
GET /api/internal/quotes/pending?page=1&size=20&supplier_id=
```

- 权限：`M4:V`；**行级过滤**：`supplier.owner_procurement_operator_id` 按数据域过滤
  （SELF = 我引入的供应商；DEPT/DEPT_SUB/ALL 按设计 §3.2）。

**响应 `data.list[]`**

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | int64 | 报价单 ID |
| `supplier_id` / `supplier_name` | int64 / string | 供应商 |
| `version_no` | int | 版本 |
| `item_count` | int | 明细行数 |
| `source` | string | `MANUAL` / `IMPORT` / `RETRO` |
| `retroactive` | bool | 补录单高亮 |
| `valid_from` / `valid_to` | string | 生效区间 |
| `submitted_at` | string | 提交时间 |
| `wait_hours` | number | 已等待小时数（SLA 用，1 位小数） |
| `has_previous` | bool | 是否存在上一版本（决定 diff 是否有对比基准） |

---

## 8. 内部：审批通过

```
POST /api/internal/quotes/{id}/approve
Idempotency-Key: <uuid>
```

- 权限：`M4:A`；**归属校验**：SELF 域下只能批自己引入的供应商（越权 → 403）。
- 前置：`status = APPROVING`，否则 409。
- 幂等：重复提交同 key 返回首次结果；已 `APPROVED_PENDING` 后再次 approve（不同 key）→ 409。

**事务内动作**

1. `quote_sheet.status = APPROVED_PENDING`，`approved_by = 操作员`，`approved_at = now()`。
2. `todo_task` 中该单的待办置 `DONE`。
3. `INSERT task_job(job_type='ACTIVATE_QUOTE', payload={quote_sheet_id}, next_run_at = valid_from)`。
4. 幂等表 `MarkResult`。

**响应 `data`**

```json
{ "id": 31, "status": "APPROVED_PENDING", "approved_at": "2026-09-12T10:00:00+08:00",
  "activate_at": "2026-09-20T00:00:00+08:00", "immediate": false }
```

> `immediate = true` 表示 `valid_from <= now`，激活任务会立刻执行（补录/即时生效场景）。
>
> **实现要求（5b 裁决）**：`immediate = true` 时，approve 的**事务提交之后**必须**同步调用一次**
> `ActivateDueQuotes`（另开事务，不并入 approve 事务）。
> 理由：5b 不交付 cron ticker，若只入队不同步激活，"生效时间已到"的报价单会
> **永久停在 `APPROVED_PENDING` 不生效**——这是功能缺失，不是优化问题。
> `ActivateDueQuotes` 本身幂等（条件更新 `APPROVED_PENDING → EFFECTIVE`，0 行跳过）。

> **预约生效（`valid_from` 在未来）**：依赖定时扫描，本阶段由 `POST /quotes/activate-due`
> 手动触发；分钟级 ticker 待阶段 6 引入 worker 骨架时补齐。

---

## 9. 内部：审批驳回

```
POST /api/internal/quotes/{id}/reject
```

请求体：`{ "reason": "output 组件高于市场最低价 32%，请复核" }`

- 权限：`M4:A` + 归属校验（同 §8）。
- **幂等**：挂 `Idempotency-Key`（CLAUDE.md 坑位表：「审批、发布、补录、汇率锁定、移交、退役都要」）。
  重复驳回返回首次结果，不靠"状态已变 → 409"顶替幂等——409 会让网络重试的前端困惑。
- `reason` 必填，10~500 字（按 **rune** 计，中文一字一算），写入 `quote_sheet.reject_reason`，
  **供应商可见**（§4）。同时写 `rejected_at = now()`（migration 000015）。
- 前置：`status = APPROVING`，否则 409。`REJECTED` 是**终态**，修订必须提交新版本。
- 事务内同时把 `todo_task` 置 `DONE`。

---

## 10. 激活（到达生效时间）

**触发方式二选一，共用同一领域方法 `ActivateDueQuotes(ctx)`**

1. 进程内 ticker：每 60s 一轮（受 `config.cron.enable` 控制，默认开启）。
2. 手动端点（运维 / E2E 用）：

```
POST /api/internal/quotes/activate-due
```

- 权限：`M4:E`。
- 响应 `data`：`{ "scanned": 2, "activated": 1, "items": [ { "id": 31, "supplier_id": 5, "version_no": 2, "closed_previous_id": 28 } ] }`

**单条激活的同事务动作（D-04 修复）**

1. 该供应商当前 `EFFECTIVE` 版本 → `status = EXPIRED`，`valid_to = 本单 valid_from`。
2. 本单 → `status = EFFECTIVE`，`activated_at = now()`。
3. `INSERT task_job(job_type='COST_RECALC', payload={supplier_id, quote_sheet_id, reason})`，
   `reason` = `QUOTE_EFFECTIVE`（补录单为 `RETRO`）。
4. `INSERT event_outbox(event_type='quote.effective', payload={...})`。
5. `task_job(ACTIVATE_QUOTE)` 置 `DONE`。

> `uk_quote_effective` 部分唯一索引做并发兜底；命中时该条跳过并记 `last_error`，不中断整批。
>
> **本阶段不实现成本重算本体**，只入队 `COST_RECALC`（M5 阶段消费）。

### 10.1 SKU 可采购态联动（补 Stage 4 缺口）

激活成功后，把本单明细中 `lifecycle_status = 'PENDING_VERIFY'` 的 SKU 置为 **`PURCHASABLE`**，
`change_reason = 'QUOTE_EFFECTIVE'`。

> 需求图 5-1：`待验证 → 可采购` 的触发条件是"能力验证通过"；本系统里"已有生效供应商报价"
> 是可采购的**必要条件**，因此在这里补齐。Stage 4 遗留的「`PURCHASABLE` 无产生路径」由此关闭。
> `lifecycle_status` 已是 `PURCHASABLE` / `PUBLISHED` 的 SKU 不变。

---

## 11. 特权补录（C11）

```
POST /api/internal/quotes/retro-effective
Idempotency-Key: <uuid>
```

> **路径说明（5d 裁决）**：设计文档 §8.6 原写 `POST /quotes/{id}/retro-effective`，
> 但补录是**创建新报价单**而非修改既有单，`{id}` 无从指代（既不是报价单 id，
> 也不是供应商 id —— 两者语义都说不通）。故改为集合路径，`supplier_id` 放入请求体，
> 与 `POST /api/supplier/quotes`（创建报价）保持同一 REST 语义。

请求体：

```json
{
  "supplier_id": 5,
  "effective_time": "2026-09-01T00:00:00+08:00",
  "valid_to": "2026-11-30T00:00:00+08:00",
  "audit_reason": "补录 9 月 1 日已口头确认并执行的报价",
  "items": [ { "sku_id": 12, "fx_tier": "6.8", "components": [ ... ] } ]
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `supplier_id` | 是 | 补录对象供应商；请求体自包含，不依赖路径参数 |

- 权限：**`M4:P`**（RETRO_OP 角色持有）。
- `effective_time` 必须是**过去时间**（未来时间请用正常提交流程），否则 400。
- `audit_reason` **必填，≥ 10 字**，写入 `quote_sheet.audit_reason` 与 `audit_log`。
- 落库：`source = 'RETRO'`，`retroactive = true`，`submitted_by = 操作员`（非供应商）。
- **审批**：特权补录**免审批**——创建后直接置 `APPROVED_PENDING`，随即因
  `valid_from <= now` 被激活为 `EFFECTIVE`（走 §10 的完整事务，含关闭旧版本 + `COST_RECALC` 入队）。
- 成本重算区间限定：`COST_RECALC` payload 携带 `effective_time`，**只影响该时点起之后的版本，
  绝不向更早回灌**（设计 §5.5）。
- 监控：当月补录次数 > `sys_config.retro_monthly_limit`（默认 5）→ 写 `alert` 告警管理层。

**响应 `data`**

```json
{ "id": 32, "status": "EFFECTIVE", "retroactive": true,
  "closed_previous_id": 30, "cost_recalc_queued": true,
  "retro_count_this_month": 3, "alert_created": false }
```

### 11.1 本阶段新增列（migration `000012`）

```sql
ALTER TABLE quote_sheet ADD COLUMN reject_reason   text NULL;
ALTER TABLE quote_sheet ADD COLUMN approved_by     bigint NULL;
ALTER TABLE quote_sheet ADD COLUMN approved_at     timestamptz NULL;
ALTER TABLE quote_sheet ADD COLUMN activated_at    timestamptz NULL;
ALTER TABLE quote_sheet ADD COLUMN remove_confirmed boolean NOT NULL DEFAULT false;
ALTER TABLE quote_sheet ADD COLUMN grace_until     timestamptz NULL;
```

**migration `000015`**（已应用 000012 后追加，不改已落库迁移）：

```sql
ALTER TABLE quote_sheet ADD COLUMN rejected_at timestamptz NULL;
```

> 驳回时间**不得复用** `approved_at`。两者语义不同，且复用后"审批停留时长"
> 这一指标永远算不出来。

---

## 12. 到期闭环（C9）

### 12.1 到期清单

```
GET /api/internal/quotes/expiring?days=7&only_single_point=false&page=1&size=20
```

- 权限：`M4:V` + 行级过滤。
- 返回 `status = 'EFFECTIVE'` 且 `valid_to <= now() + days` 的报价单。

**响应 `data.list[]`**

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` / `supplier_id` / `supplier_name` | — | 报价单与供应商 |
| `version_no` | int | 版本 |
| `valid_to` | string | 有效期止 |
| `days_left` | int | 剩余天数（负数 = 已过期） |
| `grace_until` | string | 宽限期止 = `valid_to + quote_grace_days`（3 天） |
| `in_grace` | bool | 是否处于宽限期 |
| `remove_confirmed` | bool | 是否已人工确认剔除 |
| `single_point` | bool | **单点依赖**：该单内是否有 SKU 只有这一家有效报价 |
| `alert_level` | string | `NORMAL` / `HIGH` / `URGENT`（见下） |

`alert_level` 规则：

| 条件 | 级别 |
|---|---|
| `days_left > 7` | `NORMAL` |
| `0 <= days_left <= 7` | `HIGH` |
| `days_left < 0` 且未过宽限期 | `URGENT` |
| 已过宽限期且未确认 | `URGENT` + `pending_remove = true` |
| 单点依赖 | 阈值提前至 14 / 7 天，且级别**升一级** |

### 12.2 人工确认剔除

```
POST /api/internal/quotes/{id}/confirm-remove
```

请求体：`{ "confirm": true, "reason": "已确认备用供应商可承接" }`

- 权限：`M4:E` + 归属校验。
- 前置：`status = 'EFFECTIVE'` 且已过宽限期（**未过宽限期 → 409**，提示"仍在宽限期"）。
- `confirm = false` 视为撤销确认（仅当尚未执行剔除）。
- 执行（同事务）：本单 → `EXPIRED`（`remove_confirmed = true`）+
  `INSERT task_job(COST_RECALC, reason='EXPIRE_REMOVE')` + `event_outbox('quote.expired')`。
- **禁止自动剔除**：定时任务只生成待办，绝不直接改状态。

### 12.3 定时扫描（领域方法 + ticker）

| 任务 | 频率 | 动作 | 本阶段 |
|---|---|---|---|
| `quote-expire-scan` | 每日 07:00 | T-7 / T-3 生成 `todo_task` + `alert`；到期日写 `grace_until` | 实现方法 + 手动端点，**不装 OS cron** |
| `quote-expire-final` | 每日 07:10 | **兜底**：处理 `remove_confirmed = true` 但仍未置 `EXPIRED` 的历史遗留 | 同上 |
| `quote-anomaly-scan` | 每日 07:20 | 见 §13 | 同上 |

> **口径澄清（5d 裁决）**：§12.2 的 `confirm-remove` **立即执行剔除**（同事务置 `EXPIRED`），
> 不是"只打标等定时任务"。理由：需求 §4.4 明确「人工确认 → 剔除」；且本阶段无 ticker，
> 只打标等于永远不执行，用户点了看不到任何反应。
> `quote-expire-final` 因此退化为**兜底**（筛 `remove_confirmed=true AND status='EFFECTIVE'`，
> 正常情况下查不到任何行）。实现时请在代码注释写明，避免后来者困惑。

> 与 §10 的 ticker 一样：领域方法 + 手动触发端点 `POST /api/internal/quotes/scan?type=expire|expire-final|anomaly`，
> 进程内 ticker 默认关闭（这三个是"每日一次"，不值得常驻 goroutine；`ACTIVATE_QUOTE` 才是分钟级，默认开启）。

---

## 13. 报价异常检测（D4）

```
GET /api/internal/quotes/anomalies?days=30&page=1&size=20
```

- 权限：`M4:V`；**不做归属过滤**（比价属平台级市场信息，设计 §3.2 决议）。
- 实时计算，不落表。取近 `days` 天内 `status ∈ ('APPROVING','APPROVED_PENDING','EFFECTIVE')` 的报价。

**判定**（逐组件）

- 偏离上一版本：`|price − prev_price| / prev_price > quote_anomaly_pct`（默认 `0.20`）
- 偏离市场最低价：`(price − market_best) / market_best > quote_anomaly_mkt`（默认 `0.30`）

**响应 `data.list[]`**

| 字段 | 类型 | 说明 |
|---|---|---|
| `quote_sheet_id` / `supplier_name` | — | 报价单与供应商 |
| `sku_id` / `sku_code` | — | SKU |
| `component_type` | string | 组件 |
| `unit_price` | string | 本次报价 |
| `prev_price` | string \| null | 同一供应商上一版本同组件价 |
| `delta_pct` | string \| null | 相对上一版本变动率 |
| `market_best` | string \| null | 全市场（所有供应商有效报价）该组件最低价 |
| `mkt_delta_pct` | string \| null | 相对市场最低价偏离率 |
| `reason` | string | `PREV_DEVATION` / `MARKET_DEVIATION` / `BOTH` |
| `detected_at` | string | 检测时间（= 请求时间） |

---

## 14. 差异对比与毛利预演（C8）

```
GET /api/internal/quotes/{id}/diff
```

- 权限：`M4:V` + 归属校验。
- 以 `sku_id` 为键做**三方对照**：上一版本 / 官方价当前版本 / 全市场最低价。

**响应 `data`**

```json
{
  "quote_sheet_id": 31,
  "supplier_id": 5,
  "version_no": 2,
  "distortion": true,
  "distortion_note": "本版各组件倍率完全相同，缓存维度需重点复核",
  "items": [
    {
      "sku_id": 12,
      "sku_code": "gpt-5-2026-04-11",
      "currency": "USD",
      "components": [
        {
          "component_type": "input",
          "unit_price": "2.00000000",
          "multiplier": "0.800000",
          "prev_price": "2.20000000",
          "prev_delta_pct": "-0.0909",
          "official_price": "2.50000000",
          "official_delta_pct": "-0.2000",
          "market_best": "1.90000000",
          "market_delta_pct": "0.0526"
        }
      ],
      "margin_preview": {
        "floor_price": "2.35294118",
        "reference_sell_price": "2.50000000",
        "margin_ok": true,
        "note": "按官方价作为售价基准、min_gross_margin=0.15 预演"
      }
    }
  ]
}
```

**口径说明（重要）**

- `prev_price`：同一供应商 `version_no` 小于当前的最大版本的同组件价；不存在为 `null`。
- `official_price`：该 SKU 当前 `price_version`（`is_current=true`、同币种）组件价。
- `market_best`：所有供应商 `EFFECTIVE` 报价中该组件最低 `unit_price`（**不含平台成本**）。
- `distortion`：本单**所有**明细的**所有**组件 `multiplier` 完全相同 → `true`（前端缓存维度标红）。
  存在 `multiplier = null`（绝对价）时不判定为失真。
- `margin_preview` 为**简化预演**：`floor = unit_price / (1 − min_gross_margin)`，
  `reference_sell_price` 暂取官方价。**真正的毛利预演需 M5 成本参数与 M7 价目表，
  在 Stage 6 补齐**；本阶段 `reference_sell_price` 无官方价时为 `null`，`margin_ok` 为 `null`。

---

## 15. 已定稿决策（2026-09-12）

| # | 事项 | 决策 | 理由 |
|---|---|---|---|
| 1 | 报价审批机制 | **不走 `change_request` + `approval_step`，单步 `M4:A`** | 设计 §8.6 接口形态就是 `approve`/`reject`；需求 §4.1 只画了 1 个审批人；复用退役的审批链会让"自审禁止""强制换人"语义无端复杂化 |
| 2 | 导入文件格式 | **CSV（UTF-8 BOM）**，xlsx 本阶段返回 400 并预留参数 | MVP 不引入 excelize；格式不影响状态机，后续加依赖即可切换 |
| 3 | 导入预览结果存储 | **不落库**，confirm 时前端回传、后端重校验 | 避免新增 staging 表；不信任前端是关键，必须重校验 |
| 4 | 非终态互斥 | 同供应商存在 `SUBMITTED/APPROVING/APPROVED_PENDING` 时提交新版本 → **409** | 防止审批队列堆多条冲突报价；DB 只有 pending 的部分唯一索引兜底 |
| 5 | 过去时间处理 | **静默钳制为 `now()`** 并在响应 `clamped: true` | 设计 §5.1"防改账"；补录特权走 §11 独立通道 |
| 6 | 倍率-价格自洽 | 两值都传，**不自洽 400**（不自动修正） | 设计 §5.1；自动修正会掩盖前端换算 bug |
| 7 | 激活方式 | 领域方法 + **分钟级 ticker（默认开）** + 手动端点 | 激活是核心链路不能只靠人点；`POST /activate-due` 供 E2E 与运维 |
| 8 | 到期/异常扫描 | 领域方法 + 手动端点，ticker **默认关** | 每日一次的任务不值得常驻 goroutine |
| 9 | SKU 可采购态 | 报价 **EFFECTIVE** 时把 `PENDING_VERIFY` 的 SKU 置 `PURCHASABLE` | 关闭 Stage 4 遗留缺口；"有生效报价"是可采购的必要条件 |
| 10 | 剔除前置 | **必须已过宽限期** + 人工确认，禁止自动剔除 | 需求 §4.4 明确约束 |
| 11 | 补录审批 | **免审批**，创建即 `APPROVED_PENDING` 并立即激活 | 特权通道本身已受 `M4:P` + 强制理由 + 月度告警三重约束 |
| 12 | 成本重算 | 本阶段**只入队** `task_job(COST_RECALC)`，不实现重算 | 属 M5，Stage 6 |
| 13 | 供应商注册/资质（C1/C2/C10） | 本阶段**不做接口**，只做种子数据 | 与报价生命周期解耦，可独立成阶段 |
| 14 | 新模型申请（C5/C12） | 本阶段不做 | 属 M1 模型主数据域 |
| 15 | 审计日志（红线 10：100% 写入） | **报价提交本轮就写** `audit_log` | `operator_id` 无外键，供应商 id 存得进；defer 到最后要补 100+ 处，欠债更贵。⚠️ 已知歧义：`operator_id` 在 internal_staff 与 subject_operator 两个命名空间会撞号，靠 `operator_role` 区分，F 阶段统一治理 |
| 16 | 驳回时间字段 | 新增 migration `000015` 补 `rejected_at`，**不复用** `approved_at` | 语义不同；复用后"审批停留时长"永远算不出 |
| 17 | `fx_tier` 匹配 | **数值等价**（`6.8`/`6.80` 同档），响应回显规范形式 | 5c 的 CSV 是人工填写；列本身是 `numeric(6,3)` |
| 18 | 明细行数超限 | 单列哨兵 `ErrQuoteItemsTooMany`（不用 `ErrQuoteItemsEmpty` 兼管） | 语义不同；message 要能明确告知"单次最多 500 行，请拆分" |
| 19 | 单点依赖定级 | **只提前阈值（14/7 天），不再额外升一级** | 契约原文「阈值提前 + 级别升一级」二义：双重升级会让 HIGH 档在单点时不可达（≤14 天全是 URGENT），highThresh=14 变成死逻辑。结论 (a)：只提前阈值，不额外升级 |
| 20 | `only_single_point` 的 total | **跟随过滤后重算**（内存过滤，量小） | 过滤前 total 会导致前端分页算错 |
