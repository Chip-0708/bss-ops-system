# 提示词模板（配合 Claude Code CLI 使用）

> 使用方式：每个阶段**新开一个会话**（`/clear` 或重启），只把当前阶段的提示词贴进去。
> 项目约定已在 `CLAUDE.md` 中，Claude Code 会自动加载，提示词里**不需要重复红线**。

---

## 通用原则

1. **一次只做一个阶段**，不要说"把整个项目写出来"。
2. **先要计划再要代码**：提示词里固定带上「先给方案，确认后再写」。
3. **给验收标准**：明确"什么算做完"（能编译、能启动、curl 通、约束在 psql 里查得到）。
4. **小步提交**：提示词里要求每完成一个可运行单元就 `git commit`。
5. **发现文档没写清楚的，先问**，不要自己发明——尤其是错误码、默认值、边界条件。

---

## 阶段 0 · 项目骨架

```text
阅读 CLAUDE.md 和 docs/ 下的详细设计文档第 1 章、第 8.0 节。

目标：搭建 Go 项目骨架，只做地基，不写任何业务逻辑。

要求：
1. 按 CLAUDE.md 第四节的目录结构创建（已存在的目录不要动）。
2. go.mod：module model_bss，Go 1.22，依赖 gin、gorm、gorm.io/driver/postgres、
   golang-migrate、spf13/viper、go.uber.org/zap、shopspring/decimal、robfig/cron、
   golang-jwt（如有需要）、google/uuid。
3. 配置：Viper 读取 config.yaml + 环境变量覆盖（DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/
   DB_NAME/DB_SSLMODE、HTTP_PORT、TZ）。提供 config.example.yaml。
4. internal/infra/db：GORM + pgx 初始化，连接池参数可配（max_open/max_idle/max_lifetime）。
5. cmd/server/main.go：启动 HTTP 服务，注册 GET /healthz 返回 {"status":"ok","db":"up"}，
   db 状态用 Ping 实际检测；优雅关闭（SIGTERM）。
6. 日志：zap 结构化，请求级 request_id（Gin 中间件生成并写入响应头 X-Request-Id）。
7. Makefile：make dev / build / run / migrate-up / migrate-down / test / lint。
8. 初始化 git 仓库并提交一次（commit: "chore: project skeleton"）。

先输出你的方案（文件清单 + 每个文件职责 + 你打算怎么组织配置），确认后再创建文件。
不要写任何业务表结构或业务接口，本阶段只到 /healthz 能返回 ok 为止。
```

---

## 阶段 1 · 数据库迁移（最重要，务必人工验收）— 整合版

```text
阅读 CLAUDE.md 和 docs/模型管理与定价中心-详细设计文档-v1.1.md 的第 2 章
（尤其是 2.4 全量 DDL、2.5 关键约束清单、2.6 状态字典）。

目标：把设计文档 2.4 的全部 DDL 转成 golang-migrate 迁移文件，每个分组一个 up+down，
共 7 个文件。

一、范围与原则（必读，违反任何一条都重做）
1. 不要做任何迁移文件之外的事：不动 go.mod、不动 health、不动 router、不重命名表/字段。
2. DDL 字符级照搬设计文档：列名、类型、nullable、注释、CREATE INDEX 顺序全部保留。
   不"优化"、不"简化"、不调整字段顺序。
3. 迁移文件命名：00000N_<group>.up.sql 与 .down.sql 必须成对，down 严格反向 drop。
4. 禁止在 000001~000006 的 DDL 文件里加 IF NOT EXISTS / ON CONFLICT 之类"防御性"语句；
   这会掩盖真实错误。如果 migrate up 失败，先 make migrate-down 修迁移再 up。
5. 唯一允许 ON CONFLICT (cols) DO NOTHING 的地方是 000007_seed.up.sql 的 INSERT 段。

二、文件分组（7 个文件，按依赖顺序编号）
  000001_account.up.sql      org_unit / internal_staff / account / login_session /
                              role / permission_point / role_permission / role_grant / role_mutex
  000002_subject.up.sql      legal_subject / subject_operator / supplier_profile /
                              customer_profile / credit_txn / deposit_txn
  000003_model.up.sql        vendor / model_family / model_sku / model_alias /
                              price_version / price_component
                              顶部一次性：CREATE EXTENSION btree_gist; CREATE EXTENSION pg_trgm;
  000004_quote_cost.up.sql   quote_sheet / quote_item / quote_component /
                              cost_baseline / cost_component / fx_rate_lock
                              不要重复 CREATE EXTENSION（000003 已建）
  000005_pricing.up.sql      pricing_policy / price_book / price_book_item /
                              price_book_component / customer_quote / customer_quote_item /
                              customer_price_book
  000006_flow_audit.up.sql   sync_job / staging_price / change_request / approval_step /
                              model_application / task_job / event_outbox / idempotency_key /
                              audit_log / alert / todo_task / cache_version / cron_lock / sys_config
  000007_seed.up.sql         permission_point 60 行（按 §3.4 M1-M12 × V/E/A/C/P 矩阵）/
                              role 11 种（PLATFORM_ADMIN / MODEL_OPS / PROCUREMENT / PRICING_OP /
                              SALES / FINANCE / OPS_ADMIN / RETRO_OP / AUDIT_READONLY / SUPPLIER /
                              CUSTOMER）/ role_permission 按 §3.4 对应矩阵 /
                              role_mutex (SALES,PRICING_OP) (SALES,PROCUREMENT) /
                              sys_config 9 项参数
  * down.sql 风格：DROP ... IF EXISTS 可用（防御性）；CASCADE 只在确实有跨表外键时用，
    纯表直接 DROP TABLE IF EXISTS；000007_seed.down.sql 用 DELETE 而非 DROP。

三、13 条约束落点（必须逐一覆盖）
  1 uk_cost_current (sku_id) WHERE is_current                 → 000004 cost_baseline
  2 ex_cost_no_overlap (sku_id =, valid_range &&)             → 000004 cost_baseline
  3 uk_quote_effective (supplier_id) WHERE status='EFFECTIVE' → 000004 quote_sheet
  4 uk_quote_pending (supplier_id) WHERE status='APPROVED_PENDING' → 000004 quote_sheet
  5 uk_pb_effective (level_code) WHERE status='EFFECTIVE'     → 000005 price_book
  6 uk_cq_formal (customer_id) WHERE status='FORMAL'          → 000005 customer_quote
  7 ex_price_no_overlap (sku_id =, eff_range &&)              → 000003 price_version
  8 uk_price_current (sku_id) WHERE is_current                → 000003 price_version
  9 legal_subject 两个部分唯一 WHERE subject_type IN ('COMPANY','INDIVIDUAL') → 000002
 10 uk_alias (alias) 唯一                                      → 000003 model_alias
 11 uk_idem (biz_type, request_id)                              → 000006 idempotency_key
 12 uk_component (父 id, component_type) × 5 张组件表           → 000005 / 000003 等
 13 account 两个全唯一：UNIQUE(portal_type, login_id) + UNIQUE(owner_type, owner_id)
     （与 wx_openid 部分唯一 uk_account_wx WHERE wx_openid IS NOT NULL 并列，
      不要混在同一条说明里，down 时分别处理）

四、P0 字段补全（每张表都要有，别漏）
- request_id varchar(64) NULL（幂等/审计溯源，原设计是 P0 缺口）
- created_by / updated_by bigint NULL
- created_at / updated_at timestamptz NOT NULL DEFAULT now()
- 不要写 DDL 级 ON UPDATE 改写 updated_at（GORM AutoUpdateTime 在应用层处理）

五、role 的 data_scope 不要猜
- 文档明确写了：职能角色 = ALL；非职能（SALES / PROCUREMENT 等）按设计文档 §3.2 规则填，
  成员起步 = SELF。
- 任何没明确写出来的角色或取值，**保持 NULL**，并在最终汇报里列成"待确认"清单。

六、提交节奏
- 每写完 1~2 个文件并 make migrate-up 通过，就 git add + commit 一次（commit 消息注明 group）。
- 全部 7 个文件写完后，000001~000006 各做一次 make migrate-down1 → make migrate-up 循环
  验证回滚干净。
- 最后一次 commit 更新 CLAUDE.md 进度。

七、验收（你要实际执行并贴出 psql 原文）
必过 3 条（任何一条不过 = 修复重跑，不要在数据库里手工补）：
  A. \d+ cost_baseline 必须看到 uk_cost_current 部分唯一索引
  B. 手动插两条 is_current=true 同 SKU cost_baseline，第二条必报 unique_violation
  C. 手动插两条重叠区间的 price_version，第二条必报 exclusion_violation ex_price_no_overlap
补充 3 条（保证覆盖率）：
  D. 同 (biz_type, request_id) 插两条 idempotency_key，第二条必报 unique_violation uk_idem
  E. 插两条 EFFECTIVE 同 level_code 的 price_book，第二条必报 unique_violation uk_pb_effective
  F. 全部 000001~000006 down 后再 up，对比表数量与初始一致：
     SELECT count(*) FROM information_schema.tables
      WHERE table_schema='public' AND table_type='BASE TABLE';

八、收尾
- 更新 CLAUDE.md 末尾「当前进度」勾选阶段 1。
- 报告所有 commit 哈希与上面 6 条 SQL 的 psql 原文输出。
- 列出任何"超出设计文档需要我确认"的点（如某处 DDL 与文档不一致、某约束没在文档里）。
```

---

## 阶段 2 · 鉴权与权限中间件

```text
阅读 CLAUDE.md，以及详细设计文档第 3 章（3.1 登录流程、3.2 数据权域、3.3 角色互斥、
3.4 模块编号对照表）和第 8.1 节接口清单。

目标：实现登录与权限的横切能力，这是后续所有接口的地基。

范围（只做这些，不要扩展）：
1. POST /api/{portal}/auth/login（portal ∈ internal/supplier/customer）
   - account 校验（bcrypt）、主体状态检查（资质冻结/停用/信用冻结 → 401/423）
   - 权限包装配（角色 → 功能权限 + 数据域 + 字段剔除）
   - login_session 落库（token_hash、role_snapshot、12h 过期、client_type）
2. 鉴权中间件：解析 Bearer token → login_session → 操作员上下文注入 gin.Context
   - 未过期/未吊销校验 + 滑动续期
3. 权限中间件：handler 声明所需权限点（如 M4:A），比对会话快照，缺失 403
4. 数据域 Scope：按 3.2 的**分段规则**实现（职能角色 → ALL；否则 min(声明域, 可得域)），
   SELF / DEPT / DEPT_SUB 三种过滤都要实现（不要漏 DEPT）
5. 字段剔除：在 DTO 序列化层按 role.field_mask 物理删除字段
6. POST /auth/logout、GET /auth/profile

要求：
- 数据域过滤封装成 GORM Scope，业务 repo 显式调用，不要靠隐式全局钩子
- 成本/比价类查询**不注入** owner Scope（见 3.2 决议），在代码注释里写明原因
- 提供一个测试用的登录接口返回体示例（curl）

先输出方案（中间件链顺序、上下文结构、Scope 函数签名），确认后再写。
完成后：go build/vet 通过，用 curl 走通「登录 → 带 token 访问一个受保护接口 → 无权限返回 403」。
```

---

## 阶段 3 及以后 · 通用模块开发模板

把下面方括号里的内容替换掉即可复用：

```text
阅读 CLAUDE.md，以及：
- 详细设计文档第 [X] 章、第 [Y] 节（[接口清单/DDL/流程]）
- 需求规格说明书第 [X] 章（[用例/状态机]）

目标：实现 [模块名]，对应接口见设计文档 [8.x] 的表格，状态取值见 2.6 状态字典。

范围（严格限定，不要顺手改别的地方）：
1. [接口 1]
2. [接口 2]
3. [相关的 cron/worker 任务，如有]

约束提醒（CLAUDE.md 已有，此处强调）：
- [本模块特有的红线，如：报价激活必须同事务关闭旧 EFFECTIVE 版本]
- [如：金额用 decimal，不要用 float64]
- [如：低于 floor 必须拦截并引导特价审批]

验收标准：
- go build ./... 与 go vet ./... 通过
- [本模块关键路径] 有单元测试，[关键计算] 至少 3 个用例（正常/边界/异常）
- 服务能启动，[列出需要 curl 冒烟的接口与预期返回要点]
- 关键状态流转在 psql 里能查到正确的行（贴出查询语句与结果）

先输出方案：涉及哪些文件、表结构有没有需要确认的地方、事务边界怎么划、有哪些你不确定
需要我决定的点。**方案确认前不要写任何代码文件。**
完成一个可运行的单元就 git commit 一次，commit message 用中文简述。
```

---

## 代码审查 / 排错模板

```text
当前问题：[现象 + 报错 + 你已尝试过的操作]

请做：
1. 定位到具体文件和行号，说明根因（不要猜，去读代码确认）
2. 给出最小修复方案（不要顺手重构无关代码）
3. 说明这个修复会不会影响 [相关的状态流转 / 已落库的数据 / 其他接口]
4. 如果需要改数据库，明确是新增迁移还是修改现有迁移（已提交的只能新增）

不要：
- 不要为了跑通而删除或放宽数据库约束
- 不要把不可变版本逻辑改成 UPDATE
- 不要用 float64 绕过 decimal 的编译错误
```

---

## 每次会话结束前固定加一句

```text
最后：更新 CLAUDE.md 末尾「当前进度」小节，勾选已完成项并注明本次的 git commit 信息；
如发现文档中有未定义或矛盾的地方，列成清单告诉我，不要自行决定。
```
