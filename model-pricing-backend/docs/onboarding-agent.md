# 给 Coding Agent 的上岗手册（model_bss 项目）

> 如果你是接到任务的 coding agent（Claude Code / Copilot / 其他），**先读完这份手册再动手**。
> 这份手册是项目红线、环境配置、测试纪律、常见坑的浓缩——不看会重新踩坑。

---

## 一、项目红线（违反任何一条都会被复核打回）

1. **金额一律字符串 decimal，禁 float64**
   - 所有价格、成本、费率、倍率字段，Go 里用 `shopspring/decimal`，JSON 里用字符串
   - `numeric(8,4)` 保留尾零：`"0.0300"` ≠ `"0.03"`——**前端别写死字符串比较，也不要用 `==` 比**
   - 输出用 `StringFixed(4)` 或 `StringFixed(8)`，不用 `String()`（会 trim 尾零）

2. **写接口必须挂幂等中间件**
   - 任何 POST/PUT/DELETE 写接口，路由里必须有 `middleware.Idempotency(deps.IdemStore, middleware.DefaultBizKey)`
   - 请求必须带 `Idempotency-Key` header，缺失 → 400

3. **不可变版本**
   - `price_version` / `cost_baseline` / `quote_sheet` / `price_book`：改 = 新版本 INSERT + 旧版本 `is_current=false`
   - 绝不 UPDATE 当前版本的业务字段

4. **手工改库必须声明**
   - 如果你为了测试手工改了数据库（INSERT/UPDATE/DELETE 任何业务表），**必须在报告里显式声明**：改了哪张表、哪几行、用什么 SQL、改完是否还原
   - "零改数"也要写

5. **绝不静默吞字段**
   - 请求里出现你不认识的字段 → 400 或登记为遗留，**不要静默忽略**
   - 静默吞输入在本项目等同 bug

6. **鉴权不能只看主角色**
   - 服务层二次鉴权用 `perm.AnyRoleCan(op.Roles, perm.CanXxx)`，**不要** `perm.CanXxx(op.Roles[0])`
   - `operatorRoleOf(op)` 只用于审计落库，绝不参与权限判定

---

## 二、环境配置（第一次跑起来前必读）

### 配置文件

- `configs/config.yaml` 被 **`.gitignore` 忽略**（含密码），clone 后**不会有这个文件**
- 需要手工创建 `configs/config.yaml`（或找项目 owner 要模板），至少包含：
  ```yaml
  server:
    host: "0.0.0.0"
    port: 8080
  db:
    host: "127.0.0.1"
    port: 5433
    user: "app"
    password: "dev_only"
    dbname: "model_bss"
    sslmode: "disable"
  ```
- `worker.enable` / `docs.enable` 的默认值在 `internal/infra/config/env.go` 里（`viper.SetDefault`），不依赖配置文件

### 数据库

- Docker 容器名：`model_bss_pg`，host port **5433**，库 `model_bss`，user/pass `app`/`dev_only`
- 起服务前必须 `docker start model_bss_pg`（如果没跑）
- 账号密码统一 `Test@1234`：
  - `smoke_admin`（PLATFORM_ADMIN, MODEL_OPS, PRICING_OP）——联调默认，权限最全
  - `smoke_sales`（SALES）——验证字段剔除
  - `buyer_a` / `buyer_b`（PROCUREMENT）——验证采购角色
  - `supplier_a` / `supplier_b`——供应商门户
  - `smoke_bursty`（PRICING_OP）——限流测试专用，**别用**

### 起服务

- `go run ./cmd/server`（在仓库根目录）
- **起服务前必查端口**：`Get-NetTCPConnection -LocalPort 8080`（Windows PowerShell）——**用完当场 kill**
- 残留实例会：① 抢消费 `task_job`，把口径打回旧版；② 占用 8080 导致后续启动失败
- 已经踩过 3 次（PIDs 32912 → 37480 → 25340）

---

## 三、测试纪律（写测试前必读）

1. **`go test` 必须带 `-count=1`**
   - Go 的测试缓存会骗人——改了代码但测试可能跑的是缓存结果
   - 标准命令：`go test -count=1 -p 1 -vet=off ./...`

2. **变异验证必须做**
   - 写完测试后，**故意改坏一处实现**，确认测试真的变红，再还原
   - 变异方法：`cp 备份 → 改坏 → 跑测试 → 还原 → git diff --stat`（确认干净）
   - **多分支同构修复要逐个分支变异**——不要只变异一个分支就以为覆盖了（LI-006 的教训：三个分支都加了 ERROR 处理，只变异了两个，漏了一个）

3. **不要写假测试**
   - 测试要断言**具体字段值**，不要只断言"非零""存在""没报错"
   - 金额类测试必须用**手算数值锚点**（比如 `2.3×1.03×1.01=2.39269000`），不许只测"返回了数字"
   - 参考反例：`TestList_ViewFamilyAndSku` 断言 `Total=2`（应该是 1），掩盖了 family 视图没实现的 bug

4. **`0xc0000409` 是工具链崩溃，不是代码问题**
   - Windows 上 Go 工具链随机崩溃，重试即可
   - `go build` / `go test` 报这个错 → 重跑

---

## 四、常见坑（踩过的，别再踩）

1. **残留进程抢任务**
   - `go run ./cmd/server` 起的验证服务，**用完当场 kill**
   - 残留的 worker 会抢消费 `task_job`，把口径打回旧版本，且极难察觉

2. **配置被 gitignore**
   - `configs/config.yaml` 不在 git 里，clone 后不会有
   - 服务启动需要这个文件（否则 `config.Load()` 报错）

3. **PowerShell 输出不回显**
   - WorkBuddy 的 PowerShell 工具经常不回显输出——写文件再 `Read`
   - URL 里带 `%` 会被安全策略拦——改用 `curl` 或变量拼接

4. **bash coreutils 缺失**
   - WorkBuddy 的 bash 常缺 `grep` / `head` / `tail` / `tr` / `ls` / `rm`
   - 可以用 `/c/Windows/System32/curl.exe` 和 node 绝对路径（`C:/Users/wrf/.workbuddy/binaries/node/versions/22.22.2-3/node.exe`）

5. **金额尾零**
   - `decimal.String()` 会 trim 尾零：`"0.0300"` → `"0.03"`
   - 要保留尾零用 `StringFixed(4)` 或 `StringFixed(8)`

6. **幂等中间件的 `FindByBizKey` bug（已知，待修）**
   - 同 body + 同 operator + 同 path 的重复调用会 409（即使 Idempotency-Key 不同）
   - 正确行为应该是 DONE → replay，但当前实现是 409
   - 阶段 10/阶段 F 治理

7. **`golangci-lint` 本机跑不动**
   - 本机装的版本与 `go.mod` 的 Go 版本不匹配
   - 本地靠 `gofmt -l` + `go vet ./...` 兜底，CI 上仍要求 0 issues

---

## 五、代码模式（写代码前先看现有实现）

### Store 接口模式

- 领域层定义**窄接口**（`Store` / `ReadStore` / `ParamStore`），repo 层实现
- repo 层用 `txOf(ctx)` 获取事务（`db.FromContext` 或 `r.db`）
- 参考：`internal/domain/cost/service.go`（Store）、`internal/repo/cost_baseline.go`（实现）

### Handler 模式

- handler 从 `middleware.OperatorFrom(c)` 取操作员
- 服务层二次鉴权：`perm.AnyRoleCan(op.Roles, perm.CanXxx)`
- 审计字段：`operatorRoleOf(op)`（主角色，只用于落库）
- 参考：`internal/api/cost_lock.go`（6d-3 手动锁定的 handler）

### 字段剔除

- `role.field_mask` jsonb `{"hide":[...]}`，`pkg/fieldmask.Apply` 物理删键
- 在 handler 里调用（`op.FieldMask`），参考：`internal/api/cost.go`

### 同事务写入

- 业务变更 + `task_job` / `event_outbox` / `audit_log` 必须在**同一个事务**里
- 参考：`internal/repo/cost_param.go` 的 `ReplaceOverridesTx`

---

## 六、交付要求（做完必须给）

1. **两个 commit**（或按任务要求）：
   - 功能批：`feat(xxx): 描述`
   - 文档批：`docs(CLAUDE): 进度登记 + 遗留 + 手工改库声明`
2. **报告必须包含**：
   - commit hash
   - **手工改库声明**（如无写"无"）
   - E2E 真值（具体字段值，不许只给 code=0）
   - 变异验证实测 logs（哪些变异被抓回，哪些测试红）
3. **门禁**：
   - `gofmt -w .`
   - `go vet ./...`
   - `go test -count=1 -p 1 -vet=off ./...`
   - `go build ./...`

---

## 七、上手一个任务的标准流程

1. **读任务提示词**（自包含，含契约、裁决、实现路径、测试要求）
2. **读相关代码**（提示词里列的文件，不要凭印象写）
3. **查真库**（提示词里如果有"真库当前状态"，先核对——库可能变了）
4. **写代码 + 测试**
5. **跑门禁**（gofmt / vet / test -count=1 / build）
6. **变异验证**（至少 2 处，实测变红再还原）
7. **E2E 验收**（起服务，真库验证，**用完当场 kill**）
8. **写报告**（含手工改库声明、E2E 真值、变异 logs）
9. **commit**（两个：功能 + 文档）

---

## 八、项目文档索引

- `CLAUDE.md`——项目进度、遗留项、坑位表、关键裁决（**单一事实来源**）
- `docs/api/`——各模块接口契约（04-models / 05-quotes / 06-cost / 07-supplier-and-price-change / 08-pricing / ...）
- `docs/swagger-ui-guide.md`——Swagger UI 联调指南
- `PROMPTS.md`——各阶段的提示词模板（参考）

---

**最后一条**：如果提示词里的裁决你有不同看法，**不要自作主张改**——先问任务发起人（复核会按提示词的裁决来验）。
