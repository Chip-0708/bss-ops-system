# 前端问题日志

本轮最后更新：2026-09-22；以下新条目沿用既有编号，不改写先前历史结论。

首次登记：2026-09-14；最后更新：2026-09-21。范围：model-pricing-web 当前源码及本轮修复。1—19 沿用原审查编号，20—24 为安全核验与契约差异项，25 起为真实联调问题。记录事实与验收条件，不记录真实凭证。

严重程度：P0 必须立即处理的功能/数据/安全问题；P1 联调或需求变化可能较大返工；P2 明显维护/架构问题；P3 局部质量；P4 风格/小优化。当前没有确认 P0；若真实凭据泄漏应重新按实际暴露程度评级。

“已解决”仅表示该条前端缺陷的验收通过，不代表相关 Go 后端功能完成。“前端完成·待后端确认”不得视为关闭。以后新增编号递增；回归重新打开原条目；修复补日期、方式、验证及剩余限制。

| 编号 | 等级 | 状态 | 问题 | 代码位置 | 实际影响 | 处理/验收与剩余限制 |
|---|---|---|---|---|---|---|
| 1 | P1 | 前端完成·待后端确认 | 真实登录已接入，安全刷新会话恢复待后端支持 | src/views/LoginView.vue；src/api/auth.ts；src/stores/session.ts；src/router/guards.ts | 三门户可账号密码登录，按服务端权限访问，退出调用204吊销接口，受保护请求401清会话并回登录。token仅在内存，刷新需重新登录；缺少安全会话恢复接口，尚未完成全部验收。 | 2026-09-14：49项自动化及类型检查通过；实际内部/供应商账号通过当前前端API登录、受保护查询、204退出及旧token401。客户门户仅自动化验证，未用真实客户fixture。后端提供HttpOnly会话及恢复契约后补刷新恢复；不将Bearer明文写入浏览器存储。 |
| 2 | P1 | 已解决 | 价目表页面内置 SKU 和成本依据 | src/views/internal/pricing/PriceBookList.vue:42；src/api/pricing.ts getOptions；src/mocks/handlers/pricing.ts:49 | 旧页面硬编码 SKU/floor，改币种可能只换标签；真实数据接入易返工。 | 2026-09-14：选项经 API 加载，稳定 ID 和成本依据由服务端提供；按币种筛选，已有项禁止直接切币种，保存拒绝不一致。场景测试及浏览器保存通过。新 options 路径仍须后端确认（22）。 |
| 3 | P1 | 前端完成·待后端确认 | 报价写入已拆商业DTO并按sku_id关联 | src/api/quoteContract.types.ts QuoteSubmitRequest；src/views/supplier/SupplierQuoteNew.vue、SupplierQuoteImport.vue；src/mocks/handlers/quoteContract.ts | 旧请求从展示行Omit而来，名称/币种进入写入、CSV按名称校验，模型更名影响报价。 | 2026-09-14：现有页面只提交sku_id/fx_tier/constraints/components和整单有效期，model_name/currency仅查询展示；同SKU组件聚合，Mock记录保留skuId，查询按ID读取最新模型展示。测试覆盖注入展示字段400且不写入、更名后价格项仍关联原SKU。真实int64序列化及Go保存/查询仍待验证；Legacy Mock/display类型保留迁移说明，不代表GoDTO，见18/22。 |
| 4 | P1 | 前端完成·待后端确认 | 退役前端按通知与双签口径修正，真实接口待验证 | src/api/models.types.ts DeprecateModelResponseDTO；src/Models.vue submitRetire；src/mocks/handlers/models.ts；src/mock.ts；tests/model-lifecycle.test.ts | 旧响应类型把提交后DEPRECATING当作正常结果，流程提示漏了先通知，理由限制1000不匹配接口500；当前模型HTTP Mock还存在旧格式适配回归，基线测试40/41。 | 2026-09-14修复：提交DTO收敛PUBLISHED，移除临时两态类型；异常返回只提示核对勿重交，不增加写回/回滚。Mock返回wire报告/提交对象，补页面依赖options/contract list/detail；前置已上架保持。流程明确通知发出日至sunset_date≥30天、先通知再双人换签；无真实通知字段前仅服务端校验，generated_at不参与。理由1~500（rune计），Mock拒绝非法通知时间。43项自动化及Edge退役交互通过，含原Key回放、提交SKU回查、驳回审批单/标记、30天边界及501字拒绝。Mock演示通知日期与现有驳回实现不是Go证据，真实双签/响应/通知字段与replacement_sku_id仍待联调。 |
| 5 | P1 | 前端完成·待后端确认 | 上架权限码已对齐，角色展示契约待确认 | src/domain/permissions.ts；src/Models.vue canPublish；src/stores/session.ts | 原M1:P未在接口定义，真实M1:E账号无法上架；M1:E也用于档案编辑，单独权限码不能区分定价运营。 | 2026-09-14：移除M1:P，页面和开发权限使用M1:E，不按中文roleLabel或Mock身份推断真实角色。自动化覆盖无identityKey的真实权限账号，Mock仍拒绝非定价运营上架；Edge通过。用户尚不清楚profile角色字段，新增06—11文档也未定义；按钮可能对其他M1:E编辑者可见，角色体验与真实后端授权仍待确认。 |
| 6 | P1 | 已解决 | 幂等指纹碰撞及待重试请求互相覆盖 | src/api/idempotency.ts:7；src/api/http.ts:73 | 旧 32 位哈希 Aa/BB 可碰撞，修改请求清理旧 Key，且未隔离用户；结果未知时重试可能重复写入。 | 2026-09-14：SHA-256 按门户/用户/作用域/请求体隔离，保留其他待定 Key；未知响应保留，账号切换时中止提交。纯函数并发/碰撞/改回请求测试及 Axios 网络失败重试通过。后端仍须实现持久幂等，见22。 |
| 7 | P1 | 前端完成·待后端确认 | Mock floor 校验信任客户端 | src/mocks/handlers/pricing.ts:49、119 | 客户端改 floor 可绕过旧售价校验，Mock 不能代表真实成本约束。 | 2026-09-14：与2一并修复 Mock，保存/提交重新读取当前选项，拒绝过期成本版本、币种错配，忽略客户端 floor。测试覆盖 floor 变动与原子失败。Go 必须实现同等权威校验，见22。 |
| 8 | P1 | 已解决 | 客户报价选择器受主列表分页限制 | src/views/internal/customers/CustomerWorkspace.vue | 原选择器只展示客户主列表当前页，其他客户无法选；查询失败与空结果混淆。 | 2026-09-20：报价弹窗独立调用现有客户列表分页/搜索，保留已选客户并过滤过期响应；显式显示查询失败、重试、空结果与加载更多。类型检查及 72 项测试通过；真实浏览器交互待验收。 |
| 9 | P1 | 前端完成·待后端确认 | 普通报价审批动作按采购整单单步契约修正 | src/api/supplierQuotes.ts；src/api/supplierQuotes.types.ts；src/views/internal/supplierQuotes/SupplierQuoteList.vue；src/mocks/handlers/supplierQuotes.ts | 已读需求C8及§4.1未要求第二人，旧暂定节点和stepId增加无当前收益的复杂度；通过响应与即时生效状态不同，驳回原因旧限制不匹配。 | 2026-09-14：去掉报价节点/stepId及可选审批意见，approve空对象；严格响应APPROVED_PENDING/approved_at/activate_at/immediate，后续重新查询，不把提交响应当最终状态。reject理由10—500 rune。43项自动化覆盖越权、未知字段、边界、重放、未来待生效与即时实际EFFECTIVE，Edge通过。官方价/退役双签不受影响；本轮已迁移pending/diff材料，页面展示后端毛利结论，不在客户端计算；真实归属授权和事务激活待Go验收。 |
| 10 | P1 | 已解决 | 异步查询旧响应覆盖新目标 | src/Sync.vue；src/views/internal/customers/CustomerWorkspace.vue | 快速切换对象或分页时旧响应可覆盖当前视图，操作目标与用户预期不一致。 | 2026-09-20：当前真实页面已无原记录中的价目表/采集详情请求；客户主列表、报价选项、移交预览及采集列表增加请求序号保护，移交确认使用操作快照。类型检查及 72 项测试通过；真实网络延迟场景待验收。 |
| 11 | P1 | 待处理 | 资质附件仅保存文件名 | src/views/supplier/SupplierQualifications.vue:49、65；src/api/supplierPortal.types.ts CreateSupplierQualificationRequest | 文件未上传，审核人无法取得真实材料；文件名不能替代持久附件ID。 | 联调前确认上传/下载授权和 attachment_id 契约；页面只持有附件ID/元数据，不绑定 MinIO/S3 实现。 |
| 12 | P2 | 已解决 | 部分确认流程锁定/快照偏晚 | src/views/internal/pricing/PricingPolicyList.vue；src/views/supplier/SupplierProfile.vue；src/Sync.vue | 原确认前后可修改输入或多次启动确认，实际发送与确认内容不一致。 | 2026-09-20：定价策略、供应商资料和采集建单在确认前锁入口并截取请求快照；确认期间禁用表单与关闭操作，取消后解除锁定。类型检查及 72 项测试通过；真实浏览器交互待验收。 |
| 13 | P2 | 已解决 | 定价参数允许非有限 Decimal | src/views/internal/pricing/PricingPolicyList.vue:63；src/mocks/handlers/pricingPolicies.ts:39、43；src/domain/money.ts:5 | 仅 isPositive 会接受 Infinity，导致倍率/取整运算异常。 | 2026-09-14：随19使用共享有限普通十进制校验，前端和 Mock 同步；Infinity/NaN/科学计数/超8位小数测试通过。 |
| 14 | P2 | 已解决 | 模型读取能力字段拒绝新增字段 | src/api/models.catalog.ts；tests/model-catalog.test.ts | 后端增加可选能力字段会令列表或详情整体失败。 | 2026-09-20：读取忽略未知能力字段并从展示/编辑模型中剔除，已知字段仍严格校验；写入白名单不变。新增兼容回归测试，类型检查及 72 项测试通过。 |
| 15 | P2 | 待处理 | 错误返回仍需统一展示契约 | src/api/http.ts:48、102 | HTTP及200业务错误通道不完全一致；200业务错误 fieldErrors 未透传，400等后端技术文案仍可能展示。 | 本轮已对已知凭证文本脱敏并统一5xx友好文案；后续约定业务错误码映射和字段错误，补有意义的错误场景。 |
| 16 | P2 | 暂缓 | Models.vue 同时维护多个独立工作流 | src/Models.vue save/saveVerify/changeAlias/batch/publish/submitRetire | 查询、档案、别名、验证和退役同时变化时容易连锁修改；问题在职责而非单纯行数。 | 随实际需求拆独立表单/流程；当前不做整页重写或引入 DDD。 |
| 17 | P2 | 已解决 | 默认测试未覆盖 HTTP 场景且浏览器脚本过时 | package.json scripts；tests/run.mjs；tests/browser.mjs；tests/shell-smoke.mjs | 默认绿灯不能证明页面/API/Mock 契约一致，旧选择器已不匹配实际界面。 | 2026-09-14：npm test 自动包含 *.scenario.ts；浏览器脚本自行启停临时服务，修复实际标题/权限/确认流程，覆盖选项保存、审批、主题、关闭MSW的HTTP及脱敏。41项自动化和Edge通过；当前浏览器脚本为选定关键场景，不声称全面端到端覆盖。 |
| 18 | P3 | 暂缓 | Legacy 类型/错误类和角色桥重复 | src/types.ts:17、85；src/domain/common.ts:10；src/api/models.types.ts；src/stores/session.ts startDevSession | Mock迁移保留重复 Model/ApiError/角色来源，阅读成本增加。 | 随 Legacy Mock 迁移清理；不为了目录漂亮改全工程。 |
| 19 | P3 | 已解决 | 日期展示和金额校验重复且边界不一致 | src/domain/date.ts:1；src/domain/money.ts:5；src/views/internal/pricing/PriceBookList.vue:55、59；src/views/internal/customers/CustomerWorkspace.vue:20、101 | 无效日期可能格式化报错；多处金额精度约束不同，维护一项规则需改多页。 | 2026-09-14：已实际重复的日期展示统一共享方法，保留各页空值文案/秒显示；报价及策略普通金额校验统一有限正数/至多8位小数，Mock同步，必要零值明确参数。时间等价/无效值和金额测试通过。比例、金额业务算法仍按模块保留，Go最终精度需确认。 |
| 20 | P1 | 已解决 | 诊断/审计自由数据缺乏凭证展示脱敏 | src/domain/sensitive.ts:1；src/api/audit.ts；src/api/integration.ts；src/api/officialPrices.ts；src/api/http.ts | 快照、错误、来源文本和导出若含 Token/Key，原 JSON 展示可能泄漏。当前测试数据没有确认的真实凭据。 | 2026-09-14：API展示边界递归脱敏常见凭证字段、sk-/Bearer/JWT/带名赋值和URL凭据；错误、审计导出也处理。人工测试Key在DOM/存储/导出均无原文。规则不识别任意无名秘密，后端不得返回长期明文凭证，见21。 |
| 21 | P1 | 待处理 | 真实凭证过期/轮换/吊销及响应最小化缺契约 | src/stores/session.ts token/restoreSession；src/api/http.ts Authorization/401；src/api/audit.ts | 当前 token 在内存，未发现长期凭证写入 localStorage/sessionStorage；没有真实签发/刷新/失效轮换契约。前端脱敏不能阻止响应/网络面板看到后端原文。 | 真实认证上线前由后端确认 expires_at/refresh/撤销/轮换策略，诊断及审计响应先在后端脱敏；前端对401清会话已测。不能自行宣称十分钟失效或在前端实现秘密轮换。 |
| 22 | P1 | 待处理 | 已实现模型/报价契约尚未落实，其他契约仍待确认 | src/api/models.ts；src/api/supplierPortal.ts；src/api/supplierQuotes.ts；src/api/pricing.ts | 现在模型/报价有文档依据，不能继续全部标为未知。前端和Mock自洽不代表匹配Go；价目表options、认证和上传仍不在此次两份文档覆盖内。 | 2026-09-14收到04-models(2).md和05-quotes(1).md。2026-09-16 已核实并补齐模型系列视图与 `/internal/models/options`，前端按真实 int64 契约接入；模型详情/验证、价目表选项及其他未文档化接口仍需分别核验，因此本项保持待处理。 |

| 23 | P1 | 前端完成·待后端确认 | 现有报价页面与阶段5路径/提交/导入契约已迁移 | src/api/quoteContract.ts、quoteContract.types.ts；src/api/supplierQuotes.ts；src/views/supplier/SupplierQuoteNew.vue、SupplierQuoteList.vue、SupplierQuoteImport.vue；内部SupplierQuoteList.vue | 旧草稿保存再submit、quote-options、旧历史/详情、JSON预检及batchId提交不能仅改baseURL接Go。 | 2026-09-14：迁移/supplier/skus、/quotes/history、/quotes/template、multipart import/preview、完整items import/confirm、直接POST进入APPROVING；内部/pending及/{id}/diff。valid_to必填、USD八档、逐SKU整型约束、组件白名单、string价格/null倍率。续报复制历史商业条件后经同一POST生成服务端版本；移除未文档化草稿API/编辑路由及旧基准版本字段。Mock重校验/非终态互斥/时间钳制/幂等，浏览器不注册旧报价写路由。47项自动化及浏览器新建/历史/流下载/预检编辑/确认提交通过，真实Go/数据库/数据域/成本预演/异步激活未验收；到期扫描、人工剔除、补录等无现有页面的新功能不在本轮新增，见22。 |

| 24 | P1 | 处理中 | 阶段6—11前端契约需随真实后端逐模块对齐 | src/api/cost.ts、costParams.ts；src/api/supplierQuotes.ts；src/api/customerPortal.ts；src/api/workbench.ts；src/api/alerts.ts；src/api/audit.ts | 已实现接口若仍沿用草案路径或 Mock DTO，关闭 Mock 后会失败；未实现模块只能按已确认契约前置设计。 | 2026-09-16：成本相关接口已分批对齐。2026-09-17：报价管理补接到期、异常、移除和特权补录。2026-09-18：合入后端 9b/9c、10a—10c 后，前端接入特价/刷新/导出、客户门户、工作台、告警与审计；移除不存在的告警/审计/客户通用详情请求，新增真实 wire HTTP 场景，55/55 通过。真实账号、数据库、浏览器验收及阶段8其他模块仍未完成，因此保持处理中。 |
| 25 | P1 | 已解决 | 真实成本列表沿用旧 Mock DTO，渲染异常使表格空白且路由画面卡住 | src/views/internal/cost/CostBaselineList.vue；src/views/internal/cost/CurrentCostBaselineList.vue；src/api/cost.ts、cost.types.ts；src/router/internal.ts | 后端返回 2 条当前基线，但行里没有旧页面所需的 `status`；状态映射读取 `.label` 时抛异常。统计和地址栏先更新，表格不显示，点击其他菜单后旧页面仍停留。 | 2026-09-15：真实模式改用现有成本列表契约，展示 SKU、代表组件成本、底价、主供应商、供应商数、版本及生效时间；查询使用后端支持的 `keyword`、`only_single_point`，未接通的旧详情/参数入口不在真实页出现。类型检查及 49 项自动化通过；用户确认刷新后两条基线可见、页面切换正常，前端缺陷验收通过。Mock 成本页面保留；成本历史、计算参数与完整成本预演仍需后续单独联调，不由本项关闭。 |
| 26 | P1 | 已解决 | CSV 预检缺少有效行剔除及倍率/绝对价切换 | src/views/supplier/SupplierQuoteImport.vue；src/domain/quoteImport.ts | 用户无法排除不想提交的 OK/WARN 行，也无法在预览页核对当前官方价并修改倍率；含错误行时整批被前端阻止。 | 2026-09-16：已支持有效行排除/恢复、错误行不提交但允许提交其余子集、提交数量统计；补查 `/supplier/skus` 展示当前官方价，支持倍率/绝对价切换，无官方价时禁用倍率。首轮发现异常大整数 ID 令官方价整页校验失败、旧 `error_count` 静默拦截提交；改为按预检 SKU 编码逐项补查并移除旧拦截。类型检查、生产构建及 52 项自动化通过；用户随后确认倍率选择和导入提交均可用，问题关闭。LI-013 不在本项。 |
| 27 | P2 | 已解决 | 过去生效时间被钳制后提示不够明确 | src/views/supplier/SupplierQuoteNew.vue；src/views/supplier/SupplierQuoteImport.vue；src/domain/quoteImport.ts | 旧实现只显示短暂 toast，跳转后用户难以核对后端采用的最终生效时间。 | 2026-09-16：手工和导入提交前均提示过去时间会被后端调整；响应 `clamped=true` 时以需主动确认的结果框显示后端 `valid_from`，确认后才跳转。类型检查、构建及时间判断测试通过；用户完成真实导入并确认流程可用，问题关闭。 |
| 28 | P2 | 已解决 | CSV 厂商/系列范围不可操作，且上传总表后预检不按范围筛选 | src/views/supplier/SupplierQuoteImport.vue；src/api/quoteContract.ts、quoteContract.types.ts；src/domain/quoteImport.ts；后端 supplier SKU DTO/Repo | 模板接口支持范围参数，但供应商 SKU 响应原先不返回归属 ID，前端只能暴露内部 ID；首轮修复仅筛选下载模板，上传包含多厂商的总 CSV 后预检仍显示全部行。 | 2026-09-16：后端为 `/supplier/skus` 增补 `vendor_id/family_id`；前端提供可搜索厂商及联动系列选择，并在预检响应后按当前范围过滤 `rows/preview_items`、重算统计，显示原文件/保留/范围排除行数。切换范围保留上传文件但清空旧预检，可直接重复筛选。完整 Go 测试、前端类型检查、生产构建及 54 项测试通过；真实后端模板筛选接口已核对，用户随后确认上传总表按所选厂商/系列预检可用。CSV 不新增可编辑厂商列，SKU 仍是导入匹配依据。 |
| 29 | P1 | 已解决 | 客户工作区调用未实现的草案接口，且 M8/M9 权限语义与阶段 9a 后端相反 | src/api/customers.ts、customerQuotes.ts；src/domain/permissions.ts；src/views/internal/customers/CustomerWorkspace.vue | 关闭 Mock 后，列表使用错误查询名/DTO，详情、报价列表、模板、编辑等请求必然 404；有权查看客户的 M8:V 用户还会被路由误拦截。 | 2026-09-17：按真实 9a 收口为客户列表、双确认移交和 APPLY/CLONE/TEMP 统一生成接口；API 层归一化 int64 ID，页面不再调用未实现接口，M8 管客户、M9 管客户报价。迁移 23 已确认；真实账号完成列表/数据域/403、三种报价、floor 409、幂等重放和移交双确认，浏览器确认无详情、模板、报价列表或编辑请求。后端全量测试、前端 54/54 测试及构建通过，问题关闭。 |
| 30 | P1 | 已解决 | 后端幂等首次写入无法持久化 NULL 结果 | ../model-pricing-backend/internal/repo/idempotency.go；internal/repo/idempotency_test.go | 写入 DTO 使用 `interface{}` 承载空 `result_json`，GORM 无法推断字段类型；业务接口可成功，但幂等记录未落库，同键重放保障失效。 | 2026-09-17 真实联调日志发现并修复：字段改为 `[]byte`，新增事务内真库回归测试；唯一 TEMP 请求同键提交两次返回同一报价 ID，数据库恰好一条 `DONE` 且含结果。后端全量测试、vet、build 通过。 |
| 31 | P1 | 已解决 | 客户报价/合同联合列表缺少稳定分区与特价可接受性字段 | src/api/customerPortal.ts、customerPortal.types.ts；src/views/customer/CustomerPortal.vue；后端 PortalQuoteItem/ListQuotes | 旧接口混合 QUOTE/CONTRACT，前端按当前页过滤导致空页和错误总数；前端还只能猜测报价能否接受。 | 2026-09-21：后端增加 `kind/status/can_accept` 并分别分页统计；前端报价、合同菜单分别传 `kind=QUOTE/CONTRACT`，报价状态可筛选，接受按钮只读服务端 `can_accept`。Mock 与 HTTP 契约测试同步；2026-09-22 复核确认集成后端原有 kind/status 独立分页及 can_accept；重复接受边界单独记录在 42 号。真实客户账号浏览器验收仍待执行。 |
| 32 | P1 | 已解决 | HTTP 测试环境缺少 Web Crypto，幂等写请求在 Axios 前失败 | src/api/idempotency.ts；tests/idempotency.test.ts | 远端普通 HTTP 页面中 `crypto.subtle` 与 `crypto.randomUUID` 不可用，供应商 CSV 确认等所有带幂等配置的写请求在生成 Key 时抛错；Network 与后端均无请求，并被误报为网络失败。 | 2026-09-18：保留安全上下文 SHA-256/randomUUID 原行为；缺少 subtle 时使用确定性 128 位本地指纹，缺少 randomUUID 时由 `crypto.getRandomValues` 生成 RFC 4122 v4 Key，继续按原 sessionStorage 键复用和清理。类型检查、生产构建及 61 项测试通过。用户已将 r2 部署到测试服务器，`current` 指向 `/app/model_bss-web/releases/20260918-r2`，Nginx `:8081` 正常加载新构建；普通 HTTP 环境实际完成录入暂存及后续审批，确认 fallback 写请求可到达后端并完成业务流程，本项关闭。 |
| 33 | P1 | 前端完成·待后端确认 | 定价策略页面使用新页面模型，真实 Go API 仍采用 DDL wire DTO | src/api/pricingPolicies.ts、pricingPolicies.types.ts；src/views/internal/pricing/PricingPolicyList.vue；src/mocks/handlers/pricingPolicies.ts | 旧页面直接发送 camelCase 草稿，缺少 Go 必填的 code/scope_type/price_method/param_value/status，真实创建返回 400；Mock 接受旧结构并掩盖差异。 | 2026-09-18：页面 Draft 与 Go wire DTO 分离，API 层双向转换；TARGET_MARGIN 百分数转 MARGIN 小数，官方倍率直传，固定 ALL/null、单等级、DRAFT、priority=100。新增必填编码，移除后端不支持的说明、多等级和自定义取整交互，不再调用不存在的单条 GET；Mock 改用真实 snake_case 契约。类型检查、生产构建及 63 项测试通过。随后按已确认 PUT 契约补充 DRAFT→ACTIVE 激活、ACTIVE→ARCHIVED 归档；前端显式禁止其他迁移，复用原 PUT 与幂等机制，Mock 和场景测试同步。新增状态操作尚未编译及真实服务器验收；创建、列表回显、草稿更新和状态变更验收前不关闭。 |
| 34 | P1 | 前端完成·待后端确认 | 价目表页调用后端不存在的列表、详情、options、PUT 与 submit 契约 | src/api/pricing.ts、pricing.types.ts；src/views/internal/pricing/PriceBookList.vue；src/mocks/handlers/pricing.ts | 旧页面依赖完整 CRUD 和手工 SKU 售价编辑，关闭 Mock 后会产生 404/405，且创建 payload 与真实 Go generateDraftBody 不一致。 | 2026-09-18：按已确认 Go 契约收口为 `POST /price-books` 生成草稿与 `POST /price-books/:id/publish` 发布；请求/响应使用真实 snake_case wire DTO，复用 Idempotency-Key；页面只采集 level_code、currency 及可选 policy_ids/sku_ids，展示 draft_id、统计、diff_report，并仅提供 IMMEDIATE 发布及 PRICING_OP→FINANCE 审批结果。列表、详情、options、PUT、submit、手工 SKU 编辑和 rollback 均不再暴露。Mock 与场景测试同步；真实服务器仍需验收生成、红线阻塞与两级审批。 |

| 35 | P2 | 处理中 | 价目表与采集审批缺少完整服务端预览及批次业务编号 | src/api/changeRequests.ts；src/views/internal/pricing/PriceBookList.vue；Go publish_service.go；src/Sync.vue | 原价目表进度只存在 sessionStorage，无法跨浏览器/账号同步；旧发布 payload 不含价格差异，财务跨浏览器无法取得业务明细；采集批次仍无法回查选择或显示独立 batch_no。 | 2026-09-21：审批进度已改为服务端变更单列表/详情；本轮发布 payload 增加 level_code、version_no、diff_report，财务账号可从同一详情跨浏览器查看 SKU 原价、新价、floor 与校验结果。旧审批单不会自动补历史明细。采集 batch_no/SKU 回查及通用幂等冲突关联仍未解决，因此本项保持处理中。 |
| 36 | P1 | 待处理 | buyer_b 财务审批价目表返回通用系统错误，结果未知 | src/views/internal/pricing/PriceBookList.vue approveStep；Go internal/repo/model_deprecate.go DecideApproval、internal/repo/pricing_publish.go ApplyPriceBookPublish、internal/api/middleware/idempotency.go | 2026-09-20 用户报告财务审批响应 code=10000，requestId=b23d01b1-b7ca-4d37-9ef3-0fb784e6c84b。单靠响应无法判定审批或价目表是否已生效。当前 Go 源码中审批步/变更单更新发生在生效回调前；回调失败而外层幂等事务提交 FAILED 时存在局部状态风险，尚未核对部署版本及真实数据库。 | 后端同事按 requestId 查原始错误；只读核对该审批单的 approval_step、change_request、price_book、idempotency_key，再给出事务边界与失败恢复方案。前端不通过换幂等键或假设成功来消除报错；真实状态确认前不重试。最后更新 2026-09-20。 |
| 37 | P1 | 处理中 | 供应商首页依赖不存在的专用 `/supplier/home` | src/views/supplier/SupplierHome.vue；src/api/supplierPortal.ts；src/mocks/handlers/supplierPortal.ts | 真实环境首页加载失败；旧 Mock 捏造资质提醒、待办、通知和统计，掩盖接口缺口。实际是前端接线问题，不要求后端新增首页接口。 | 2026-09-20：首页改用已有报价历史接口与页面入口；状态数量取后端筛选结果的 `total`，最近报价进入现有详情，汇率档位复用报价规则；移除专用首页 API 和 Mock 响应。类型检查、71 项测试及生产构建通过；真实环境尚待验收，不展示未有接口支撑的资质提醒、申请待办或通知。最后更新 2026-09-20。 |
| 38 | P1 | 前端完成·待后端确认 | 供应商模型申请仍使用暂定草稿/详情/提交契约 | src/api/supplierModelApplications.ts、supplierModelApplications.types.ts；src/views/supplier/SupplierModelApplications.vue；src/components/ModelApplicationReviewPanel.vue | 旧页面调用真实 Go 不存在的草稿、详情和二次提交接口，内部也无法使用真实审核决策。 | 2026-09-21：供应商改为一次 `POST /supplier/model-applications` 提交并读取自身列表；内部接入列表和 APPROVE/MERGE/REJECT 决策，展示重复候选、驳回原因与合并 SKU，终态保留灰色“已审核”。Mock 与契约测试同步；真实供应商/MODEL_OPS 账号及数据库副作用待验收。 |
| 39 | P2 | 已解决 | CSV 参考列变更 WARN 缺少醒目说明 | src/views/supplier/SupplierQuoteImport.vue | 后端已对 sku_code/model_name/currency 不一致返回 WARN 和库内当前值，但旧预检表只显示普通 messages，用户容易忽略并误以为参考列生效。 | 2026-09-21：预检区增加 WARN 说明并区分 OK/WARN/ERROR 标签，逐行继续展示后端 warnings/messages，明确 SKU ID 为匹配键且以库内当前值为准；不修改 CSV DTO 或导入规则。 |
| 40 | P1 | 前端完成·待部署验收 | 客户报价创建缺少真实价目表/历史报价预览，模型申请审核入口不易发现 | src/views/internal/customers/CustomerWorkspace.vue；src/api/customerQuotes.ts；src/views/internal/models/ModelApplicationReview.vue；Go customer quote-context | APPLY 只能看到说明，CLONE 需手填报价 ID，无法在提交前核对 SKU/价格；模型申请审核嵌在模型页顶部，用户难以定位。 | 2026-09-21：新增受 M9:V 和客户数据域约束的 quote-context 读取接口，返回当前生效价目表及该客户历史报价售价项，不返回成本/floor/毛利；前端 APPLY/CLONE 展示真实 SKU/价格并用历史报价下拉。模型申请审核复用既有接口与组件，改为独立菜单。客户门户价目表明确展示版本、等级和全部明细，接受成功后按钮显示灰色“已接受”。相关后端测试、前端类型检查及 72 项测试通过；真实账号部署验收未执行。独立生成合同接口不存在，本轮未增加假按钮。 |
| 41 | P1 | 处理中 | 内部 Supplier Profile 真实查询和结算币种未完成端到端验收 | src/api/suppliers.ts、suppliers.types.ts；src/views/internal/suppliers/SupplierList.vue；后端 supplier profile 查询及 v28 迁移 | 原列表/详情缺真实路由，页面展示无来源资质/附件/供给字段；缺 settlement_currency，商务字段需物理剔除。 | 发现及更新 2026-09-22：前后端列表/详情、CNY/USD、财务或归属采购可见商务字段已实现；旧响应竞态与无来源展示已清理。快照结构核对后新增 v28 迁移，但尚未执行。前端 typecheck、74 项测试及 build 通过；真实数据库和账号验收待完成。 |
| 42 | P1 | 处理中 | 客户接受报价可能重复提交，刷新后状态未由服务端可靠回显 | src/views/customer/CustomerPortal.vue；后端 PortalQuoteItem/AcceptQuoteTx | 特价单主状态生效而审批子状态仍 APPROVED 时，旧事务守卫可能再次接受；页面原接受操作未可靠锁定并刷新服务端状态。 | 发现及更新 2026-09-22：确认弹窗时即锁入口，接受成功后重新查询；已接受变灰不可点，失败不误标成功。集成后端原有 kind/status 独立分页及 can_accept；本轮将服务校验和事务 UPDATE 均收紧为拒绝已生效，定向测试通过。Mock 场景覆盖成功、失败与重复请求；真实库持久化及浏览器验收未完成，故保持处理中。 |
| 43 | P2 | 待处理 | 资质记录/角色数组缺少真实服务端分页契约 | src/views/supplier/SupplierQualifications.vue；src/views/internal/orgPermissions/OrgPermissionCenter.vue；对应 API/Mock | 两个列表型区块仅 Mock 返回数组；当前 Go 未注册资质或组织角色列表接口，也没有可核实的 page/size/total，不能用客户端切片冒充。 | 发现及更新 2026-09-22：已有真实主列表分页保持不变，“我的申请”查询已重置第一页。需先确认/实现真实存储、鉴权和分页接口，再接入这两页并验收筛选总数。 |

1—23 条目的发现日期和最后更新日期为 2026-09-14；24 号最后更新日期为 2026-09-18；25 号的发现、解决及最后更新日期为 2026-09-15；26—28 号的发现和最后更新日期为 2026-09-16；29—30 号的发现、解决及最后更新日期为 2026-09-17；31—35 号的原始发现日期不变并于 2026-09-21 更新；36—37 号发现和最后更新日期为 2026-09-20；38—40 号发现和最后更新日期为 2026-09-21；41—43 号发现和最后更新日期为 2026-09-22。后续更新应为对应条目单独补日期，保留原修复与重开历史。

## 2026-09-14 · 退役前端修正完成

用户授权先修正可明确的前端退役部分。保留其他既有工作区变动，没有恢复整个模型模块或改动供应商报价审批。

- DTO提交响应为PUBLISHED，去掉旧DEPRECATING正常成功分支；异常返回不伪装批准，不自动重发/回滚。
- 页面说明先通知、双签换人、下线日距通知任务发出日至少30天；报告时间仅展示。真实通知字段尚无契约，不新增猜测字段。
- 退役理由限制500字，前端与Mock一致。HTTP Mock旧格式适配基线导致退役场景失败，补退役报告/提交及页面所需查询适配，未覆盖全部其他写接口迁移。
- 已验证43项自动化：Mock提交/回查PUBLISHED、驳回仅审批单REJECTED且pending标记清除、原数据不变及重放、距演示通知29天拒绝/30天通过。批准后的完整真实双签尚未联调。
- 本条保持前端完成·待后端确认；不以本轮前端测试关闭真实后端证据缺口。

## 2026-09-14 · 同事退役实现回复（最新口径，待交叉验证）

来源：用户转述同事回复。下列为实现报告，不是已读取Go源码或已执行接口的事实；需求仍作为业务验收依据。覆盖此前仅从接口响应示例推断“提交已经迁移生命周期”的判断。

1. 同事说原第1、2项代码已实现，只是文档写错/未写。本消息未包含那两项的原清单，不擅自对应到本日志1/2号或其他功能，不能据此关闭无关条目。
2. 提交时未改SKU生命周期；驳回只将change_request置REJECTED，SKU保持PUBLISHED。因此没有需要回滚的SKU迁移，不应增加“驳回恢复PUBLISHED”写操作。
3. 顺序为客户通知（≥30天提前期）→ 双人审批；提前期从“通知任务发出日”到sunset_date计算，不从申请提交日、影响报告generated_at或审批通过日计算。
4. 需要的接口证据：提交后响应与GET SKU均仍PUBLISHED；驳回后审批单REJECTED且SKU原状态不变；首签后不迁移、双签完成后才DEPRECATING；下线日对通知发出时间≥30天，缺通知或不足30天拒绝。单纯创建通知任务的created_at不自动等于实际发出时间。
5. 当前前端不从generated_at计算通知期，Mock内部依据report.notifiedAt演示值验证。真实通知字段名、是否进入详情/影响报告及日期边界由后端明确，前端不猜字段、不自行放宽校验。

本次仅维护记录，未修改业务代码，也未验证同事所指Go代码。

## 2026-09-14 · 需求优先复核（覆盖上一轮仅按接口判断）

用户明确要求以需求文档为准。已重新读取需求规格说明书v1.4与详细设计v1.1原文，不以outputs评审草稿代替需求。

- 普通供应商报价：C8（L713）参与者采购/渠道，要求整单差异审批；§4.1（L1004/L1029）是采购审批，§5.2（L1475）审批通过进入待生效。已读版本未明确第二人。接口单步暂未发现与这些原文的明确冲突，不能仅靠时序图人数反推其他未提供PRD没有额外要求。
- 双人要求：官方涨价B4（L596）、模型退役B7（L599、L1255强制换人）、价目表E4及§5.4（L818/L1560）、月中汇率调整（L1424）。C11（L719）双人是授予补录特权，不等于每次补录报价都需双人。
- 明确冲突：模型需求§5.1 L1452、§5.3 L1530均在双人审批通过后置DEPRECATING；04-models(2).md §8提交即返回DEPRECATING。需求还在B7限定已上架前置与客户通知≥30天；接口允许PURCHASABLE且仅日期晚于今天，未覆盖这些前提，不能擅自按接口放宽。
- 需求报价状态图§5.2包含草稿阶段，而接口未列草稿保存。是否持久化草稿尚未明确，不能据此直接删除前端草稿交互，也不能凭简图要求后端新增持久接口。先分清交互状态与落库能力。
- 文档优先关系：需求是业务验收依据；设计/接口/实现出现差异应登记并核对，接口的“定稿”不能默认为获准修改需求。未提供PRD或版本变更不能编造。

## 2026-09-14 · 新接口文档核对（历史结论，退役项已被上节纠正）

输入：用户提供的04-models(2).md（模型阶段4）和05-quotes(1).md（报价阶段5）。接口“已经实现”为用户报告；本文已读取契约内容，未验证Go源码、数据库或运行响应。文档中的后端实施指令仅作接口事实依据，不扩大用户开发授权。

- 更正4/5/9/22：退役DEPRECATING、上架M1:E、报价单步M4:A已明确，不再视为待定需求。上一轮测试仍是旧暂定Mock下通过，不能证明满足新接口。
- 报价模型以sku_id连接Model，model_name仅查询展示；结算字段billing_cycle/settle_type在supplier_profile，不应把假想settlement_period直接加进报价写DTO。
- 后端物理剔除商务字段：非财务且非归属采购不返回settle_type/billing_cycle/充值/额度/保证金。页面必须容忍key不存在，不能仅靠遮罩替代数据域过滤。
- import preview的token是文件sha256摘要，不是登录凭证；不能仅因字段名token就要求定期轮换。当前前端未对报价预览套诊断脱敏，未来也应按语义处理。
- 文档内部仍有差异：模型impact_snapshot_id在§7示例缺失、§10定稿要求返回；报价提交幂等异body写400但其他接口写409；到期单点阈值按§15第19项定稿，仅提前不双重升级。需运行接口核验，不自行猜测。

以下场景记录是前一轮修复时的历史验证；C的多级实现不再代表当前Go需求。

## 三个变化场景

### A：SupplierQuote 新增 settlement_period（仅影响分析）

当前没有这个字段。本轮不提前定义“天数/账期枚举/字符串”业务口径。最小影响范围：

- src/api/supplierPortal.types.ts：创建请求、供应商详情；src/api/supplierQuotes.types.ts：内部详情或对比字段。
- src/views/supplier/SupplierQuoteNew.vue：填写、校验、请求；src/views/supplier/SupplierQuoteList.vue：展示；内部 SupplierQuoteList.vue：审批核对展示。
- src/mocks/data/supplierQuotes.ts、src/mocks/handlers/supplierPortal.ts、src/mocks/handlers/supplierQuotes.ts：存取、验证、查询投影；tests/quote-linkage.scenario.ts、tests/quality.scenario.ts：实际字段保存及两端一致性。
- 如要求 CSV 导入，还需 src/api/supplierPortal.types.ts 导入行类型、supplierPortal.ts 的 importHeaders/解析/模板/预检，以及 src/views/supplier/SupplierQuoteImport.vue 导入预检页面。
- API Module 已转发请求，若路径不变通常无需改方法；Router、Pinia、Axios、模型技术参数、Cost/Pricing算法不应因仅新增商业账期而修改。续报应复制该商业字段，不能丢失。

变化涉及多个展示/写入边界是合理成本；当前3号名称依赖仍会放大模型变化影响。settlement_period 是否影响付款、成本或历史快照需业务确认，不能默认传播重算。

### B：完全关闭 MSW，接入真实 Go（HTTP 场景已测，Go 未联调）

实际通过：独立 Vite 服务 VITE_MOCK_ENABLED=false，禁用 Service Worker，审计页面经 auditApi → Axios → 本地 HTTP 契约测试接口查询/详情/导出，确认收到请求、无注册Worker、敏感展示脱敏。另有 Axios 场景检查真实Authorization且无X-Mock-Identity、失败重试Key与401清会话。

理论修改：部署环境 VITE_API_BASE_URL/VITE_AUTH_ENABLED/VITE_MOCK_ENABLED；认证API与session恢复（1/21）；API types/modules 与真实契约对齐（3/4/9/22）；上传（11）。在Go遵守现有已定DTO的模块，Vue/Router/局部状态无需因为关闭MSW而修改。未定字段/写入模型变化会影响对应表单和详情；不能声称所有页面零修改。前端没有连接PostgreSQL/文件系统的代码；上传按附件ID契约对接可避免绑定MinIO/S3。当前测试没有真实Go/数据库持久化/对象级权限，不能宣称联调完成。

### C：单级改多人/多级（串行两级已测）

本轮已让 SupplierQuote DTO/UI 容纳中间 APPROVING、节点进度、当前stepId；Mock根据当前处理人推进，最后一步才 APPROVED_PENDING。测试覆盖首步后仍审批中、下一步、错误处理人、旧步骤409以及幂等重放。默认配置保持原单级。

后续接串行Go节点主要对齐 types/API 与后端处理人权限契约，页面无需重做终态假设。多人会签/或签、并发投票、转交、撤回仍需规则/DTO/展示变化；未为假想流程搭建通用状态机。模型退役、官方价审批是其他独立流程，不声称 SupplierQuote 改造已统一所有审批。新文档明确当前报价单步，9号应对齐当前接口；只有未来明确需求变为多级时才重新设计，不能提前要求Go增加节点。

## 当前优先顺序

先落实已明确模型/报价契约（3、4、5、9、23），核验真实认证/凭证和未覆盖接口（1、21、22），再处理客户选项、详情竞态、附件上传（8、10、11）。16、18 随实际维护需求推进。验证记录见 VALIDATION.md；本日志不得替代后端安全验收。

## 2026-09-14 · 后续修正与补充接口文档

- 5/9已完成本轮明确前端修正，未关闭后端证据缺口；23仍待处理，供应商提交/导入及内部pending/diff查询尚未迁移。
- 当前报价单步覆盖历史场景C的暂定多级实现：已经移除节点与stepId。未来确有多人/多级需求时需修改DTO、动作、处理人权限、Mock、页面进度与测试；不预建通用审批框架。其他实际双签流程保持独立。
- 新增06—11文档仅作为草案依据。07提到PROCUREMENT与approval_step.required_role，但未提供登录/profile中对应字段；不能由此编造用户角色模型。11的client_secret和短期token属于开放接口，明确与三门户登录态隔离；具体有效期、轮换、吊销仍未明确，沿用21。
- 类型检查、43项自动化、生产构建及Edge浏览器关键流程通过；构建存在大包提示，本轮不扩大到拆包优化。无Git提交/推送/部署。

## 2026-09-14 · 已实现模型/报价文档对齐（最新）

范围：04-models(2).md、05-quotes(1).md覆盖的现有功能和调用；不把06—11未实现草案当作现网契约，不新增文档中尚无页面的运维扫描/补录等功能。

- 模型：HTTP维护PUT /{id}与别名suggest返回wire格式，publish支持可选remark；退役允许可选replacement_sku_id，仍按需求/同事报告保持提交PUBLISHED。详情/options/人工验证未在04文档定义，保留暂定标记；后端角色/profile和通知时间证据仍待确认。
- 报价：页面→Quote API→Axios→MSW wire处理器，共享原报价记录；前端无MSW/Mock数据依赖。另存元数据仅补原记录缺少的SKU ID/汇率/约束，不复制组件价格。Legacy类型与草稿/旧CSV路由仅供历史迁移测试，不用于当前浏览器报价写入。
- 内部审批仅查询pending/diff。动作完成后关闭材料并刷新队列；移出队列不表示已生效，接口未提供内部最终状态详情，不继续调用猜测GET /quotes/{id}。immediate只展示后端安排，实际激活由真实后端验收。
- 模型更名影响查询展示，不修改商业写DTO；新增商业约束只影响对应DTO/表单/导入及测试。多人审批未来仍按明确需求再扩展，未恢复暂定stepId机制。
- 真实Go未运行；47项测试含Axios wire/无Mock身份、multipart、流下载和大ID拒绝，不等于数据库事务或后端安全鉴权通过。已实现文档的全量功能覆盖仍应逐需求验收，不能以本轮接口迁移称整个阶段功能已完成。

## 2026-09-21 · 主要接口契约快速检查

- P2，已解决（发现及解决：2026-09-21）：`src/api/models.ts` 的别名建议请求曾使用 `/internal/models/{id}/aliases/suggest`，后端 `internal/api/router.go` 实际注册 `/internal/models/aliases/suggest`，真实请求会返回 404。已修正 URL；`npm run typecheck`、`npm test`（72/72）通过。剩余限制：未连真实 Go 服务验收。
- P2，已解决（发现及解决：2026-09-21）：`src/api/models.types.ts`、`src/Models.vue` 曾读取退役影响响应中的 `impact_snapshot_id`，后端 `internal/domain/model/sku.go` 响应字段实际为 `snapshot_id`，导致前端无法提交退役申请。已修正响应读取、Mock 与场景断言；请求体仍按后端 `impact_snapshot_id` 发送。`npm run typecheck`、`npm test`（72/72）通过。剩余限制：未连真实 Go 服务验收。
