# BSS 模型管理与定价系统交接说明

本文以 `bss-ops-system` 当前代码和已完成验证为准。前后端现已统一放在本仓库；测试服务器是否已经部署某项功能，需与“代码已实现”分开判断。

## 1. 项目简介

BSS 模型管理与定价系统用于维护 AI 模型和 SKU，并串联供应商报价、官方价格、成本计算、定价策略、客户报价、价目表和 Supplier Profile 等业务。

系统包含三个入口：

- **Internal Portal**：供内部运营、采购、财务和审批人员使用。
- **Supplier Portal**：供供应商提交报价、查看历史以及使用供应商自助能力。
- **Customer Portal**：供客户查看价目表、报价、账单和通知。

Vue 前端根据门户、路由和权限组合页面；Go 后端提供统一 `/api` 接口并访问 PostgreSQL。MSW 只用于前端开发和未完成真实接口的场景，不能作为真实联调或部署完成的证据。

## 2. 项目结构

```text
bss-ops-system/
├── model-pricing-web/       # Vue 前端，包含三个 Portal
└── model-pricing-backend/   # Go 后端、migration 和 OpenAPI
```

前端技术栈为 Vue 3、TypeScript、Pinia、Axios、Element Plus、MSW 和 Vite。接手时主要关注：

- `src/app`、`src/router`、`src/layouts`：启动、路由和页面框架。
- `src/modules`：已按业务收口的模块。
- `src/views`：尚未迁移但仍正常使用的业务页面。
- `src/api`：业务 API、DTO 及 HTTP 基础能力。
- `src/mocks`：MSW handlers 和模拟数据。
- `tests`：单元、契约和场景测试。

后端技术栈为 Go、Gin、GORM 和 PostgreSQL 15。主要目录：

- `cmd/server`：服务入口。
- `internal/api`：路由、Handler 和中间件。
- `internal/domain`：各业务模块的服务与规则。
- `internal/repo`：PostgreSQL 数据访问。
- `internal/worker`：后台任务。
- `internal/infra`：配置和基础设施。
- `migrations`：数据库 migration 和稳定 fixture。
- `openapi`：Swagger/OpenAPI 文件。

## 3. 核心业务链路

### 模型 / SKU

模型、系列和 SKU 是供应商报价、官方价格、成本、定价策略及客户报价的基础数据。内部端支持模型维护、别名、验证、发布、退役和退役影响查看。

### 供应商报价

```text
供应商填写或导入报价
→ 前端及后端预检
→ 提交报价
→ 内部审批
→ 待生效 / 生效
```

报价支持手工填写和 CSV 导入，并包含倍率/绝对价格、有效期、币种、约束及幂等校验。后续还有到期扫描、异常检查、追溯生效和剔除确认等生命周期处理。

### 官方价格

当前不是自动访问厂商官网或厂商 API。实际链路为：

```text
人工创建采集批次
→ 人工录入 staging price
→ 确认暂存价格
→ 生成变更单
→ 审批
→ 生成正式 price_version
```

正式价格变化可触发成本重算、事件记录及相关倍率报价跟随。

### 客户报价 / 价目表

定价策略可用于生成价目表草稿，价目表经过发布或审批后供客户报价使用。客户报价支持 APPLY、CLONE、TEMP、特价、刷新、导出和客户接受，并包含成本底价、幂等和状态校验。部分高级流程仍需在最新测试服务器版本上继续验收。

### Supplier Profile

内部端已经实现：

```http
GET /api/internal/suppliers
GET /api/internal/suppliers/:id
```

接口使用 `M3:V` 权限，并复用 `SELF / DEPT / DEPT_SUB / ALL` 数据域。详情越域返回 403，不存在返回 404。有效报价统计会检查状态和有效时间，并按 SKU 去重。供应商商务字段仅财务角色或档案归属采购可见，其他账号的响应会物理剔除这些字段。

内部端 Supplier Profile 已实现，不代表 Supplier Portal 自助档案维护、资质上传等能力全部完成。

## 4. 当前功能状态

| 模块 | 状态 | 说明 |
|---|---|---|
| 模型/SKU | 已实现并验证 | 目录、查询、维护及生命周期已有代码和自动化覆盖；核心查询做过真实联调 |
| 成本基线 | 已部署并验收 | 基线查询等核心链路已有测试服务器验证记录 |
| 供应商报价 | 已部署并验收 | CSV 预检、提交和审批主链路已有真实环境验证；最新代码仍应回归 |
| 官方价格 | 已部署并验收 | 人工 staging、确认和审批主链路已走通；不包含自动厂商采集 |
| 定价策略 | 已实现但待部署/真实环境验收 | 代码和契约已接入，完整真实环境覆盖不足 |
| 价目表 | 已实现但待部署/真实环境验收 | 生成、发布及审批已有实现；服务器版本和完整审批回显需复核 |
| 客户报价 | 已实现并验证 | APPLY、CLONE、TEMP、底价冲突、幂等和移交已有验证；最新 safeguards 待部署回归 |
| Customer Portal | 已实现但待部署/真实环境验收 | 需要真实客户账号和持久化场景继续验收 |
| Supplier Profile（内部端） | 已实现但待部署/真实环境验收 | API/domain/repo/test 已完成代码级验证；最新版本尚未部署测试服务器，v28 尚未在该服务器执行 |
| Supplier Portal 自助档案/资质 | 未完成 | 前端和 Mock 已有部分能力，真实后端、附件上传及完整验收仍不足 |
| 工作台、告警、审计 | 已实现但待部署/真实环境验收 | 后端与前端代码已存在，真实环境覆盖仍需补齐 |
| 开放 API/MCP | 未完成 | 部分 Open API 基础代码存在，不能视为完整对外能力 |

## 5. 本地启动

### PostgreSQL

```bash
cd model-pricing-backend
make pg-start
make migrate-up
make migrate-version
```

默认使用 PostgreSQL 15，本地主机端口为 `5433`。执行 migration 前先确认目标数据库和当前版本；不要对共享数据库使用 `migrate-drop` 或随意 `force`。

### Go backend

```bash
cd model-pricing-backend
make dev
```

也可以运行：

```bash
go run ./cmd/server
```

本地默认地址为 `127.0.0.1:8080`。启动后检查：

```bash
curl http://127.0.0.1:8080/healthz
```

`/healthz` 会实际检查数据库连接。Swagger 默认入口为 `/swagger/index.html`。

### Vue frontend

```bash
cd model-pricing-web
npm install
npm run dev
```

默认地址为 `127.0.0.1:5173`。

经同源代理连接真实后端时使用：

```env
VITE_MOCK_ENABLED=false
VITE_API_BASE_URL=/api
```

本机没有同源代理时，可按本地环境将 `VITE_API_BASE_URL` 指向 `http://127.0.0.1:8080/api`。业务联调和正式构建必须关闭 MSW。

## 6. 测试服务器

测试服务器实际链路：

```text
浏览器 → Nginx :8081
              ├─ 前端静态文件
              └─ /api/ → 127.0.0.1:18081

Go backend → 127.0.0.1:18081
```

约定路径：

```text
/app/model_bss
/app/model_bss-web
/app/model_bss-web/releases
/app/model_bss-web/current
```

- 后端由 systemd 管理，操作前使用服务器实际 service 名称核对 unit、启动参数和配置路径。
- 前端发布到独立的 `releases/<release>` 目录，检查完成后切换 `current` 软链接。
- Nginx 提供前端静态文件，并将 `/api/` 反向代理到 `127.0.0.1:18081`。
- 不要把本地开发端口 `8080` 用作测试服务器后端端口。

部署前至少检查：

```bash
systemctl status <实际服务名>
systemctl cat <实际服务名>
nginx -T
readlink -f /app/model_bss-web/current
curl http://127.0.0.1:18081/healthz
```

仓库不保存服务器密码、Token、Secret 或真实生产配置，示例配置不得直接覆盖服务器配置。

## 7. 数据库与 migration

当前源码 migration 最高为：

```text
000028_supplier_settlement_currency
```

最近三个 migration：

- `000026_open_api_token`：Open API token 相关结构。
- `000027_admin_seed`：管理员基础 seed。
- `000028_supplier_settlement_currency`：为 `supplier_profile` 增加 `settlement_currency`，当前限定为 `CNY / USD`。

**源码已经包含 v28，但当前测试服务器尚未执行 v28。部署新后端前必须先查询 `schema_migrations`，确认当前 version 和 `dirty` 状态，再制定升级及回退步骤。**

数据使用上应区分：

- **fixture/seed**：用于稳定的基础测试数据和开发环境初始化，应可重复构建。
- **业务测试数据**：报价、审批、价目表、客户接受等测试运行中产生的数据，不应随意固化为基础 migration。

不要在未知数据库版本上重复执行迁移，也不要用手工改 version 代替迁移核对。

## 8. 前端 legacy 说明

以下文件属于当前稳定运行的 legacy Mock/session 兼容体系：

- `src/mock.ts`
- `src/operations.ts`
- `src/session.ts`
- `src/types.ts`

它们仍被 MSW handlers、测试、fixture 或 Pinia session 兼容逻辑使用，不应在交接后立即强行删除。`src/style.css` 仍通过正式样式入口加载，不是废弃文件。

本轮已经完成：

- 删除无调用方的旧 API 入口。
- Workbench API 迁入业务模块。
- `Draft`、`BatchResult` 重复类型收口。
- 根级模型和官方价格页面迁入业务模块。

当前 `src/api` 与 `src/modules` 是渐进式结构，不建议仅为目录统一继续大规模重构。

## 9. 测试和最小验收

前端：

```bash
npm run typecheck
npm test
npm run build
```

当前记录：类型检查通过，自动化测试 `74/74` 通过，关闭 MSW 且使用同源 `/api` 的正式构建通过。构建存在非阻塞的大 chunk 提示。

后端：

```bash
go test ./... -count=1
go vet ./...
```

当前两项均通过。若 Windows 的全局 Go 临时目录无权限，可以只在当前 shell 将 `GOTMPDIR`、`GOCACHE` 指向可写临时目录，不要为此修改业务代码。

最小 smoke test：

1. `/healthz` 返回成功且数据库可连接。
2. 使用真实账号登录并核对角色、菜单和权限。
3. 查询模型/SKU。
4. 查询成本基线。
5. 提交供应商报价并完成内部审批。
6. 人工录入官方 staging price，确认并完成审批。
7. 创建客户报价，验证底价冲突、幂等和客户接受。
8. 查询 Supplier Profile 列表及详情，分别验证数据域、403/404 和商务字段屏蔽。

## 10. 已知遗留和未完成项

- Supplier Profile 最新代码尚未部署到测试服务器。
- v28 尚未在当前测试服务器执行。
- 内部端 Supplier Profile 完成不代表 Supplier Portal 自助档案能力全部完成。
- 供应商资质附件的真实上传和 `attachment_id` 流程仍需确认或完善。
- 官方价格仍是人工采集，没有自动访问厂商官网或 API。
- 部分模块仍依赖 MSW 或 provisional contract，不能以 Mock 测试代替真实验收。
- 部分分页、角色数据域和真实账号场景仍需在测试服务器验收。
- `mock.ts / operations.ts / session.ts / types.ts` 等 legacy 体系暂时保留，不是当前阻塞项。

## 11. Git 和交接基线

最终交接仓库：

```text
D:\Codex\workspaces\实习\projects\bss-ops-system
```

当前分支与 HEAD：

```text
branch: feature/model-pricing
HEAD:   95cdf4606bca50f22cd01665ce5ec5c7dea8974d
```

最近重要提交：

```text
95cdf46 refactor(web): reuse shared batch result type
8bd98a9 refactor(web): reuse shared model draft type
8dde83b refactor(web): move workbench api into business module
b6b9613 chore: remove unused legacy api entries
489d1d4 refactor(web): move legacy root views into business modules
75a93c5 feat: integrate model pricing frontend and backend
```

当前工作区中的后端目录已完整同步自：

```text
1e15eb56e1413e7df39f8fbf5d97a9f1ad377a01
feat: add supplier profiles and quote acceptance safeguards
```

同步内容已经通过逐文件 Git tree 校验，但尚未提交到聚合仓库。因此当前 HEAD 本身仍不包含这次后端同步；交接前应将后端同步和本文件作为清晰、可审查的提交处理。

当前本地分支相对 `origin/feature/model-pricing` 领先 4 个既有提交，尚未全部推送。当前未提交修改集中在同步后的 `model-pricing-backend` 和本交接文档，`model-pricing-web` 没有意外修改。

接手人不需要再到其他 integration worktree 获取后端代码；完成提交后的本聚合仓库应作为唯一交接基线。

## 12. 接手建议

1. 拉取最终交接分支并确认包含最新后端同步提交。
2. 阅读本 `HANDOVER.md`，确认本地 HEAD 与交接记录一致。
3. 启动 PostgreSQL。
4. 查询 migration version 和 `dirty` 状态，再执行缺失 migration。
5. 启动 Go 后端。
6. 检查 `/healthz` 和 Swagger。
7. 关闭 MSW 后启动前端，确认请求进入真实 `/api`。
8. 按模型、成本、供应商报价、官方价格、客户报价、Supplier Profile 的顺序执行核心 smoke test。
9. 登录测试服务器核对 systemd、Nginx、当前二进制、前端 `current` 链接及数据库 migration。
10. 部署 Supplier Profile 最新版本前先将测试数据库安全升级到 v28，再部署后端和前端并完成真实账号验收。
