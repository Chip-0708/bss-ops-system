# 开放接口 + 事件出站 + MCP Server（阶段 11）

> **状态：草案。阶段 11 未开始，接口全部未实现。**
> 依据：设计 §8.11 / §8.12 / §8.13。通用约定见 `README.md`。

> **为什么必须有**：本系统是主数据中心。
> **别名映射必须下发网关**，否则客户用 `gpt-5` 调用时网关解析不到具体 SKU，计费口径会错。

---

## 1. 鉴权

- `/api/open/**` 用独立的 `client_id + client_secret`（存 `sys_config` 或环境变量），
  换取短期 token；**只读为主**。
- 与三门户的登录态完全隔离。

## 2. 接口清单

| 方法 | 路径 | 消费者 | 说明 |
|---|---|---|---|
| GET | `/api/open/aliases?since=` | 网关 | 别名 → SKU 映射（全量/增量），**带版本号** |
| GET | `/api/open/sellable-models` | 网关 / 官网 | 可售清单（状态、币种、分级标签） |
| GET | `/api/open/routing/{sku}` | 网关 | **主备供应商序列 + 权重建议**（故障转移用） |
| GET | `/api/open/price-book?level=` | 计费 / 官网 | 生效价目表（按等级，含组件价） |
| GET | `/api/open/cost-snapshot?sku=&asOf=` | 对账 | 成本快照（历史时点可查） |
| GET | `/api/open/events?since=&type=` | 通知 / 计费 | 事件拉取（长轮询） |

### 2.1 `GET /api/open/aliases`

```json
{
  "code": 0, "message": "ok",
  "data": {
    "version": 42,
    "items": [ { "alias": "gpt-5", "sku_id": 40, "sku_code": "gpt-5-2026-04-11" } ]
  }
}
```

- `since` 传上次拿到的 `version`，返回增量；不传返回全量。
- **网关必须按 `version` 做本地缓存**，不要用时间戳。

### 2.2 `GET /api/open/routing/{sku}`

```json
{ "code": 0, "data": {
  "sku_id": 40,
  "primary":  { "supplier_id": 1, "weight": 100 },
  "backups":  [ { "supplier_id": 2, "weight": 0 } ],
  "currency": "USD"
}}
```

- 主备序列来自成本引擎的四因子评分 + 手动锁定结果（见 `06-cost.md`）。

### 2.3 `GET /api/open/events?since=&type=`

- **长轮询**：无新事件时挂起最长 30s，超时返回空列表（不是错误）。
- 响应 `data`：`{ "last_id": 123, "events": [ { id, event_type, payload, created_at } ] }`
- 消费者用返回的 `last_id` 作为下次的 `since`。

## 3. 事件清单

事件写 `event_outbox`，与业务变更**同事务**提交，由 worker 推送；消费者也可主动拉取。

| 事件 | 触发时机 | 消费者 |
|---|---|---|
| `model.published` / `model.deprecated` | 上架 / 退役执行 | 网关路由启停、通知系统 |
| `price.effective` | 价目表生效（含回滚版本） | 计费系统、网关缓存失效重载 |
| `cost_baseline.changed` | 成本重算（报价生效 / 官方价变更 / 补录） | 网关毛利校验、指标看板 |
| `quote.approved` / `quote.effective` | 审批通过 / 到达生效时间激活 | 采购待办、成本重算 |
| `official_price.changed` | 新官方价版本生效 | 报价服务（倍率跟随 + 静默版本） |
| `quote.expired` | 报价到期剔除 | 采购待办、成本重算 |

> **阶段 0–5 已经在写的**：`quote.effective` / `quote.expired`。
> 其余随阶段 6–10 补齐。
> ⚠️ 目前 `event_outbox` 只入不出——**没有 worker 消费**，事件会一直堆着。

## 4. 幂等与重试

- worker 投递失败**指数退避重试 5 次**，之后标记 `DEAD` 并写 `alert`。
- 消费者侧必须按 `event.id` 幂等（同一事件可能投递多次）。

---

## 5. MCP Server 工具清单（Agent 集成）

> 与既有 Dify + MCP 网关对齐：本系统以 MCP Server 形式暴露能力，供工作流 / chatflow 调用。

| 工具 | 类型 | 说明 | 需人工确认 |
|---|---|---|---|
| `search_model` | 只读 | 按别名/厂商/能力检索 SKU（**受字段剔除约束**） | 否 |
| `get_model_price` | 只读 | 某 SKU 当前官方价版本 | 否 |
| `get_model_cost` | 只读 | 某 SKU 当前成本基线（**受字段剔除约束**） | 否 |
| `compare_suppliers` | 只读 | 多供应商比价 + 四因子得分 | 否 |
| `suggest_price` | **建议** | 基于策略与成本给出建议售价（**只返回建议，不落库**） | 是 |
| `write_staging_price` | **写** | 写入 `staging_price` 暂存区 | 是 |

> **红线（设计 §1462）**：Agent **只能写 `staging_price` 与建议类结果**，
> 绝不能直接改 `price_version` / `cost_baseline` / `price_book`。
> 所有 Agent 写入记 `audit_log.source_type = 'AGENT'`，可审计追溯。

---

## 6. 待裁决点

1. `client_secret` 存 `sys_config` 还是环境变量（安全 vs 可运维）。
2. 长轮询的超时与最大连接数（会占用连接，需限流）。
3. `/api/open/cost-snapshot` 是否要对下游隐藏成本构成、只给聚合值。
4. MCP 工具的字段剔除是否与 HTTP 接口共用同一套 `field_mask`。
