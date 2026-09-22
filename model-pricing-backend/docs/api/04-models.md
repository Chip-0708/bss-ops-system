# 模型管理 M1（内部门户）

> 阶段 4 接口契约。字段以已落库的 DDL（`migrations/000003_model.up.sql`）为准，
> 状态机见设计文档 §9：`DRAFT → PENDING_VERIFY → PURCHASABLE → PUBLISHED → DEPRECATING → OFFLINE`。
> 通用约定（响应包 / 分页 / 错误码 / 字段剔除）见 [`README.md`](./README.md)。

---

## 0. 数据字典：`model_sku`

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | int64 | 主键 |
| `vendor_id` | int64 | 厂商 ID → `vendor.id` |
| `family_id` | int64 | 系列 ID → `model_family.id` |
| `sku_code` | string(128) | SKU 编码，全局唯一，如 `gpt-5-2026-04-11` |
| `model_type` | string(24) | 对话 / 推理 / 多模态 / 向量 / … |
| `native_currency` | string(3) | 原厂币种，`USD` / `CNY`（**字符串**，非数字金额） |
| `context_window` | int \| null | 上下文窗口 |
| `capability` | object \| null | 能力参数（jsonb，结构见 §0.1） |
| `verify_status` | string(16) | `UNVERIFIED` / `MANUAL` / `PROBED` |
| `tier_tag` | string(16) \| null | 旗舰 / 主力 / 经济 / 长尾 |
| `is_sensitive` | bool | 是否敏感模型 |
| `cross_border` | bool | 是否跨境 |
| `lifecycle_status` | string(16) | `DRAFT` / `PENDING_VERIFY` / `PURCHASABLE` / `PUBLISHED` / `DEPRECATING` / `OFFLINE` |
| `sunset_date` | string \| null | 下线日期，`YYYY-MM-DD` |

列表接口额外返回冗余展示字段：`vendor_name`、`family_name`。

### 0.1 `capability` 结构（已定稿）

固定 key 集合，**未知 key 服务端拒绝写入**（避免 jsonb 变成黑盒）：

| key | 类型 | 说明 |
|---|---|---|
| `function_call` | bool | 支持函数调用 / 工具调用 |
| `vision` | bool | 支持图像输入 |
| `audio` | bool | 支持音频输入/输出 |
| `video` | bool | 支持视频输入 |
| `embedding` | bool | 可用作向量模型 |
| `reasoning` | bool | 推理/思维链型模型 |
| `json_mode` | bool | 支持结构化输出 |
| `streaming` | bool | 支持流式响应 |
| `max_output_tokens` | int \| null | 最大输出长度 |

示例：

```json
{
  "function_call": true,
  "vision": true,
  "audio": false,
  "video": false,
  "embedding": false,
  "reasoning": true,
  "json_mode": true,
  "streaming": true,
  "max_output_tokens": 32768
}
```

---

## 1. 模型库列表

```
GET /api/internal/models
```

- 权限点：`M1:V` ｜ 幂等：否
- 支持两种视图：`view=family` 按系列折叠、`view=sku` 展开到 SKU

**请求参数（Query）**

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `view` | string | 否 | `family`（默认）/ `sku` |
| `keyword` | string | 否 | 名称 / 编码 / SKU 模糊匹配，1~64 |
| `vendor_id` | int64 | 否 | 厂商筛选 |
| `family_id` | int64 | 否 | 系列筛选 |
| `model_type` | string | 否 | 见数据字典枚举 |
| `lifecycle_status` | string | 否 | 见数据字典枚举 |
| `tier_tag` | string | 否 | 档位筛选 |
| `page` / `size` | int | 否 | 见通用约定 |

**响应 `data`**

```json
{
  "list": [
    {
      "id": 1001,
      "sku_code": "gpt-5-2026-04-11",
      "model_type": "对话",
      "native_currency": "USD",
      "context_window": 128000,
      "capability": { "function_call": true },
      "verify_status": "PROBED",
      "tier_tag": "旗舰",
      "is_sensitive": false,
      "cross_border": true,
      "lifecycle_status": "PUBLISHED",
      "sunset_date": null,
      "vendor_id": 7,
      "vendor_name": "OpenAI",
      "family_id": 33,
      "family_name": "GPT-5",
      "aliases": ["gpt5", "gpt-5-latest"]
    }
  ],
  "total": 137,
  "page": 1,
  "size": 20
}
```

`view=family` 时，分页单位为系列，`list` 每项包含
`family_id / family_name / vendor_id / vendor_name / sku_count / children`。
`children` 为命中系列下的完整 SKU 数组；筛选条件只决定系列是否进入结果，
不会裁剪已命中系列的 `children`。例如关键字命中系列内一个 SKU 时，展开后仍返回该系列全部 SKU。

**错误码**：400 参数非法 ｜ 401 ｜ 403 无 M1:V ｜ 423

---

## 1.1 厂商与系列选项

```
GET /api/internal/models/options
```

- 权限点：`M1:V` ｜ 幂等：否
- 返回全部厂商和系列，供模型筛选与新建表单使用。

```json
{
  "vendors": [{ "id": 7, "name": "OpenAI" }],
  "families": [{ "id": 33, "vendor_id": 7, "name": "GPT-5" }]
}
```

**错误码**：401 ｜ 403 无 M1:V

---

## 2. 创建 SKU

```
POST /api/internal/models
```

- 权限点：`M1:E` ｜ 幂等：**是**（必须带 `Idempotency-Key`）
- 新建后 `lifecycle_status = DRAFT`

**请求体**

| 字段 | 类型 | 必填 | 校验 |
|---|---|---|---|
| `vendor_id` | int64 | 是 | 存在 |
| `family_id` | int64 | 是 | 存在且属于该 vendor |
| `sku_code` | string | 是 | 1~128，全局唯一（重复 → 400） |
| `model_type` | string | 是 | 枚举 |
| `native_currency` | string | 是 | 3 位大写 |
| `context_window` | int | 否 | ≥ 0 |
| `capability` | object | 否 | 固定 key 集合，见 §0.1 |
| `tier_tag` | string | 否 | 枚举 |
| `is_sensitive` | bool | 否 | 默认 false |
| `cross_border` | bool | 否 | 默认 false |
| `aliases` | string[] | 否 | 每个全局唯一 |

**响应 `data`**：新建 SKU 完整对象（同列表项结构）。
**错误码**：400（含 `sku_code` 重复、别名重复）｜ 401 ｜ 403 ｜ 409 幂等冲突 ｜ 423

---

## 3. 维护 SKU

```
PUT /api/internal/models/{id}
```

- 权限点：`M1:E`
- 与早期设计文档 §8.3 的差异：当前实现使用 `PUT /api/internal/models/{id}` 定位待维护 SKU。

**请求体**：同创建接口字段（除 `vendor_id` / `family_id` 迁移需单独接口外均可改）；
`lifecycle_status` **不可直接改**，须走 `publish` / `deprecate` / `batch` 流程。

**响应 `data`**：更新后的完整对象。
**错误码**：400 ｜ 401 ｜ 403 ｜ 404 SKU 不存在 ｜ 423

---

## 4. 别名维护与查重建议

```
POST /api/internal/models/{id}/aliases
GET  /api/internal/models/aliases/suggest?keyword=xxx
```

> 查重是独立 GET 集合路径，只传 `keyword`，不传 SKU ID；别名维护仍按 SKU ID 走 POST。

- 权限点：`M1:E`（写） / `M1:V`（查重）

**写请求体**

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `aliases` | string[] | 是 | 全量覆盖式提交；每个 1~128，全局唯一 |
| `source` | string | 否 | `MANUAL`（默认）/ `IMPORT` |

**查重响应 `data`**（Top3）

```json
{ "suggestions": [ { "sku_id": 1001, "sku_code": "gpt-5-2026-04-11", "score": 0.92 } ] }
```

**错误码**：400（别名重复指向其他 SKU）｜ 401 ｜ 403 ｜ 404

---

## 5. 一键合并为别名

```
POST /api/internal/models/aliases/merge
```

- 权限点：`M1:E` ｜ 幂等：**是**
- 场景：供应商反馈的"新模型"其实是已有模型的别名，合并后回填供应商

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `target_sku_id` | int64 | 是 | 保留的主 SKU |
| `source_sku_id` | int64 | 是 | 被合并的 SKU（将成为别名） |
| `alias` | string | 是 | 被合并方的名称，作为别名写入（source=`MERGE`） |

**响应 `data`**

```json
{ "target_sku_id": 1001, "merged_sku_id": 1002, "alias_id": 55, "alias": "gpt-5-turbo" }
```

**错误码**：400（target 与 source 相同 / 别名已存在）｜ 401 ｜ 403 ｜ 404 ｜ 409

---

## 6. 批量改状态 / 打标签

```
POST /api/internal/models/batch
```

- 权限点：`M1:E` ｜ 幂等：**是**
- 允许的批量动作：`PENDING_VERIFY`（提交验证）、`tier_tag`、标签增删
- ⚠️ 上架 / 退役**不允许**批量走此接口，必须逐个走 `publish` / `deprecate`

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `sku_ids` | int64[] | 是 | 1~200 个 |
| `action` | string | 是 | `SUBMIT_VERIFY` / `SET_TIER` / `ADD_TAG` / `REMOVE_TAG` |
| `payload` | object | 否 | 动作参数（如 `tier_tag`、`tag`） |

**响应 `data`**

```json
{ "total": 12, "succeeded": 11, "failed": [ { "sku_id": 1003, "code": 10001, "message": "状态不允许该操作" } ] }
```

**说明**：批量为**部分成功**语义，前端需展示失败明细。

---

## 7. 退役影响分析（B8）

```
GET /api/internal/models/{id}/deprecation-impact
```

- 权限点：`M1:V`
- 发起退役前**必须先调用**本接口；`deprecate` 会校验影响清单是否存在（缺失则拒绝提交）

**响应 `data`**

| 字段 | 说明 |
|---|---|
| `snapshot_id` | **必存**：这是响应字段；发起退役（§8）时将其值放入请求字段 `impact_snapshot_id`，缺失 → 400。清单 24 小时有效（`expires_at`） |

```json
{
  "sku_id": 1001,
  "snapshot_id": "cdefc0c1-e544-4409-b0e1-2cd3f015e550",
  "references": {
    "price_books":  [{ "id": 12, "name": "标准价目表", "level_code": "BASIC" }],
    "contracts":    [{ "id": 88, "customer_name": "某客户", "expire_at": "2026-12-31" }],
    "customer_quotes": [{ "id": 301, "status": "FORMAL" }]
  },
  "reference_count": 5,
  "replacements": [{ "sku_id": 1007, "sku_code": "gpt-5-2026-10-01", "reason": "同系列更新版本" }],
  "generated_at": "2026-09-11T10:00:00+08:00"
}
```

**错误码**：401 ｜ 403 ｜ 404

---

## 8. 发起退役

```
POST /api/internal/models/{id}/deprecate
```

- 权限点：`M1:E` ｜ 幂等：**是**
- **双人审批**：提交后进入审批流，当前操作员不能审批自己提交的退役单
- 前置：`lifecycle_status ∈ {PUBLISHED, PURCHASABLE}`；且请求须带影响清单

**请求体**

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `impact_snapshot_id` | string | 是 | 第 7 步返回的影响清单标识（落库表 `deprecation_impact`，缺失 → 400） |
| `sunset_date` | string | 是 | `YYYY-MM-DD`，须晚于今天 |
| `reason` | string | 是 | 退役理由，1~500，入审计 |
| `replacement_sku_id` | int64 | 否 | 推荐替代 SKU |

**响应 `data`**

```json
{
  "sku_id": 1001,
  "approval_id": 9001,
  "sunset_date": "2026-12-31",
  "lifecycle_status": "PUBLISHED"
}
```

> ⚠️ **两套状态必须分开看**（2026-09-14 澄清，前端同事提问）：
>
> | 状态 | 载体 | 取值 |
> |---|---|---|
> | **审批单状态** | `change_request.status` + `approval_step` | `PENDING`（待第 1 签 / 待第 2 签）→ `APPROVED` / `REJECTED` |
> | **模型生命周期** | `model_sku.lifecycle_status` | `PUBLISHED` → `DEPRECATING` → `DEPRECATED` |
>
> - **提交申请时生命周期状态不变**（返回当前值，如 `PUBLISHED`），
>   只创建 `change_request`（`PENDING`）+ 2 步 `approval_step`。
> - **两步都 APPROVED 后**才把 SKU 条件更新为 `DEPRECATING`，并从可售/可新采清单移除。
> - **任一步 REJECTED** → `change_request` 置 `REJECTED`，**SKU 状态完全不变**，
>   因此**不存在"驳回回滚"的问题**（提交时没改过，无需回滚）。
> - 到达 `sunset_date` 才转为 `DEPRECATED` 并发 `model.deprecated` 事件给网关。
>
> 依据：需求 §4.6 时序图「第 2 签完成 → MD 状态置『即将下线』」。
>
> **另注意**：`model_sku.sunset_date` 在**审批通过前是 `NULL`**，
> 该值此时只存在于 `change_request.payload` 里；审批通过后才写入 SKU。
> 前端在"待审批"阶段不要从 SKU 详情读 `sunset_date`（会是空），要从审批单详情取。

> ⚠️ **已知缺口（2026-09-14，前端同事提出，已确认）**
>
> 需求 §4.6 要求「客户通知 ≥30 天提前期」，且顺序是**通知 → 双人审批 → 下线执行**。
> 但当前后端：
> 1. **没有"通知发出时间"这个数据** —— 全库无通知表，也无 `notification_sent_at` 之类字段
>    （客户通知属阶段 F/11，未实现）。所以 30 天目前**无从算起**。
> 2. `sunset_date` **只校验"晚于今天"**，没有 30 天下限（实测填 3 天后被接受）。
>
> **口径（后端权威校验，前端只做提示）**
> - 阶段 F 之前（过渡口径）：提交时校验 `sunset_date ≥ 今天 + deprecate_notice_days`
>   （`sys_config` 新增，默认 30），不满足 → `400`，并在 `message` 里给出最早允许日期。
> - 阶段 F 通知功能上线后（最终口径）：以**通知实际发出时间**为准，
>   新增 `deprecation_notice` 表记 `sent_at`；**审批通过时**权威校验
>   `sunset_date − MIN(sent_at) ≥ 30 天`，不满足 → `409` 并提示最早可下线日期。
> - 前端可以提前置灰/提示，但**后端必须拦**，不依赖前端。

**错误码**：400（清单缺失 / 日期非法 / 未达 30 天通知期）｜ 401 ｜ 403 ｜ 404 ｜ 409 幂等冲突

---

## 9. 上架

```
POST /api/internal/models/{id}/publish
```

- 权限点：`M1:E`（定价运营）｜ 幂等：**必须携带 `Idempotency-Key`**
- 前置：`lifecycle_status = PURCHASABLE`，否则 400

**请求体**：`{ "remark": "可选备注" }`

**响应 `data`**

```json
{ "sku_id": 1001, "lifecycle_status": "PUBLISHED", "published_at": "2026-09-11T10:00:00+08:00" }
```

**错误码**：400 状态不允许 ｜ 401 ｜ 403 ｜ 404 ｜ 409

---

## 10. 已定稿决策（2026-09-11）

| # | 议题 | 结论 |
|---|---|---|
| 1 | `capability` 结构 | **固定 key 集合**（见 §0.1），未知 key 拒绝写入，不改成独立表 |
| 2 | 退役影响清单 | **落库**：新增 `deprecation_impact` 表，分析接口响应返回 `snapshot_id`；发起退役请求使用 `impact_snapshot_id`，并校验清单存在且未过期 |
| 3 | 批量接口上限 | 单次最多 **200** 个 SKU，超出 400 |
| 4 | `aliases` 写接口 | **全量覆盖**语义：前端提交当前全部别名，服务端做差集增删，避免并发歧义 |
| 5 | 上架是否需要审批 | **不需要**，定价运营直接上架（状态须为 `PURCHASABLE`） |

### 10.1 `deprecation_impact` 表（阶段 4 新增迁移 000010）

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | bigint PK | 主键 |
| `snapshot_id` | varchar(64) UNIQUE | 对外返回的清单标识（UUID） |
| `sku_id` | bigint NOT NULL | 被分析 SKU |
| `impact_refs` | jsonb NOT NULL | 引用的价目表 / 合同 / 客户报价明细（**不叫 references**：SQL 保留字） |
| `reference_count` | int NOT NULL | 引用总数 |
| `replacements` | jsonb NULL | 推荐替代 SKU 列表 |
| `expires_at` | timestamptz NOT NULL | 清单有效期（建议 24h），过期后发起退役需重新分析 |
| `created_by` / `created_at` | — | 审计字段 |
