# CLAUDE.md — 模型管理与定价中心（model_bss）

大模型中转平台的**模型数据中心 + 进货/出货两条定价线**。
进货线：厂商官方价 → 供应商报价 → 标准采购成本 → 完全成本
出货线：完全成本 → 定价策略 → 价目表 → 客户报价/合同价

**技术栈**：Go 1.22（Gin + GORM + pgx）｜PostgreSQL 15｜Vue 3 + Element Plus｜uni-app（H5/小程序）｜Redis 7（可选，仅验证码与限次）｜golang-migrate

> **正式代码基线**：后端开发以当前聚合仓库 `bss-ops-system/model-pricing-backend` 为准。该目录已同步后端 integration commit `1e15eb56e1413e7df39f8fbf5d97a9f1ad377a01`；无需再到其他 worktree 获取代码。连接任何环境前必须核验实际配置和数据库目标，不得将测试配置直接用于生产。此条不授权自动切换、迁移或操作生产数据库。

---

## 一、必读文档（按需片段读取，不要一次全读）

| 文件 | 什么时候读 |
| --- | --- |
| `docs/模型管理与定价中心-详细设计文档-v1.1.md` | **主力文档**。§2.4 全量 DDL（唯一权威来源）、§2.5 数据库约束清单、§2.6 状态字典、§8 接口清单、§9 状态机、§12 Redis 边界 |
| `docs/模型管理与定价中心-需求规格说明书-v1.4.md` | 需要理解业务语义、用例、状态机意图时读对应章节 |
| 本文件 | 每次会话自动加载，**下面的红线无条件生效** |

> 两份文档合计约 3400 行。请按任务需要**检索片段**（Grep/Read 指定行号），不要试图一次全部读入上下文。

---

## 二、硬性红线（违反即为 bug，不看文档也要遵守）

1. **金额精度**：一律 `numeric(20,8)`；Go 侧用 `shopspring/decimal`；**禁止 float64 参与任何金额运算**；API 传输用字符串（`"0.00001234"`）。
2. **时间**：一律 `timestamptz` 存 UTC；展示按用户时区；**生效判定统一按 `Asia/Shanghai`**。PostgreSQL **没有** `ON UPDATE CURRENT_TIMESTAMP`，`updated_at` 由 GORM `AutoUpdateTime` 维护。
3. **不可变版本**：官方价、报价单、价目表、客户报价、**成本基线**——改值 = 同事务「新版本 INSERT + 旧版本 `valid_to=:event_time, is_current=false`」。**禁止原地 UPDATE 价格/成本字段**。成本基线若新值与旧值完全一致则跳过不产生新版本。
4. **状态值**：只能用设计文档 §2.6 状态字典里的枚举值，**禁止自创状态字符串**。
5. **幂等**：产生业务新版本或影响资金/合同的写操作，必须过 `idempotency_key` 中间件（`request_id` + `biz_key` + `request_hash`）。见设计文档 §8.0.1。
6. **字段剔除**：`cost` / `margin` / `baseline` 对销售与供应商角色，在 **DTO 序列层物理删除**（响应里不存在该 key），不是前端隐藏。
7. **数据域**：供应商/客户/报价/报价明细查询必须注入 `owner` 行级过滤 Scope；**成本视图与比价类不做归属过滤**（采购可见 ALL，见设计 §3.2 决议）。
8. **事务边界**：连锁生效只放最小同步事务（如官方价变更 T1），成本重算/通知/事件出站一律走 `task_job` / `event_outbox` 异步，且与业务变更同事务入队。
9. **Agent 集成**：Agent（Dify/MCP）**只能写 `staging_price` 与建议类结果**，绝不能直接改 `price_version` / `cost_baseline` / `price_book`。采集结果先入暂存区，多源不一致必须人工确认，**禁止静默取值**。
10. **审计**：价格相关操作 100% 写 `audit_log`（前后值 JSON）；系统/cron/agent 触发的写 `source_type` 与 `source_id`。

---

## 三、数据库约束（设计文档 §2.5，迁移文件必须包含）

以下由数据库强制，不要试图在应用层"检查后写入"：

- 部分唯一索引：`cost_baseline(sku_id) WHERE is_current`、`quote_sheet(supplier_id) WHERE status='EFFECTIVE'`、`quote_sheet(supplier_id) WHERE status='APPROVED_PENDING'`、`price_book(level_code) WHERE status='EFFECTIVE'`、`price_version(sku_id) WHERE is_current`、`legal_subject(uscc) WHERE subject_type='COMPANY'`、`legal_subject(mobile) WHERE subject_type='INDIVIDUAL'`
- 排他约束：`price_version`、`cost_baseline` 的区间不重叠（`EXCLUDE USING gist`，需 `CREATE EXTENSION btree_gist`）
- 组件表唯一：`UNIQUE(父id, component_type)` × `price_component` / `quote_component` / `cost_component` / `price_book_component`
- 幂等：`UNIQUE(biz_type, request_id)`

**报价激活**：新版本生效时，**同一事务内**把该供应商旧的 `EFFECTIVE` 置 `EXPIRED` 并写 `valid_to`，再置本单 `EFFECTIVE`。

---

## 四、目录约定（不要自创结构）

```
cmd/server/          入口 main.go
internal/
  domain/            领域模型（GORM struct，对应 migrations）
  repo/              数据访问（含行级过滤 Scope、字段剔除在 service 层）
  service/           业务逻辑（事务边界在此）
  api/
    middleware/      鉴权 / 权限 / 幂等 / 数据域注入 / 审计
    internal/        内部运营后台接口
    supplier/        供应商门户
    customer/        客户门户（浏览器 + H5 共用）
    open/            开放接口（网关/计费/对账）+ MCP
  worker/            task_job 消费者
  cronjob/           定时任务
  infra/             db / cache / config
migrations/          golang-migrate 版本化迁移（严格按设计文档 §2.4）
web/                 【占位·不要动】前端由同事在独立仓库实现，本项目不写任何前端代码
docs/                需求与设计文档（**只读，不要修改**）
docs/api/            【可写】**接口契约目录**：前端对接的唯一契约源头。
                     实现任何接口前先更新这里；`openapi/swagger.json` 是代码注解生成的产物，不是源头。
```

---

## 五、编码约定

- 配置：Viper + 环境变量；敏感项不入库不入代码。
- 错误：领域错误用 `errors.Is` 可判定的自定义类型；HTTP 层统一转 `{code,message,data,requestId}`。
- 日志：zap，结构化；每个请求带 `request_id`，每个业务操作带 `operator_id`。
- 迁移：只追加，不修改已提交的迁移文件。
- **GORM 行模型读写分离（踩过 500 级事故）**：用于 INSERT/UPDATE 的行模型（如 `skuRow`）
  **不得包含**任何 JOIN 出来的别名列（如 `vendor_name`、`family_name`）——
  GORM 会把它们当真实列写入，报 `column "xxx" does not exist`。
  需要关联展示字段时，另建 `xxxListRow`（内嵌基础行模型 + 别名列），**只用于列表查询扫描**。
- **NOT NULL 字段必须在创建时显式初始化**：数据库 DEFAULT 只在"字段未出现在 INSERT 列"时生效，
  结构体字段为零值时 GORM 仍会显式写 NULL 并触发非空约束错误。
  新增 NOT NULL 列后，务必在所有写入路径初始化该字段。
- 测试：service 层必须有单元测试（尤其是金额计算、状态流转、幂等）；不追求覆盖率数字，**价格与成本相关的计算路径必须有测试**。
- **只做后端**：不实现任何前端代码（Vue / H5 由同事在独立仓库开发），也不要生成 `embed.FS`、模板、静态资源相关代码。
  后端对前端只负责三件事：
  ① 统一响应包 `{code,message,data,requestId}`（已落地 `pkg/response`）；
  ② **CORS 中间件**（前端独立域名/端口，必须开启，允许凭据）；
  ③ **OpenAPI 接口文档**：`swag init` 生成，随代码同步更新——这是给前端的**唯一契约**，
     新增/修改接口必须同步 swag 注解，否则前端拿到的文档就是错的。
- **外部门户角色约定**：SUPPLIER / CUSTOMER 不参与 M1-M12 权限点矩阵，其可见性由 `portal_type` + owner 归属（`owner_type`/`owner_id`）校验保证，`role_permission` 为空是预期行为；如发现其 `role_permission` 被赋值，需复审并清空。

---

## 六、开发顺序（严格按此推进，前一步可运行再进下一步）

0. 项目骨架 + 配置 + 数据库连接 + 健康检查 + Makefile
1. **数据库全量迁移**（按设计文档 §2.4，含全部约束）+ 种子数据（角色、权限点、sys_config）
2. **鉴权 + 权限中间件**（login_session、权限包快照、数据域 Scope、字段剔除）——横切能力，后续所有接口依赖
3. **幂等中间件**（`idempotency_key`）
4. 模型管理 M1（SKU/别名/官方价版本）
5. 供应商与报价 C（提交/导入/审批/激活/到期）
6. **成本引擎 D**（完全成本、四因子评分、成本基线不可变版本）
7. 官方价变更 B（暂存区 → 差异 → 审批 → 连锁生效）
8. 定价与价目表 E（策略/floor/红线/发布/回滚/涨价传导）
9. 客户报价 E（套用/克隆/临时/特价/合同价/刷新）
10. 工作台与审计 F（待办/指标/告警/审计查询）
11. 开放接口 + 事件出站 + MCP Server

> 每一步完成后必须：`go build ./...` 通过、`go vet ./...` 无告警、服务能启动、关键接口 curl 冒烟通过、**git 提交一次**。

---

## 七、AI 协作约定

- **先出计划再动手**：任何超过 3 个文件的改动，先用文字列出方案（改哪些文件、怎么改、风险点），确认后再执行。
- **不要一次性生成整个项目**：按上面第六节的顺序，一次只做一个模块。
- **不确定就持续问**：文档中未定义的细节（如错误码取值、分页默认值）或存在多种会影响结果的解释时，明确列出未知点、可选方案及影响，向用户询问；答案仍不足以确定实现时继续追问，直到关键假设得到确认。不得为了推进而自行发明契约或把未经确认的假设写成事实。纯实现细节且不影响需求结果时，可依据现有代码与惯例作出合理判断并说明。
- **先定义可验证的完成标准**：实现前明确目标行为与相关回归路径；修复缺陷优先复现并补针对性测试，重构确认前后行为一致。完成时只报告实际执行过的验证及结果，未验证的部分明确标注。
- **简洁且精准地修改**：只实现当前任务需要的能力，不增加一次性抽象或未经要求的可配置项；保持既有风格，不顺手重构、改格式或清理无关旧代码。清理由本次改动造成的无用导入、变量和函数；每处改动都应能对应任务目标。
- **项目事实变化时更新进度**：只有阶段状态、实现范围、验证结论或遗留事项实际变化时，才更新本文件末尾的「当前进度」小节；纯问答和无项目事实变化的分析不追加记录。
- **禁止**：修改 `docs/` 下的文档；删除或改写已有迁移文件；为了"跑通"而绕过红线（尤其是把不可变版本改成 UPDATE）。

---

## 八、常见坑（本项目特有）

| 坑 | 正确做法 |
| --- | --- |
| 用 `float64` 算价格 | `decimal.Decimal` |
| 用 `time.Now()` 直接存本地时间 | `time.Now().UTC()`，判定生效时转 `Asia/Shanghai` |
| 成本基线直接 UPDATE | 新版本 INSERT + 旧版本关闭 |
| 忘了关闭旧的 EFFECTIVE 报价 | 激活时同事务关闭 |
| 销售接口返回了 cost 字段 | DTO 层物理删除 |
| 成本/比价查询加了 owner 过滤 | 按 §3.2 决议不加 |
| 幂等只做在报价提交 | 审批、发布、补录、汇率锁定、移交、退役都要 |
| 用 `DATETIME` | `timestamptz` |
| 写 `ON UPDATE CURRENT_TIMESTAMP` | PG 不支持，用 GORM AutoUpdateTime |
| 供应商门户路由挂 `RequirePerm` | **禁止**。`SUPPLIER`/`CUSTOMER` 角色在 000007_seed 里是零内部权限点，挂了必然 403；只会走 AuthN，隔离靠服务层按 supplier_id 硬过滤 |
| 表单校验只跑 `go build/vet/test` | 必须再跑 `golangci-lint run ./...`。其 gofmt formatter **默认带 `-s`**（如 `[]any{}` 要简写成 `{}`），单跑 `gofmt` 查不出来 |
| repo 里直接用 `r.base` | 用 `r.txOf(ctx)`（优先 `db.FromContext`），否则幂等中间件注入的事务会被绕过 |
| 前端复用固定 Idempotency-Key | **禁止**。业务失败会被记 `FAILED`，同 key 重试永远返回 409 幂等冲突；前端必须**每个写请求生成新 key**（提交成功后才丢弃，网络超时重试才安全） |
| **验证用的 `go run ./cmd/server` 用完当场 kill** | 起服务前必先 `Get-NetTCPConnection -LocalPort 8080` 查占用。残留实例会：① 抢消费 task_job，把口径打回旧版（6d-1 的 v4 就是这么来的）；② 占用 8080 导致后续启动失败。**第三次发生了**（PIDs 32912→37480→25340），必须入坑位表 |
| **PowerShell 内联多行函数/复杂脚本输出被吞** | 终端对含 function 定义的多行内联命令可能静默吞掉全部输出（7a E2E 实测两次），但 **HTTP 请求实际已发出并写库**——重跑会撞幂等 409 或产生脏数据。复杂 E2E 一律写成 `tmp/*.ps1` 脚本文件再 `powershell -File` 执行；输出被吞时先查库确认副作用再决定重跑 |
| **GORM 行模型内嵌带 TableName 的行 + 别名列扫描不上** | 列表/详情 JOIN 查询**绝不写** `struct { someRow \`gorm:"embedded"\`; SKUCode string }`——GORM 见 someRow 自带 TableName() 就当关联模型处理，embedded 列全丢零值，只有平铺的别名列能扫上（8b-2 实测 list[0]={id:0, sku_code:"...", 其余全空}）。正确做法=**所有列写在 flat struct 上 + 显式 gorm:"column:..."**，与 model.go skuListRow 同款 |

---

## 九、当前进度

> **⚠️ 登记纪律（所有 agent 必须遵守）**：本文件有 **40k 字符上限**（claude CLI 超过会截断）。
> 完成一个阶段后，本文件只保留：**速览行（3~8 行，含 commit hash + 接口清单 + 关键裁决 +
> 单测/变异条数）+ 遗留编号**（如 `9b-①..⑨`）。详细实现与验证证据以 Git 提交、测试和现有项目文档为准，交接状态统一维护在仓库根目录 `HANDOVER.md`。
> 违反 = 下一个 agent 的上下文被截断，等于任务失败。
>
> **⚠️ 手工改库声明标准（9c 复核教训）**：手工改库**每一次执行都要列入声明**（同一语句重复 N 次
> 就写"N 次"，不能只写一次）。**E2E 重跑时的清理（DELETE / UPDATE 状态回滚）同样属于手工改库**，
> 必须当场写进报告——**不要留给复核者从 audit_log 反推**。
> 判断标准：只要是**绕过了业务接口、直接用 SQL 改数据/schema**，就是手工改库，包括：
> ① fixture（INSERT 测试账号/数据）② 测试准备（UPDATE 状态到可测值）③ 测试清理（DELETE 残留）
> ④ 重跑重置（把状态改回起点）⑤ 临时改配置/参数。
>
> 历史实现与验证细节以 Git 提交和现有项目文档为准；不要继续向本文件追加大段过程日志。

### 9.0 已完成阶段速览（阶段 0~7）

| 阶段 | 内容 | 锚点 |
| --- | --- | --- |
| 0 | 项目骨架 + 统一响应包 | 253e41d / 6ea7d24 |
| 1 | 数据库迁移（7 组 45+3 表，约束全生效） | dba66f0 |
| 2 | 鉴权与权限中间件 + swagger | 491c1c0…5d4ff65 / 118ec76 |
| 3 | 幂等中间件（状态机 + 真实 GORM 仓储 + 卡死防护） | 156031c…6b26e43 |
| 4 | 模型管理 M1（创建/维护/别名/批量/上架/退役影响/退役审批） | 66065a4…7a7208d |
| 5 | 供应商与报价 C（数据底座/提交/历史/审批/激活/批量导入/审批 diff） | 4573c31…3ee3875 等 |
| 6 | worker + 成本基线（Unchanged/locked_manual/CalcSKU 四因子/比价+议价） | 1776582 / a49fcaf / 65a71e9 / d237b2c / 0d9856c / 49ab753 / 0b70271 等 |
| 7 | 官方价变更（采集/暂存/比对 + 确认/审批/生效连锁） | 39eed7f / a4b391a / 079b33a / 4605b7c |
| 7b+ | 复核收尾（倍率静默跟随重算真库锚点） | 2196f3b |

### 9.0.1 全量遗留索引（阶段 F 统一治理）

**机制类（影响实现）**：
- **幂等中间件 `FindByBizKey` 命中后不区分 DONE/PROCESSING 直接 409**；且 `MarkResult` 未挂 SetIdempotencyResult 时 result_json=NULL → 重放 data:null（6d-2/6d-3/8a/8b-1 都踩到）——**阶段 10 治理**
- **预约生效**：5b、7b-①、8b-1-⑥ 三处都不支持 `effective_time > now`（统一等 worker 分钟级 ticker）
- **DecideApproval 非事务**：连锁失败留 APPROVED-without-effects 脏态（7b-②，修复方向=包事务或可重试）
- **报价预约 ticker's LIMIT 1 脏数据**：`ui_activate_quote_scan` 用 `ORDER BY … LIMIT 1` 导致任一张留痕，非瓶颈可后做
- **`representCompJoin` 改 `LEFT JOIN LATERAL`**（6b-4 遗留②）：当前用 LEFT JOIN，结构正确但非最优

**数据一致性类（阶段 F 治理清单）**：
- **compatibility 因子权重 0.08 无数据源**（库里/导入模板/接口都没有，CalcSKU 恒 0）
- **quote_sheet 历史区间 dirty**：seed 19/24/25 的 `valid_to < valid_from`（判定只看锁有效）
- **`audit_log.operator_id` 有两套命名空间**（0=系统任务 / NULL=人工操作 + 后来加的 staff:N 前缀风格）
- **`operatorRoleOf(op)` 只回首个角色**：PLATFORM_ADMIN+MODEL_OPS+PRICING_OP 的账号操作都记成 PLATFORM_ADMIN，与真实审批人角色错位（8b-1 里 audit 落到 PRICES_OP 时就是这样）

**权限类（并入阶段 F「角色权限过宽」专项）**：
- `M5:E` 被 6 角色持有（PRICING_OP/PROCUREMENT/FINANCE/MODEL_OPS/RETRO_OP/SALES），仅 PRICING/PROCUREMENT 应有
- `M4:A`/`M4:P` M4:P 发到了 PROCUREMENT（阶段 5b 曾出现 buyer_a 误批准证据）
- `M7:A` M7:P 两个价格目表权限点可能用处与真实审批错位（8b-1-B2）
- `M2:V` 发给了 SALES（官方价暂存区对销售可见，非成本机密，留 F 复审）
- `M10` 全权限给了 FINANCE（工作台/审计只读即可，F 复审）

**文档类**：
- `approval_step.biz_type` 的 DDL 注释枚举（CHANGE_REQUEST/PRICE_BOOK/...）**与现有约定不符**——约定是 biz_type = change_request.change_type（DEPRECATE/PRICE_UP/PRICE_DOWN/PRICE_BOOK_PUBLISH/PRICE_BOOK_ROLLBACK），注释是旧建议性描述，**阶段 F 更新注释对齐现状，不是改代码对齐注释**
- `change_type` 注释（NEW_MODEL/CAPABILITY/PRICE_DOWN/PRICE_UP/DEPRECATE）**不含 PRICE_BOOK_PUBLISH/PRICE_BOOK_ROLLBACK/SPECIAL_PRICE**——同上，F 更新注释
- **`request_id` 有部分历史行是 NULL**（audit_log，在 7b 之前；后来统一加了）
- **未跟踪文档 07-11 单独提交**（docs/api/07~11 接口契约存在但未纳入 git，阶段 F 治理）
- **`price_version.confidence` 总是 1.00**（7a 录入时写死）——LLM 抽取时代的占位，看以后是否要接

**运营遗留项**：
- **测试 `-p 1` 并发偶发随机 FAIL**（internal/domain/price 等，与业务无关）——F 治理
- **golangci-lint 编不过本机 Go 版本**（重编 go1.26 工具链后已恢复，8b-2 清理了 11 条存量）
- **stage7a/真库有 idempotent request_id 残留**（`7a-e2e-edge-001`/7a-fresh-*等，FAILED 键复用永远 409——不清理，运营信息）
- **`retry_count` 与 GORM default 冲突**（000006 task_job DDL `retry_count = 0` + GORM `default:0` tag 导致 INSERT 显式写 0 被忽略，属正常）

**JAX 后续变更**：
- `customer_quote.quote_type`（000023 追加）——原契约 0.1 提及但 000005 遗漏
- `customer_quote` 无 `level_code` ——从 `customer_profile` 读（契约 0.1 误写在本表上）
- `customer_quote.price_book_version` 是版本号 int，不是 `price_book_id`
- `customer_quote` 无 `cost_baseline_id` ——从 `price_book_item.baseline_version` 间接关联
- `customer_quote_item.floor_price/below_floor` 在 item 上（聚合计算），不在 quote 上
- `ErrInvalidQuoteType` 部分复用为 items 长度不齐/空/CLONE 缺 source_quote_id 兜底参数错
- `idempotency result_json 落盘 NULL` 与 8a/8b-1/8b-2 同根（publish/generate 也复现）
- `LoadSKUCode` 失败被 `_ =` 吞掉（sku_code 仅用于 FloorViolation 显示，失败不阻断业务）
- `DefaultBizKey dedup` 拦截了 applyPartialSku 的二次相同 body（换 key 也拦）——设计行为不算 bug，但 UX 不友好，阶段 10 治理

---

**9b 特有遗留**：
- **PDF 导出不支持**（本期只做 XLSX；`format=pdf` → 400）——PDF 依赖重，P1 补
- `margin_impact` 对 SALES 的 mask 范围待 F 复审（明细 vs 聚合结论）
- `SPECIAL_PRICE_REJECTED` 路径**未实现**（审批驳回后 `special_price_status` 未回落）
- `uk_cq_ver` 是 per-customer 版本号语义——契约需注明（不是全表递增）
- `DecideApproval` 非事务脏态（同 7b-②，本批 E2E 第一轮踩到：Scan bug → cr 已 APPROVED 但业务未生效）

**阶段 F 待排期确认项**：
- **RETRO_OP 的 M4:A/M4:P** 是否收敛至 15（Stage 2 后复审）——已超时未做
- **上架（PUBLISHED→PURCHASABLE）前置校验**（需等报价链路可产生该状态）

---

- [ ] 8 定价与价目表 E
  - [x] 8a 定价策略 + 生成价目表草稿（§1/§2）— commit 56cbde5（速览：4 接口 GET/POST/PUT /pricing/policies + POST /price-books；4 种 price_method[MARGIN/COST_UP/OFFICIAL_ANCHOR/FIXED]；命中 ALL/VENDOR/FAMILY/SKU + priority 最小优先；单测 24 条 + 变异 4 处）
        遗留 8a-①..⑨（rounding_rule 只落值未实现取整 / TIERED 不在 DDL / baseline_version 是版本号 / 优先级不叠加 / MODEL_TYPE 不支持 / floor_rule 无此列 / 契约字段名待更新 / component 只落代表组件 / result_json NULL 同根）
  - [x] 8b-1 发布价目表 + 回滚（§3/§4）— commit 612ae4c（速览：2 接口 publish/rollback；A1 sku_id DROP NOT NULL[000021]；A2 状态机 DRAFT/APPROVING/EFFECTIVE/RETIRED；A3 原地升格 version_no 不变；双人审批 PRICING_OP→FINANCE；单测 17 条 + 变异 3 处）
        遗留 8b-1-①..⑨（change_type 新枚举待更新注释 / M7:A 权限点定位 / floor_violation bool vs 三态 / GRAY 不支持 / SCHEDULED ticker / 驳回回 DRAFT 待确认 / created_by 前缀风格 / result_json NULL 同根）
  - [x] 8b-2 涨价传导决策队列（§5）— commit 02dfd68（速览：3 接口 list/generate/decide；migration 000022 price_upconduction；等比调整 sug=cur×(1+delta)；floor 护栏 NOT_FOLLOW 409；冻结期 7 天；单测 14 条 + 变异 3 处；**新坑位表：GORM embedded 行 + 别名列扫描不上**）
        遗留 8b-2-①..⑧（自动生成待 worker / FOLLOW 不自动发布 / price_suggested 算法 / margin mask / 冻结期可配 / operator_role 用 Roles[0] / result_json NULL 同根 / 11 条存量 lint 已清）
  - [ ] 8b-3（待排期）：价目表详情/历史/对比等只读接口
- [x] 9a 客户列表 + 生成报价 — commit 88bbd28
      3 接口：GET /customers（行级过滤 ALL/DEPT/DEPT_SUB/SELF）+ POST /customers/:id/transfer（双确认 + 历史随迁 + 原归属留档）+ POST /customer-quotes（APPLY 套用生效价目表/CLONE 克隆/TEMP 手工填价，floor 硬校验 → 409 特价审批）。
      migration 000023：customer_quote.quote_type（APPLY/CLONE/TEMP/SPECIAL/CONTRACT）。
      单测 23 条绿；变异验证 3 处全抓回。
      E2E：admin total=2 / sales total=1（SELF 行级过滤）；APPLY 指定 items → 200；
      APPLY 全量带出 → 409（sku40 3.41812858 < floor 3.52941176）；TEMP 缺 valid_to → 400；
      3 个 quote（TEMP/APPLY/TEMP）→ owner=7 origin=2 留档。
      手工改库声明：tmp/9a-fixture-customer.sql + tmp/9a-fixture-sales-b.sql（fixture，不改业务表）。
      剩余子项（详情/编辑/审批/生效）在 9b。
- [x] 9b 特价审批 + 刷新 + 导出 — commit fb14a08
      3 接口：POST /customer-quotes/:id/special-price（M9:E+幂等，DRAFT-only，重复申请 409，margin_impact 预演，change_request[SPECIAL_PRICE] + 2 步 PRICING_OP→FINANCE）+ POST /:id/refresh（M9:E+幂等，unit_price 不动只重算 floor，floor 变了才产新版，unchanged 不产版，状态闸 DRAFT/PENDING）+ GET /:id/export?format=xlsx（M9:V，直返文件流，excelize v2.8.1，TEMP 带水印 + 过期 409，PDF → 400）。
      单测 22 条绿；变异验证 3 处全抓回（Refresh 总产新版 / 水印全类型 / SpecialPriceApproved=EFFECTIVE）。
      E2E：special-price quote id=1 → cr_id=8 step_count=2 → 两步 APPROVED → sps=APPROVED + event_outbox id=50；
      refresh changed（bump sku40 3.0→3.5）→ 新 quote id=4 v4 + 老 id=1 EXPIRED；
      export APPLY 6292B / TEMP 6573B（带水印）/ pdf→400 / 过期→409。
      本批修 3 个真实 bug：① cost_component 列名应以 cost_baseline_id（非 baseline_id）+ currency 从 cost_baseline 取；
      ② GORM Scan(&[]byte) 对 jsonb 解析错 → 改 struct Take；③ floor 比较必须 StringFixed(8) 截断（decimal.Div unbounded 与 numeric(20,8) 直接 Equal 必 false）。
      手工改库声明：① UPDATE customer_quote SET special_price_status=NULL WHERE id=3（第一轮 Scan bug 脏 PENDING 回滚；
      change_request id=7 已 APPROVED 不可回退，作历史保留）；② 临时 bump sku40 baseline 3.0→3.5（id=344）
      事后 DELETE 344 + UPDATE 336 恢复 is_current；③ UPDATE valid_until=now()-1d 测 409 后还原。
      遗留 9b-①..⑨（PDF 不支持 / margin_impact 对 SALES mask 待 F / SPECIAL_PRICE_REJECTED 未实现 /
      uk_cq_ver per-customer 语义待契约注明 / DecideApproval 非事务脏态同 7b-② / idempotency result_json NULL 同根 等）。
- [x] 9c 客户门户 6 接口 — commit 9d96294
      6 接口：GET /api/customer/price-book（本等级 EFFECTIVE，联 model_sku+LATERAL 代表组件，无成本字段）+
      GET /quotes（行级过滤 customer_id=operator_id，报价+合同联合分页）+ POST /quotes/:id/accept（幂等；
      单事务 quote→EFFECTIVE + INSERT customer_price_book + audit CUSTOMER_QUOTE_ACCEPTED）+
      GET /billing（bills=[] 占位）+ GET /notifications（created_at DESC）+ GET /home（聚合）。
      migration 000024 customer_notification。权限裁决 7：不挂模块权限点，AuthN + owner_id 行级过滤。
      字段剔除裁决 6：DTO 物理不含 cost/margin/baseline/floor_price 等 key（不靠运行时 fieldmask.Apply）。
      单测 17 条绿；变异 3 处全抓回（行级过滤 / levelCode 透传 / 加回 floor_price）。
      E2E：login CUSTOMER/1 → price-book 2 items(GLOBAL v1) → quotes total=4 → accept DRAFT 409 →
      UPDATE→APPROVED → accept 200 EFFECTIVE contract_cnt=1 → 同 key 幂等 result 复用 → home pending=2 unread=1 →
      row-filter 他人报价 404 → 未认证 401 → audit id=128。
      手工改库声明：tmp/9c-fixture-customer-account.sql + 9c-fixture-notifications.sql（fixture 保留）；
      UPDATE quote id=2 → APPROVED（E2E 后保留为 EFFECTIVE）；INSERT+DELETE 临时 customer_id=2 报价（已清）；
      **重跑清理 3 次**（DELETE cpb id=1/2/3 + UPDATE quote id=2 → DRAFT，E2E 脚本 3 个自身 bug 中断重跑所致，
      audit 125~127 无对应 cpb——复核补全）。
      遗留 9c-①..⑤（bills 占位待计费系统 / 通知标记已读接口未做 / accept 立即转合同无内部确认 / home 单接口未拆并行 / 通知不自动生成）。
- [ ] 10 工作台与审计 F
  - [x] 10a 待办聚合 + 指标卡 — commit 27353ed
        2 接口：GET /api/internal/workbench/todos（行级过滤+去重+deeplink）+ GET /api/internal/workbench/metrics（按角色裁剪卡片）。
        权限裁决 10：不挂模块权限点，AuthN + 角色自动裁剪；PLATFORM_ADMIN 看全部，其他角色按 assignee_id=operatorID 过滤。
        字段剔除裁决 9：SQL 层不 SELECT + DTO 物理不含 unit_cost/floor_price/margin/baseline。
        破 floor 判定：unit_price < floor（严格小于，price==floor 不算违规）。
        卡片：PROCUREMENT 4 卡 + PRICING_OP 3 卡 + SALES 3 卡（季度成交额占位）+ FINANCE 2 卡（汇率锁定占位）。
        单测 16 条绿；变异 3 处全抓回（行级过滤/破 floor 边界/角色裁剪）。
        E2E：smoke_admin todos OPEN total=6 list_len=5（去重后）；buyer_a PROCUREMENT 4 卡；smoke_finance FINANCE 2 卡；smoke_sales SALES 3 卡；DTO 无成本字段；未认证 401。
        手工改库声明：无（只读接口，无 fixture）。
        遗留 10a-①..⑤（指标卡缓存/季度成交额占位/破 floor 口径/权限点定义/汇率锁定占位）。
  - [x] 10b 告警处理 + 审计日志查询导出 — commit 3d9642c
        4 接口：GET /alerts（M12:V，severity/status/alert_type 筛选）+ POST /alerts（M12:E+幂等，HANDLE/RESOLVE/IGNORE/TO_TICKET 状态机，终态 409，TO_TICKET 建 todo_task）+ GET /audit-logs（M12:V，多维查询 + operator_name 解析）+ GET /audit-logs/export（M12:V，CSV UTF-8 BOM / XLSX，上限 10000 行）。
        权限裁决 1：契约 F:V/F:E → M12:V/M12:E（M12 是工作台/审计模块）。
        数据域裁决 2：审计日志不引入行级过滤（全量可见）。
        operator_name 裁决 3：SYSTEM→系统，CUSTOMER→customer_profile→legal_subject.legal_name，SUPPLIER→supplier_profile→legal_subject.legal_name，其他→internal_staff.name。
        状态机裁决 5：HANDLE→HANDLING，RESOLVE→RESOLVED+resolved_at，IGNORE→IGNORED+resolved_at，TO_TICKET→状态不变+建 todo_task。
        单测 14 条绿（alert 8 + audit 6）；变异 3 处全抓回（终态判断/导出上限/operator_name 解析）。
        E2E：GET /alerts?status=OPEN → 9 行；severity=CRITICAL → 4 行；alert_type=QUOTE_ANOMALY → 2 行；POST HANDLE alert_id=1 → 200 HANDLING；POST RESOLVE alert_id=2 create_todo=true → 200 RESOLVED + todo_id=28；重复处理 → 409；GET /audit-logs?page=1&size=5 → 5 行（operator_name 非空）；action=CUSTOMER_QUOTE_ACCEPTED → 4 行；export csv → 200 22666B；export xlsx → 200 15188B；缺 from → 400；未认证 → 401。
        手工改库声明：无（业务接口变更，非手工 SQL）。
        遗留 10b-①..⑤（契约 F:V/F:E→M12:V/M12:E 文档修正 / operator_id 命名空间冲突治理 / todo_task.biz_type='ALERT' 新枚举 DDL 注释 / todo 指派他人未实现 / 导出 10000 上限 + 保留策略待定）。
  - [x] 10c audit_log operator_id 拆两列 + 回填 — commit 7090353
        迁移 000025：ALTER TABLE audit_log ADD COLUMN internal_operator_id + subject_operator_id；回填按 operator_role 分派（内部角色 → internal_operator_id，外部角色 → subject_operator_id，SYSTEM/operator_id=0 → 两列 NULL）；operator_id 保留（冗余字段，向后兼容）。
        写侧：AuditRepo.Record 按 operator_role 分派两列；读侧：operator_name 解析先查两列（不靠 role 白名单）。
        裁决 5：operator_id 撞号是「治理」不是「消灭」（加冗余列辅助区分，阶段 F 再引入统一主体表）。
        单测 15 条绿（isInternalRole 14 + resolveOperatorName_System 1）；变异 2 处（isInternalRole 改成外部角色 → 测试红；internal_operator_id 读 legal_subject → E2E 覆盖）。
        E2E：迁移前 81 行 → 迁移后 81 行（schema 变更不写数据）；回填 PLATFORM_ADMIN 20 行 internal_operator_id=20，CUSTOMER 4 行 subject_operator_id=4，SYSTEM 14 行两列 NULL；accept quote id=3 → audit_log id=133：operator_id=1, operator_role=CUSTOMER, internal_operator_id=NULL, subject_operator_id=1；GET /audit-logs → operator_name 从两列解析。
        手工改库声明：UPDATE customer_quote SET status='APPROVED' WHERE id=3（E2E 测试准备，1 次）。
        遗留 10c-①..④（qual-expire-scan 无数据源 / operator_id 撞号未完美解决 / todo_task.biz_type='ALERT' DDL 注释 / operator_role 冗余字符串治理）。
  - [ ] 10d 工作台 worker ticker
- [x] 11a 开放接口 7 个接口 — commit dd186a0
      7 接口：POST /api/open/auth/token（client_id+secret 换 1h token，sys_config 配置）+ GET /aliases（since 版本过滤）+ GET /sellable-models（PUBLISHED/PURCHASABLE）+ GET /routing/{sku}（primary=成本基线主供应商，backups=四因子排序）+ GET /price-book?level=（当前生效价目表）+ GET /cost-snapshot?sku=&asOf=（聚合 unit_cost）+ GET /events?since=&limit=（30s 长轮询）。
      裁决 2：开放接口独立鉴权（OpenAuthN），不挂 AuthN/RequirePerm/Idempotency；token 明文仅下发一次，库中只存 sha256 hex。
      裁决 6/7：字段剔除在 SQL 层——不 SELECT cost/margin/floor_price/calc_snapshot/supplier_cost；DTO 物理不含内部字段。
      裁决 8：sellable-models 生命周期过滤 = IN ('PUBLISHED','PURCHASABLE')（ACTIVE 不存在，见遗留 11a-③）。
      单测 17 条绿；变异验证 3 处全抓回（aliases 加 unit_cost / events 30s→3s / token 过期检查）。
      E2E：token 200/401、aliases 3 items/since=1 空、sellable-models 5 items、routing/40 primary=2 backups=1、price-book GLOBAL 2 items、cost-snapshot sku=40 unit_cost=3.00000000、events since=0 31 events、无 token 401、过期 token 401。
      手工改库声明：① INSERT sys_config open_api.client_id/client_secret（fixture）② INSERT cache_version model_alias=1（fixture）③ UPDATE open_api_token expires_at=now()-1h（E2E 过期测试，事后恢复）。
      遗留 11a-①..⑤（aliases 增量不可行 / 长轮询最大连接数未做 / level_tags 来源未定 / token TTL 硬编码 / routing 无评分明细）。
- [x] 11b 事件推送 worker — commit 2d36496
      EventJobRepo（PollDue/Claim/MarkDone/MarkFailed/MarkDead/ResetStaleRunning）+ DeliverClient（HTTP POST + X-Event-Signature）+ EventDeliverJob（启动复位 + 认领 + 推送 + 退避）。
      退避 1min×5^(n-1)，第 5 次 DEAD + alert(EVENT_DELIVERY_FAILED, CRITICAL)。
      webhook 配置每次 tick 从 sys_config 读；webhook_url 为空时 NOOP。
      单测 8 条绿；变异验证 3 处（① 认领条件 E2E 覆盖 ② 退避 TestEventBackoff 红 ③ 签名 TestDeliver_NoSecret 红）。
      E2E：31 条 PENDING 全部 DONE；5 条 FAILED + retry_count=1；id=8 retry_count=4 → DEAD + alert id=10。
      手工改库声明：① INSERT sys_config webhook_url/secret（fixture）② UPDATE event_outbox 5 条 PENDING（E2E 准备）③ UPDATE id=8 retry_count=4（DEAD 测试）④ 清理 sys_config/event_outbox/alert。
      遗留 11b-①..⑤（model.published/deprecated/quote.approved 事件产生未写 / 多 webhook 路由未做 / 签名验证消费者侧未做 / webhook_secret 保存策略待确认 / 历史 31 行已清）。
- [ ] 11c MCP Server

（每完成一项，在此勾选并注明提交信息）
