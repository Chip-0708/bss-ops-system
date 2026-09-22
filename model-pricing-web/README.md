# 模型管理与定价中心 · 前端工程

## 项目简介

模型管理与定价中心面向模型 SKU、官方价格、供应商报价、成本、定价和客户报价等业务，提供三个相互隔离的 PC Portal：

- **内部运营 Portal**：模型、供应商报价、成本、定价、客户、财务、权限、告警与审计。
- **供应商 Portal**：可报价 SKU、单笔报价、CSV 批量导入、报价历史、模型申请，以及仍处于暂定契约阶段的供应商档案类页面。
- **客户 Portal**：价目表、报价、合同、账单与通知的前端页面；当前业务接口仍以 Mock/PROVISIONAL 为主。

项目已经从“纯 Mock 原型”进入**真实 Go Backend 联调阶段**。目前真实联调主线是账号登录、供应商报价与 CSV 导入、内部报价审批查询，以及成本基线列表；模型管理已接入部分真实接口。其余页面即使已经有 View、TypeScript DTO 或 API Module，也不等于 Go Backend 已实现，具体边界见[当前实现状态](#当前实现状态)。

## 技术栈

- Vue 3、Vue Router
- TypeScript、Vite
- Element Plus
- Axios
- Pinia
- MSW（本地开发、自动化测试及未接通功能的辅助能力）
- Decimal.js（金额与倍率计算，避免使用 JavaScript 浮点数处理业务金额）
- Node.js Test Runner、esbuild、MSW Node Server（单元与 HTTP 场景测试）
- Playwright（独立浏览器验收脚本，需由运行环境提供）

## 运行架构

真实联调与业务环境的请求链路是：

```text
View → API Module → Axios → Go Backend → Database
```

`Database` 属于后端侧能力。前端仓库只能通过接口响应和联调记录验证结果，不直接控制数据库事务、权限、状态迁移或后台任务。

启用 Mock 时的开发测试链路是：

```text
View → API Module → Axios → MSW
```

两条链路复用同一套 View 和 API Module。MSW 仅在 `DEV` 且 `VITE_MOCK_ENABLED=true` 时启动，未匹配请求会放行；生产构建若尝试启用 Mock 会直接报错。当前 `.env.development` 为方便本地界面开发而关闭认证并启用 MSW，但这不是项目的业务运行方式，也不能作为真实后端完成度证据。

## 当前实现状态

状态依据当前前端源码、Go 路由与实现、`ISSUES.md`、`VALIDATION.md`、现有人工联调记录及 2026-09-18 工作区差异综合判断。

| 业务范围 | 当前状态 | 证据与边界 |
| --- | --- | --- |
| 登录与会话 | **已真实联调（内部、供应商）/ 客户待真实账号验收** | 三 Portal 登录、Bearer 请求、204 退出和 401 清会话均已接入；Go 已注册三 Portal 登录/退出。内部与供应商账号做过真实调用，客户仅有自动化覆盖。后端尚无安全会话恢复接口，刷新后需重新登录。 |
| 模型管理 | **部分真实联调，部分待确认** | Go 已实现列表、创建/维护、别名、批量、上架、退役申请及审批路由；真实模型列表已读取。前端仍调用尚未在当前 Go 路由注册的 options、详情和人工验证接口，系列视图结构也未完全对齐，不能把整页标为已完成。 |
| 供应商报价 | **已真实联调（当前主线）** | 可报价 SKU、报价提交、历史、详情、内部待批/差异、通过/驳回，以及 CSV 模板、预检、确认导入均有当前 Go 实现；供应商提交与导入已完成人工联调。 |
| CSV 厂商/系列范围（LI-013） | **已真实联调** | `/supplier/skus` 已返回 `vendor_id/family_id`，页面提供厂商与联动系列选择；模板范围和混合 CSV 预检范围均已实测，用户已确认真实页面结果。 |
| 成本管理 | **成本列表已真实联调；其余局部待接入** | 真实模式使用当前成本列表 DTO，真实页面已显示后端数据。Go 还实现历史、参数、比价、议价机会和主供应商锁定等接口，但前端现有页面/API 契约尚未全部迁移；Mock 模式仍保留旧成本演示页。 |
| 官方价格 | **已按 Go 契约接线，待真实验收** | 采集、暂存、变更提交及审批使用当前 Go 接口；变更单列表/详情按 `change_request_id` 查询服务端步骤，不再以浏览器缓存作为审批进度来源。 |
| 价目表与定价策略 | **已按 Go 契约接线，待真实验收** | 定价策略、草稿生成和发布使用当前 Go DTO；发布后的审批进度从通用变更单列表/详情恢复，跨浏览器账号可读取同一服务端状态。 |
| 客户管理与客户报价 | **9a 已真实联调；9b 已接线待真实验收** | 9a 客户列表、移交和报价生成已完成真实验收。前端现已按 Go 契约接入特价申请、按最新成本刷新和 XLSX 导出；9b 已有 HTTP 契约自动化，尚未在运行中的真实数据库环境完成浏览器验收。 |
| 客户 Portal | **9c/P1-8 已接线待真实验收** | 首页、当前价目表、接受报价、账务概况和通知使用真实 Go 路由；报价与合同分别传 `kind=QUOTE/CONTRACT` 查询，使用服务端 `can_accept` 和各自精确分页总数。 |
| 供应商档案、资质、模型申请、对账 | **内部 Supplier Profile 查询已接线，待迁移及真实验收；模型申请已按 Go 契约接线** | 内部供应商列表/详情使用当前 Go 查询契约和数据域；`settlement_currency` 限 CNY/USD，需先应用 v28 迁移。资质附件/审核和对账写流程仍非真实后端能力。 |
| 财务 | **Mock / PROVISIONAL** | 汇率、授信、押金和账户页面为只读前端样例；当前 Go 路由没有对应接口。 |
| 工作台、告警、审计 | **10a—10c 已接线待真实验收** | 工作台使用 `/workbench/todos` 与 `/workbench/metrics`；告警使用列表与统一处理接口；审计使用列表和二进制导出，不再请求不存在的详情接口。HTTP 契约自动化已通过，真实权限/数据域/数据库和浏览器交互待验收。 |
| 权限、系统对接 | **Mock / PROVISIONAL** | 前端已有路由守卫、权限消费和相关页面，但独立权限管理、系统对接页面尚无对应已验收 Go 业务接口。前端守卫不是服务端授权替代品。 |

## 核心业务模块

### 登录与三个 Portal

登录页可选择内部用户、供应商或客户身份，并调用对应的 `/api/{portal}/auth/login`。Axios 自动携带内存中的 Bearer Token；受保护请求返回 401 时会清理会话。Portal 路由相互隔离，菜单和操作按钮根据服务端返回的权限快照展示。

开发模式可在明确关闭认证时使用临时身份，但该机制只服务于本地 UI/Mock 验证，不代表生产角色、数据域或授权规则。

### 模型管理

前端包含模型查询、创建/维护、别名、批量、上架和退役流程。当前 Go Backend 已有对应的主要写接口和退役审批入口，但真实联调范围不完整：模型列表可真实读取，options、详情、人工验证和系列聚合仍有缺口。模型生命周期最终以服务端状态为准，页面不会把 Mock 状态迁移当作真实后端结论。

### 供应商报价

供应商可查询可报价 SKU，以 `sku_id` 关联模型，按组件提交价格、汇率档位与供给约束；历史版本可作为续报输入，但新版本仍通过当前统一提交接口创建。内部采购侧可查询待审批报价、查看服务端差异/毛利材料并整单通过或驳回。

报价写入不携带展示用模型名称或币种，不用前端 DTO 推断服务端已保存的数据。版本号、归属隔离、有效状态、审批与激活均由 Go Backend 权威处理。

### 成本管理

关闭 Mock 时，成本菜单使用已对齐真实 DTO 的当前成本列表，展示 SKU、代表组件成本、底价、主供应商、供应商数量、版本和生效时间。历史、参数、比价、机会和锁主等后端能力尚未全部接到当前页面，不能由 Go 中存在路由推导为前端已验收。

### 其他模块

定价策略、官方价格变更进度、价目表发布审批和供应商模型申请已按当前 Go handler 接线，但尚未完成真实服务器验收；财务、组织权限和系统对接仍主要用于前端开发、交互验证和契约讨论。客户 9b/9c、工作台、告警和审计已经按当前 Go handler 接线，但在真实账号、数据库和浏览器环境完成验收前，不能描述为生产业务闭环。

## 供应商报价与 CSV 导入

这是当前前后端开发和联调最完整的业务主线。

### CSV 模板与范围

- 模板由真实 `GET /api/supplier/quotes/template` 返回带 BOM 的 CSV 文件。
- 支持全部可报价 SKU、历史报价 SKU、指定厂商和指定系列四种范围。
- LI-013 已完成：页面从真实可报价 SKU 响应汇总厂商/系列选项，系列随厂商联动；上传包含多个范围的总 CSV 时，预检结果会按当前厂商或系列过滤，并显示原始、保留与范围排除行数。
- CSV 仍以 SKU 为匹配依据，不增加可编辑厂商列；模板不会包含其他供应商报价、平台成本或毛利。

### Preview 与有效子集提交

- `POST /api/supplier/quotes/import/preview` 使用 `multipart/form-data` 上传 CSV，后端完整解析但不落库，返回逐行 `OK`、`WARN`、`ERROR` 和可提交的 `preview_items`。
- `ERROR` 行不会提交，也不会阻止其余有效子集；`OK/WARN` 行可以手动排除并恢复，页面持续显示最终提交数量。
- 确认时前端只回传当前有效且未排除的完整报价 DTO，由 `POST /api/supplier/quotes/import/confirm` 再次执行服务端校验并创建进入审批的报价。

### 官方价、倍率与绝对价

- 预检后按本次 SKU 补查当前官方价，页面显示组件基准。
- 有官方价时可在 `MULTIPLIER` 与绝对价之间切换，并使用 Decimal.js 计算显示值；没有官方价的组件只能使用绝对价。
- 最终价格、倍率一致性、精度和基准有效性仍由后端复核，前端计算只用于录入和提示。

### 生效时间

提交前若 `valid_from` 已早于当前时间，页面会提示后端可能钳制到提交时刻。真实响应返回 `clamped=true` 时，页面会显示后端最终采用的 `valid_from`，用户确认后才跳转。`valid_to`、时区和报价生命周期仍以服务端校验与返回为准。

## 本地启动

建议使用与 `package-lock.json` 兼容的 Node.js/npm 环境。当前验证记录使用过 Node.js 24 与 npm 11。

```powershell
cd D:\Codex\workspaces\实习\projects\model-pricing\model-pricing-web
npm.cmd ci --cache .npm-cache
npm.cmd run dev -- --port 5173
```

默认访问 `http://127.0.0.1:5173/`；端口冲突时以终端输出为准。

可用命令：

```powershell
npm.cmd run typecheck
npm.cmd test
npm.cmd run build
npm.cmd run test:browser
```

- `npm test` 执行单元测试和 `*.scenario.ts` HTTP 契约场景。
- `npm run build` 先执行类型检查再构建生产产物。
- `npm run test:browser` 需要运行环境提供 Playwright；仓库不额外固定浏览器依赖。

## 真实后端联调

### PRICE_BOOK_QUOTE E2E（测试环境）

在测试库迁移完成后设置 `E2E_TEST_DATABASE_URL`，运行后端集成目录的 `scripts/prepare-e2e-fixture.ps1`（需 `psql`）。脚本幂等检查账号授权、专用 GOLD 客户与专用 ACTIVE 策略，固定一个已有真实成本的已发布 SKU；缺少真实成本等前置数据时会报错。输出的非敏感 `public/e2e-fixture.json` 不纳入 Git。页面读取同源 `/e2e-fixture.json`；使用仓库提供的 Nginx 样例时，需将文件放在 `/app/model_bss-web/current/dist/e2e-fixture.json`，实际位置以服务器当前 Nginx `root` 为准。

测试构建显式设置 `VITE_E2E_ENABLED=true`、真实认证开启和 Mock 关闭。价目表页点击“新建一轮测试”创建新的 GOLD 草稿；“继续当前测试”从独立 localStorage 恢复本轮 ID。发布和两级审批继续使用原页面，客户 APPLY 继续使用客户页面。刷新、退出及切换账号不会创建新轮；只有创建返回有效且非空、无阻塞草稿后才覆盖当前 Run。此功能只覆盖价目表到客户 APPLY 链路。

真实联调建议在未提交版本库的 `.env.local` 中设置：

```dotenv
VITE_AUTH_ENABLED=true
VITE_MOCK_ENABLED=false
VITE_API_BASE_URL=http://localhost:8080/api
```

修改环境变量后必须重启 Vite。

- `VITE_AUTH_ENABLED=true`：使用三 Portal 真实账号密码登录。若在非开发构建中关闭认证，应用会进入配置错误页，不会自动获得管理员身份。
- `VITE_MOCK_ENABLED=false`：不注册 Service Worker，请求直接进入 Go Backend。业务环境必须保持该值。
- `VITE_API_BASE_URL`：Axios 基础地址；未设置时默认为 `/api`，适合由同源反向代理转发。示例地址仅用于本机联调。
- 登录入口：`/#/internal/login`、`/#/supplier/login`、`/#/customer/login`。
- Bearer Token 仅保存在内存，不写入 `localStorage` 或 `sessionStorage`；刷新页面后需要重新登录。
- 不要把真实 Token、密码、Secret 或内部连接信息写入 `.env.example`、源码、测试、日志或提交记录。
- 后端必须负责真实权限、数据域、幂等、状态迁移、金额校验和数据库事务；前端显示控制与 MSW 规则都不是安全边界。

本仓库当前提交的 `.env.development` 会启用 Mock，适合独立开发页面。要验证真实后端，请显式使用以上配置并确认浏览器中没有活动的 Mock Service Worker。

## 测试与验证

截至 2026-09-18，最新记录为：

- 前端 `npm.cmd test`：全套 **55/55 通过**，含客户 9a、9b/9c、工作台、告警和审计真实 wire 契约场景。
- `npm.cmd run typecheck`：通过。
- `npm.cmd run build`：通过，保留既有的大包体积提示。
- Go Backend：本次合并后的 API、workbench 等包测试通过；全量 `go test ./... -count=1 -p 1` 仍有一个远端新增的时间漂移用例失败，并因本地 PostgreSQL `127.0.0.1:5433` 未启动导致真库用例失败，详见 `VALIDATION.md`。未声称全量通过。
- LI-013、CSV 有效子集、倍率/绝对价与 `clamped` 提示已完成真实页面人工验收。
- 客户 9a 已在关闭 Mock 的真实浏览器中完成登录、搜索、三种报价、floor 冲突和移交双确认；客户归属在验收后已恢复。

自动化通过只证明受覆盖的前端逻辑、HTTP 契约和 Mock/fixture 场景没有回归，不等于所有 Portal、权限、数据库事务、后台任务或业务流程都完成真实验收。详细证据与时间线见 [VALIDATION.md](VALIDATION.md)。

## 当前边界 / Known limitations

- 模型 options、详情、人工验证与系列聚合尚未完全匹配当前 Go 路由；模型退役的真实通知与双签全链路仍需继续验收。
- 认证没有安全的会话恢复/刷新契约；客户真实账号、冻结后会话吊销和凭证生命周期尚未完整验收。
- 定价策略已按当前 Go 契约适配但待真实服务器验收；官方价格、价目表其他流程、财务、供应商资质/对账、组织权限和系统对接仍主要依赖 Mock/PROVISIONAL 契约。内部 Supplier Profile 查询已接线但需先应用数据库迁移；客户 9b/9c 与工作台、告警、审计也尚未完成真实环境验收。
- 客户报价/合同联合列表没有 `source_kind` 服务端筛选，也未返回 `special_price_status`；前端只能筛当前页且无法提前判断特价单是否可接受，见 `ISSUES.md` 31。
- 成本列表已经真实联调，但成本历史、参数、比价、议价机会和锁主尚未全部接到当前真实页面。
- 金额以十进制字符串传输，前端使用 Decimal.js 辅助计算；权威精度、舍入、floor 与倍率校验由后端负责。
- 时间使用带时区 ISO 8601。前端提示不能替代后端对 `valid_from`、`valid_to`、通知期、审批和生效任务的校验。
- 报价提交进入审批不等于已经生效；审批响应、待生效、激活、过期与剔除是不同生命周期阶段。
- MSW 数据位于浏览器内存，刷新会重置；它只用于开发和测试，不代表数据库持久化或真实跨主体隔离。
- `MANUAL_VALIDATION.md` 的部分准备说明仍保留早期 Mock-first 口径，执行人工路线时应以本 README 的环境配置和 `VALIDATION.md` 的最新状态为准。

## 相关文档

- [ISSUES.md](ISSUES.md)：当前问题、状态、验收条件和剩余限制。
- [VALIDATION.md](VALIDATION.md)：自动化、真实接口和人工联调证据。
- [MANUAL_VALIDATION.md](MANUAL_VALIDATION.md)：浏览器人工验收路线；部分旧 Mock 前提需结合最新状态使用。
- [MOCK_CONTRACT.md](MOCK_CONTRACT.md)：Mock 与 PROVISIONAL 契约，不代表后端已经实现。
- [后端接口文档](../model-pricing-backend/docs/api/README.md)：当前工作区中的 Go Backend API 文档入口；最终状态仍以实际路由、实现和联调结果为准。
