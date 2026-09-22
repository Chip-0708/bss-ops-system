# 成本管理 D（内部门户 M5）

> **状态：草案。阶段 6 未开始，接口全部未实现。**
> 依据：需求 §7（D1–D6）、设计 §5.4 / §8.7 / §11。本文件随实现推进定稿。
> 通用约定（响应包、分页、幂等、错误码、字段剔除）见 `README.md`。

---

## 0. 数据字典

### 0.1 `cost_baseline`（成本基线：**不可变版本**）

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` / `sku_id` | — | 主键 / 所属 SKU |
| `version` | int | 单调递增（**列名是 `version`，不是 `version_no`**——以 000004 DDL 为准） |
| `currency` | char(3) | 模型币种（USD / CNY） |
| `valid_from` / `valid_to` | timestamptz | 生效区间；当前版本 `valid_to` 为 `null` |
| `is_current` | bool | 部分唯一索引 `uk_cost_current(sku_id) WHERE is_current` |
| `valid_range` | tstzrange | **`GENERATED ALWAYS AS STORED` 生成列**（非普通列），与 `EXCLUDE USING gist` 配合，**区间不可重叠** |
| `primary_supplier_id` | bigint | 本版本选定的主供应商 |
| `loss_rate` / `channel_rate` | numeric(8,4) | **本版本实际使用的损耗系数 / 通道费率**（值未变判定的一部分，见 §1） |
| `locked_manual` | bool | `MANUAL_LOCK` 时置 `true` |
| `change_reason` | string | 见 0.4 |
| `calc_snapshot` | jsonb | **计算依据快照**：报价版本、官方价版本、参数、四因子得分、备选序列、formula_version（见 §10-9） |
| `backup_sequence` | jsonb \| null | 备选供应商序列（按四因子 total 降序，供网关故障转移） |

> **成本区间（cost_min / cost_max / cost_weighted）不是 `cost_baseline` 的列**——
> 需求 D3 里它是「输出」而非「存储」，由 `GET /compare`（§4）对当前全部 EFFECTIVE 报价**实时计算**，
> 不落库。列表接口（§2）不返回这三列。

### 0.2 `cost_component`（成本基线逐组件）

| 字段 | 类型 | 说明 |
|---|---|---|
| `cost_baseline_id` / `component_type` | — | 联合唯一 `uk_cc` |
| `unit_cost` | 金额字符串 | **完全成本口径**（不是供应商原始报价） |
| `supplier_cost` | 金额字符串 | 供应商原始成本（用于追溯；**列名是 `supplier_cost`，不是 `source_unit_price`**） |

> `component_type` 固定 12 种，与 `price_component` / `quote_component` 完全同一集合（见 05-quotes.md §0.3.1）。
> `multiplier` **不是 `cost_component` 的列**——倍率信息在 `calc_snapshot` 里。

### 0.3 `fx_rate_lock`（汇率锁定，每月 8 档）

档位：`6.5 / 6.7 / 6.75 / 6.8 / 6.85 / 6.9 / 6.95 / 7.0`（与设计 §5.1、05-quotes.md §0.4 同一集合），按月锁定，月中调整需双人审批。

### 0.4 `change_reason` 枚举

`QUOTE_EFFECTIVE` 报价生效 / `OFFICIAL_PRICE_CHANGE` 官方调价 / `RETRO` 特权补录 /
`PARAM_CHANGE` 成本参数调整 / `MANUAL_LOCK` 人工锁定主供应商 / `DAILY_RECALC` 日终重算 /
`SUPPLIER_SWITCH` 备选切换 / `EXPIRE_REMOVE` 到期剔除。

**写入规则**（设计 000004 注释原文）：同事务「旧版本 `is_current=false, valid_to=:event_time` + 新版本 INSERT」；
**若新值与当前版本的组件价格、`loss_rate`、`channel_rate`、`primary_supplier_id` 全部相同，则跳过不产生新版本**
（防止日终全量重算把表撑爆）。

---

## 1. 核心口径

**完全成本**（在模型币种内计算，**汇率不参与**）：

```
完全成本 = 供应商单价 × (1 + 损耗系数) × (1 + 通道费率) [+ 可配置税项，默认不计]
```

- 损耗系数默认 3%~5%，**可按模型 / 供应商覆盖**
- 通道费率默认 1%~2%
- 可配置税项：进项抵扣默认开、预提所得税默认 0
- 供应商单价：倍率模式 = 官方价 × 倍率；绝对价模式 = 直接填入

**floor（销售侧唯一下限）**：`floor = 完全成本 / (1 − 最低毛利率)`，最低毛利率 = `sys_config.min_gross_margin` = `0.15`。

**四因子评分（选主供应商）**：

| 因子 | 权重 | 公式（归一化 [0,1]） | 缺数据兜底 |
|---|---|---|---|
| 价格 | 0.55 | `price_score = min_cost / supplier_cost`（代表组件完全成本，最低价为 1） | 无报价则不参与 |
| 稳定性 | 0.25 | `min(1, days_effective / 90)`；**起算口径**：该供应商在该 SKU 上**首次生效**的 `quote_sheet.valid_from`，中间断档（有 EFFECTIVE 空窗期）则重新起算 | 新供应商（无历史）= `0.5`（中性，不惩罚 newcomers） |
| 配额充足度 | 0.12 | `supplier_rpm / max_rpm`（`constraints_.rpm` 相对最高者归一）；无 rpm 用 tpm | 两者都缺 = `0.5` |
| 兼容完整度 | 0.08 | `constraints_.compatibility` 非空（如「OpenAI 兼容」）= `1.0`；显式不兼容 = `0` | 未声明（空）= `0.5` |

> 总分 `total = 0.55×price + 0.25×stability + 0.12×quota + 0.08×compatibility`。
> 主供应商 = total 最高者；备选序列 = total 降序。
> **四因子得分（原始值 + 归一化值）与稳定性起算日全部写入 `calc_snapshot`**（含 `formula_version`，可追溯）。
> 稳定性是代理指标：真正的 SLA/可用率需探测数据源（阶段 11 后才有），本阶段用「稳定供货时长」，
> 并在 `calc_snapshot` 记录所用口径。

**不可变版本规则**：改值 = 同事务「新版本 INSERT + 旧版本关闭生效期」；
**值未变则不产生新版本**（比较口径：逐组件 `unit_cost` + `loss_rate` + `channel_rate` + `primary_supplier_id`
全部相同才跳过，逐组件 `decimal.Equal` 数值比较，**不用整体哈希**——decimal 哈希对尾零敏感）；
变更发 `cost_baseline.changed` 事件并 `cache_version + 1`。

---

## 2. 成本基线列表（当前版本）

```
GET /api/internal/cost/baselines?page=1&size=20&keyword=&only_single_point=false
```

- 权限：`M5:V`。**不做归属过滤**（设计 §3.2：成本与比价类放开 ALL）。
- 成本构成字段对**销售 / 供应商**角色由 `field_mask` 剔除（连 key 都不存在）。

**响应 `data.list[]`**

| 字段 | 说明 |
|---|---|
| `sku_id` / `sku_code` | SKU |
| `version` / `valid_from` | 当前版本（**列名 `version`**） |
| `currency` | 模型币种 |
| `primary_supplier_id` / `primary_supplier_name` | 主供应商 |
| `unit_cost` | 完全成本（**代表组件口径**，见下 `unit_cost_basis`） |
| `unit_cost_basis` | `unit_cost` 的口径标注，如 `"input"`（前端展示「input 单价」而非笼统「成本」） |
| `supplier_count` | 有效报价的供应商数 |
| `single_point` | 是否单点依赖（只有 1 家） |
| `floor_price` | 本 SKU 的 floor（= 完全成本 /(1−15%)，同口径代表组件） |

> `unit_cost` 口径（§10-2 已定稿）：**代表组件**的完全成本。优先 `input` 组件（对话/推理/向量模型的
> 主计价维度）；无 `input` 则取该基线第一个组件（按 `component_type` 字母序）。
> 逐组件明细在 §3 history 与 §4 compare 里给全。
> **成本区间（cost_min/cost_max/cost_weighted）不在本接口返回**——属比价视图（§4）实时计算。

## 3. 成本版本历史（支持历史时点）

```
GET /api/internal/cost/baselines/{sku}/history?asOf=2026-08-01T00:00:00%2B08:00
```

- 权限：`M5:V`。`asOf` 为空返回全量时间线；传了返回**该时刻生效的那一个版本**。
- 响应 `data.list[]`：`version` / `valid_from` / `valid_to` / `change_reason` /
  `primary_supplier_name` / `unit_cost` / `calc_snapshot`。

## 4. 多供应商比价 + 成本区间 + 趋势

```
GET /api/internal/cost/baselines/{sku}/compare?days=90
```

- 权限：`M5:V`。
- 响应 `data`：
  - `suppliers[]`：每家供应商的逐组件完全成本、`constraints`、四因子得分、`is_primary`、`is_backup`
    （**SLA 字段本阶段不返回**——库里无探测/可用率数据源，待阶段 11 开放接口后补）
  - `range`：`{ cost_min, cost_max, cost_weighted }`（**实时计算**，不落库；
    `cost_weighted` 按四因子 total 加权，total 之和为 0 时退化为算术平均，不除零）
  - `trend[]`：近 `days` 天成本趋势点（`date` / `unit_cost` / `version`），**按天聚合取每日最新版本，不插值**
  - `market_best`：全市场该组件完全成本最低价

## 5. 议价机会看板 / 单点依赖告警

```
GET /api/internal/cost/opportunities?type=bargain|single_point
```

- 权限：`M5:V`。
- `type=bargain`：主供应商价格显著高于市场最低价的清单（阈值 `quote_anomaly_mkt`）。
- `type=single_point`：只有 1 家有效报价的已上架 SKU。

## 6. 手动锁定主供应商

```
POST /api/internal/cost/baselines/{sku}/lock-primary
```

- 权限：`M5:E`（**采购**持有，设计 §1017 的 M5 权限修正）。
  **服务层按角色 code 加固**（设计 §1017 原意即"一个权限点靠角色区分"）：
  仅 `PROCUREMENT` 可调；`MODEL_OPS` / `RETRO_OP` 虽也持有 `M5:E`（种子历史遗留），
  服务层显式拒绝（见 §11-1）。
- 幂等：**必填** `Idempotency-Key`。
- 请求：`{ "supplier_id": 2, "reason": "该供应商 SLA 更优，锁定一年" }`，`reason` 必填。
- 行为：覆盖算法结论，`change_reason = 'MANUAL_LOCK'`、`locked_manual = true`；触发一次成本重算。
- 响应 `data`：`{ sku_id, version, primary_supplier_id, change_reason, locked: true }`。

## 7. 成本参数（可按模型 / 供应商覆盖）

```
GET /api/internal/cost/params
PUT /api/internal/cost/params
```

- 权限：`M5:V`（GET）/ `M5:E`（PUT）。
  **PUT 服务层按角色 code 加固**：仅 `PRICING_OP` 可调（`MODEL_OPS` / `RETRO_OP` 拒绝，见 §11-1）。
- GET 响应 `data`：
  - `defaults`：`{ loss_rate: "0.0300", channel_rate: "0.0100", tax_inclusive: true, withholding_tax: "0.0000" }`
  - `overrides[]`：`{ scope_type: "MODEL"|"SUPPLIER", scope_id, loss_rate, channel_rate, tax_inclusive, withholding_tax }`
  - **字符串语义**（6d-2 复核批裁决）：所有比率字段透传 DB `numeric(8,4)` 入库**原字符**（含尾零，
    如 `"0.0300"`）——前端绝不能按字符串相等比较（`"0.0300"` ≡ `"0.03"` 数值同但字符串异），
    必须 parseFloat / decimal 比较。
- PUT：`overrides` 全量替换（增量语义易错，用全量）。
  - body 必含 `overrides` 键（缺失 400）；`"overrides": []` 是合法的清空语义；
  - 本批次 `defaults` 只读——body 带非空 `defaults` 直接 400（6d-2-①）；
  - 每条 override 必须**四字段齐**（`loss_rate` / `channel_rate` / `tax_inclusive` / `withholding_tax`），
    缺字段视为显式提交该字段零值（6d-2-⑤ 待产品确认是否允许部分提交）；
  - 费率必须 0~1、小数位 ≤4、纯小数字面量（拒绝 `1e-3`）；
  - scope_type 只允许 `MODEL` / `SUPPLIER`（`GLOBAL` 走 defaults），scope_id 必须存在；
  - 同一 `(scope_type, scope_id)` 在 body 中重复即 400。
- 覆盖优先级：**最具体者优先**（供应商覆盖 > 模型覆盖 > 全局默认）。
- 参数变更 → `change_reason = 'PARAM_CHANGE'` 触发重算（受影响 SKU = GLOBAL 集 + 新旧 override 影响面并集）。

## 8. 汇率档位查询 / 锁定（**本阶段不实现，推迟到阶段 8/9**）

> 完全成本在模型币种内计算、**汇率不参与**（需求 D5）；`fx_rate_lock` 只在
> 「供应商 USD 报价折人民币对账」与「月报汇总」用，属阶段 10 财务结算。
> 表 `fx_rate_lock` 已存在（000004），本阶段不写接口、不写数据。

```
GET  /api/internal/fx/lock?month=2026-09
POST /api/internal/fx/lock
```

- 权限：`M5:E`（**财务**持有；服务层按角色 code 加固：仅 `FINANCE` 可调，见 §11-1）。
  POST 幂等（**必填** `Idempotency-Key`）。
- POST 请求：`{ "month": "2026-09", "tier": "6.8", "reason": "..." }`。
- 月中调整（该月已锁定过）→ 走双人审批（`change_request` + 2 步 `approval_step`）。

## 9. 消费 `COST_RECALC`（本阶段的关键）

阶段 0–5 已经往 `task_job` 里入队 `COST_RECALC`，但**没有消费者**。阶段 6 必须补上 worker：

| 触发 | payload |
|---|---|
| 报价生效 | `{ supplier_id, quote_sheet_id, reason: "QUOTE_EFFECTIVE" }` |
| 官方价变更 | `{ supplier_id, quote_sheet_id, reason: "OFFICIAL_PRICE_CHANGE" }` |
| 特权补录 | `{ supplier_id, quote_sheet_id, reason: "RETRO", effective_time }` |
| 到期剔除 | `{ supplier_id, quote_sheet_id, reason: "EXPIRE_REMOVE" }` |

**带 `effective_time` 的只影响该时点起之后的版本，绝不向更早回灌。**

> **防重与幂等（5d 复核确认）**：
> - 任务认领：`task_job.status` 条件更新 `PENDING→RUNNING`（0 行 = 已被别的消费者拿走，跳过）；
>   失败置 `FAILED` + `last_error` + `retry_count+1`（上限 3，超限置 `DEAD` 不重试并写 alert）。
> - 值未变跳过：重算结果与当前版本全同 → 不产生新版本（重复消费的天然幂等）。
> - 并发唯一：`uk_cost_current(sku_id) WHERE is_current` 部分唯一索引 + `ex_cost_no_overlap`
>   EXCLUDE gist 兜底；同 SKU 两个并发重算，后提交的事务因约束失败回滚 + 记 `last_error`。

---

## 10. 已定稿决策（原「待裁决点」，5d 方案复核后定稿）

1. **三个非价格因子公式**：见 §1 表（价格=最低价为基准归一；稳定性=首次生效日起算 min(1,天数/90)、
   断档重新起算、新供应商兜底 0.5；配额=rpm/tpm 相对最高者归一、缺数据 0.5；
   兼容=compatibility 非空 1.0/显式不兼容 0/未声明 0.5）。
2. **`unit_cost` 口径**：代表组件（优先 `input`，无则按 component_type 字母序第一个），
   响应带 `unit_cost_basis` 标注。逐组件明细在 §3/§4。
3. **成本参数存储**：新表 `cost_param`（`scope_type ∈ GLOBAL/MODEL/SUPPLIER` + `scope_id`，
   `UNIQUE(scope_type, scope_id)`）；解析优先级 **SUPPLIER > MODEL > GLOBAL**（最具体者优先）。
4. **值未变比较口径**：逐组件 `decimal.Equal` 数值比较（`unit_cost` + `loss_rate` + `channel_rate` +
   `primary_supplier_id` 全同才跳过），**不用整体哈希**（decimal 哈希对尾零敏感）。
5. **汇率锁定**：本阶段**不实现**，推到阶段 8/9（完全成本不用汇率，折人民币属财务结算）。
6. **资质冻结联动**：本阶段不做状态机联动，只做「计算时排除」——
   `qual_status='FROZEN'` / `settle_status='FROZEN'` / `status='INACTIVE'` 的供应商不参与评分，
   并在 `calc_snapshot.excluded_suppliers` 记录。自动降权/切换备选推到供应商资质阶段（C10）。
7. **COST_RECALC 并发**：三道防线（任务条件更新认领 + 值未变跳过 + 部分唯一索引/EXCLUDE 兜底）。
8. **趋势聚合粒度**：按天聚合取每日最新版本的 `unit_cost`，**不插值**（真实反映"那天没重算"）。
9. **`calc_snapshot` 字段**：`quote_sheet_id` / `quote_version_no` / `quote_valid_from` /
   `official_price_version_no` / `params`（含 `params_scope`）/ `scores`（逐供应商四因子原始值+归一化）/
   `excluded_suppliers` / `formula_version`。不存完整报价单副本（用 id 关联）。
10. **议价机会阈值**：复用 `quote_anomaly_mkt`（默认 0.30），与 D4 异常检测同一「显著高于市场」语义。

## 11. 补充裁决（5d 方案复核）

1. **M5 写接口的角色加固**：`M5:E` 权限点当前被 6 个角色持有（PRICING_OP / PROCUREMENT / FINANCE 应有；
   MODEL_OPS / RETRO_OP 为种子历史遗留的越权持有）。**服务层按角色 code 收敛**：
   `pkg/perm.CanEditCostParam`（仅 PRICING_OP）/ `CanLockPrimary`（仅 PROCUREMENT）/ `CanLockFx`（仅 FINANCE），
   每个函数单测锁住允许/禁止集合（MODEL_OPS、RETRO_OP 必须为 false）。
   「角色权限过宽」统一登记 CLAUDE.md 遗留项，阶段 F 统一治理（与 RETRO_OP 的 M4:A/M4:P 重叠合并）。
2. **辅助告警不抛错**：主业务落库成功但辅助告警（alert/todo）写失败时，
   返回 200 + `alert_created=false` + zap error 日志，**不打成 500**（5d 复核原则）。
   幂等 FAILED 语义本阶段不动中间件（"业务部分写入 + 500" 概率极低但存在，登记阶段 10 治理）。
