# Mock 接口约定（前端暂定）

依据：详细设计 v1.1 §8.3，以及用户提供的后端 `04-models.md` 与接口通用 `README.md`（契约定稿日期 2026-09-11）。模型接口的局部对齐情况见下文；未对齐字段及其他模块仍为 `PROVISIONAL`。开发环境由 MSW 拦截 HTTP 请求；关闭 MSW 不代表所有接口已可真实使用。

开发身份仅用于 Mock 权限模拟；真实鉴权必须由后端校验，不能信任客户端角色。写操作使用 HTTP `Idempotency-Key`，客户端为同一待重试请求在 sessionStorage 保留 UUID，成功清除；不会自动重发 409。

## 2026-09-14 · 模型/报价当前调用契约（覆盖以下历史暂定记录）

依据：04-models(2).md、05-quotes(1).md；用户报告已实现，未运行Go。06—11均为未实现草案。本节描述文档覆盖的现有前端功能，不表示所有后端端点都有页面。

- 模型：列表/创建/维护、全量别名/suggest/merge、批量、上架、退役影响/提交按wire字段；上架M1:E及定价角色，publish空对象或remark。维护HTTP使用PUT /{id}，suggest返回suggestions。详情/options/人工验证仍暂定。
- 退役提交：impact_snapshot_id/sunset_date/reason及可选replacement_sku_id，理由1—500；提交PUBLISHED，通知先于双签，通知发出日至下线≥30天，generated_at不参与代算。真实双签/通知字段待核验。
- 供应商查询：GET /supplier/skus分页；GET /supplier/quotes/history按status/sku_id/from/to；GET /supplier/quotes/{id}本主体嵌套明细与decision，越权404。
- 供应商写入：POST /supplier/quotes直接APPROVING；valid_from/valid_to必填带时区，remark≤500；items按唯一sku_id聚合components。每SKU USD fx_tier八档，CNY null；constraints为整型与兼容说明；不传模型名称/币种/版本号。绝对价multiplier=null，倍率与当前官方价按1e-4容差复核。
- 导入：GET /supplier/quotes/template返回BOM CSV流，scope=history/active/vendor/family；POST /supplier/quotes/import/preview multipart file≤2MB/500行，不落库，返回token摘要/逐行OK/WARN/ERROR/preview_items；前端编辑有效行后POST /import/confirm回传完整报价DTO，全量重校验并APPROVING。不传token、currency或batchId。
- 内部查询：GET /internal/quotes/pending按supplier_id；GET /internal/quotes/{id}/diff显示三方组件价格及后端margin_preview。approve空对象，返回APPROVED_PENDING/approved_at/activate_at/immediate；reject reason10—500 rune。完成后刷新队列，不能据队列缺席推断EFFECTIVE。
- 续报复用同一提交接口创建新版本，版本按供应商由后端生成；移除未文档化的草稿PUT、/{id}/submit、/{id}/renew和旧JSON导入调用。未提交表单只在组件本地保留。
- Mock继续共享报价记录，当前浏览器不注册旧供应商报价写路由；旧Legacy处理器仅由历史迁移测试显式引用。示例大ID保留字符串，普通报价ID使用安全整数，拒绝响应中的不安全number。真实Go序列化待确认。
- Mock毛利预演明确未实现Go成本引擎，null仅用来验证页面不伪造结论；实际floor/参考价/预演依据由Go返回后验收。到期扫描/剔除、补录、静默跟随和真实worker未在本轮新增或验证。

## 2026-09-14 · 退役前端当前实现（覆盖旧两态兼容）

提交请求为impact_snapshot_id/sunset_date/reason及可选replacement_sku_id；提交响应DeprecateModelResponseDTO的lifecycle_status为PUBLISHED，双人审批完成前不迁移SKU。页面去掉DEPRECATING作为正常提交结果的分支，异常返回提示核对且勿重复提交，不自动回滚/写回生命周期。理由限制1~500 rune，Mock同步。

退役顺序显示客户通知→双人审批（第二签换人）→下线执行；通知期由服务端从任务发出日算到sunset_date≥30天。generated_at仅为报告时间，前端没有通知字段不代算。Legacy Mock的notifiedAt仍为演示通知发出时间，非法时间拒绝，不表示真实Go已验证。

HTTP Mock补retirement wire适配和退役页面必需的options/按view查询/详情，修复本轮基线40/41中的退役错误。已有Legacy change_request驳回清pending标记而不改SKU，通过新测试验证；没有新增驳回写接口或第二审批角色配置。

## 2026-09-14 · 同事退役实现说明（最新实现报告，待验证）

用户转述：原第1/2项代码已实现但文档错/漏；提交没有改变SKU生命周期；驳回仅把change_request置REJECTED，SKU保持PUBLISHED；客户通知在双人审批之前，30天从通知任务发出日算至sunset_date。第1/2项原清单未附，不能猜测其覆盖其他接口或本日志编号。

因此前面“接口文档提交即DEPRECATING”只表示文档示例冲突，不能断言实际后端违规。当前Mock保留PUBLISHED不需要按旧文档改写；无需增加驳回生命周期回滚。真实响应、SKU回查、双签迁移和通知发出时间需运行交叉验证；在验证前标记同事报告，不称已联调。

通知起算不得用generated_at、申请提交/审批通过时间，也不能默认通知任务创建就等于已发出；尚无真实通知字段名，前端不猜DTO。本次没有业务代码修改。

## 2026-09-14 · 需求优先复核（最高业务依据）

用户要求需求优先；已回读需求规格v1.4原文与详细设计v1.1。接口“已定稿”不代表可以覆盖需求。

- 普通供应商报价C8（需求L713）为采购/渠道整单审批，未明确第二审批人；与单步M4:A暂未发现人数上的明确冲突。官方涨价B4、模型退役B7、价目表发布E4则明确双人，不能混用。
- **退役接口冲突**：需求§5.1 L1452/§5.3 L1530为双人审批通过后才置DEPRECATING；模型接口§8提交申请即返回DEPRECATING。当前保留PUBLISHED的Mock在审批时点上符合需求方向，不应为接口强行提前迁移。需后端分离申请审批状态与模型生命周期。
- B7要求已上架前置与客户通知≥30天；接口允许PURCHASABLE和仅晚于今天的日期，这些放宽未获需求依据。不能直接把两份接口视为全部业务验收标准。
- 报价草稿阶段在需求状态图中存在；接口未提供草稿持久化方法，需确认交互/落库范围，不能简单删去需求流程。

以下按接口核对及更早暂定记录保留历史；与本节冲突的结论不再作为实施方案。

## 2026-09-14 · 已实现接口文档核对（优先于此前暂定说明）

收到用户提供的04-models(2).md与05-quotes(1).md；用户报告接口已实现，已确认文档但尚未运行Go验证。下列当前代码差异未修复，不能把此前测试通过当作新契约验收。

- 模型退役：返回DEPRECATING，允许PUBLISHED/PURCHASABLE，理由1~500。当前Mock审批优先PUBLISHED与至1000字限制需改；上架使用M1:E（定价运营），M1:P为前端暂定错误，需移除。模型options/detail/verify不在本次文档中，不能宣称已经实现。
- 报价：单步M4:A，明确不使用approval_step。此前stepId与多级测试是暂定扩展，当前不应要求后端实现。
- 报价POST直接提交进入APPROVING；请求valid_from/valid_to，items按sku_id聚合components，USD带fx_tier，不传模型名称和币种。当前保存草稿/PUT/续报/submit方法没有文档支持。
- 查询：可报价SKU /supplier/skus、历史 /supplier/quotes/history、内部待批 /internal/quotes/pending、差异 /internal/quotes/{id}/diff。当前路径和DTO需迁移。
- 导入：/supplier/quotes/template下载CSV，multipart /import/preview，/import/confirm传完整报价请求及编辑后的preview_items；当前JSON校验/batchId提交不同。
- 新文档不覆盖认证、资质上传、价目表options；敏感凭证轮换仍须其他契约。preview.token是文件SHA256摘要，不是鉴权Token。

差异及状态见ISSUES.md第3/4/5/9/22/23项。

## 2026-09-14 修订与待确认边界（历史暂定记录）

以下是当前实现，覆盖本文较早记录中与之冲突的描述；不等于 Go 后端已接受新契约。

- 价目表选项：新增暂定 `GET /api/internal/price-books/options`，返回 PriceBookDraftItem[]，包含稳定 SKU ID、币种、单位和成本依据。页面不再内置 SKU/floor；Mock 保存与提交均重新取当前选项，客户端不得覆盖 floor。真实后端也必须做权威校验。
- 模型权限：页面读取权限码，不依赖 dev identity。上架暂定独立 `M1:P`；不能把已有用于模型申请审批的 `M1:A` 当作上架权限。权限码及分配需后端确认。
- 报价审批：详情可返回 approvalSteps/currentStepId，动作可返回 APPROVING；请求带当前 stepId。Mock 支持串行两步场景及旧单步默认，校验当前处理人和过期步骤；供应商视图仅投影进度。未实现会签、或签、转交和通用流程配置器，节点 ID 与字段名仍暂定。
- 敏感展示：审计、集成诊断、官方价格详情及错误文本先脱敏再供页面使用，审计导出也脱敏。仅使用人工构造测试凭证验证；不声称正则能识别任意自由文本秘密。真实后端必须减少响应字段并处理凭证生命周期。
- 幂等键仅存指纹和随机 ID，不存完整请求体；未知结果保留，不通过客户端过期时间擅自作废。后端幂等记录保留期、重放范围和状态查询仍需约定。

## 已有概要路径

前缀均为 `/api/internal/models`。

| 方法与路径                     | 当前输入                                   | 当前结果                                                          |
| ------------------------------ | ------------------------------------------ | ----------------------------------------------------------------- |
| GET 根路径                     | `view/keyword/vendor_id/family_id/model_type/lifecycle_status/tier_tag/page/size` | `{list,total,page,size}`；SKU snake_case DTO或系列项，列表不返回官方价/成本/协议/验证历史 |
| GET `/options`                 | 无；PROVISIONAL                           | `{vendors:[{id,name}],families:[{id,vendor_id,name}]}`；完整 Mock 选项，不由当前页推导 |
| GET `/{id}`                    | 路径 ID；PROVISIONAL                       | `{sku: ModelSkuContractDTO, mock_extensions?: {protocol, verification, pending_retirement}}`；不存在或不可见404，扩展不含成本/金额 |
| POST 根路径                    | CreateModelSkuRequest，snake_case          | 完整 ModelSkuContractDTO；新建为 DRAFT                            |
| PUT `/{id}`                    | UpdateModelSkuRequest，不带迁移ID或状态    | 完整 ModelSkuContractDTO；不直接改生命周期状态                    |
| POST `/{id}/aliases`           | `{aliases: string[], source: 'MANUAL'}`    | 全量覆盖别名；先完整校验，失败不改变旧数据                         |
| GET `/{id}/aliases/suggest`    | `keyword`                                 | `{suggestions:[{sku_id,sku_code,score}]}`；页面显示候选 SKU 编码       |
| POST `/aliases/merge`          | `{target_sku_id, source_sku_id, alias}`     | `{target_sku_id, merged_sku_id, alias_id, alias}`；Mock仅登记未占用别名，保留来源生命周期 |
| POST `/batch`                  | `{sku_ids, action: 'SET_TIER' 或 'SUBMIT_VERIFY', payload?}` | `{total,succeeded,failed:[{sku_id,code,message}]}`；API 适配为逐项展示 |
| POST `/{id}/publish`           | `{}`                                       | `{sku_id,lifecycle_status,published_at}`；定价运营、可采购状态才能上架 |
| GET `/{id}/deprecation-impact` | 无 | ModelDeprecationImpactDTO：impact_snapshot_id、sku_id、references、replacements、generated_at |
| POST `/{id}/deprecate` | `{impact_snapshot_id, sunset_date, reason}` | sku_id、approval_id、sunset_date、lifecycle_status=PUBLISHED；提交不改SKU，真实响应待验证 |

## 暂定扩展，不是正式新增后端要求

### 客户门户六页（2026-09-13）

以下统一为PROVISIONAL，不代表正式Go接口定稿：

| 路径 | 请求/响应及边界 |
| --- | --- |
| GET `/api/customer/home` | 客户名、对客报价/合同/通知数量、账户概况；桌面聚合接口待确认 |
| GET `/api/customer/price-book` | page/size/search/status → 当前主体价目表列表，详情含组件售价/币种/单位/生效时间；等级价/合同优先规则未实现 |
| GET `/api/customer/quotes` | 同上 → 当前主体非草稿报价，详情包含validFrom/validTo、canAccept与acceptedAt；不返回内部原因、成本、floor、参考价或编辑权限 |
| POST `/api/customer/quotes/{id}/accept` | 空对象、Idempotency-Key → {quoteId,acceptedAt}；Mock仅接受有效期内尚未接受的FORMAL，TEMP规则待确认；保存客户确认回执，不转合同或改报价状态 |
| GET `/api/customer/contracts` | 同上 → 明确的Mock合同快照；独立桌面合同入口为暂定扩展，正式接口是否归入quotes待确认 |
| GET `/api/customer/billing` | 同上 → {list,total,page,size,account}；账单与余额/授信/押金只读样例，不付款/充值/核算 |
| GET `/api/customer/notifications` | 同上 → 通知正文样例；不实现未确认的已读及计数变更 |
| GET `/api/customer/{section}/{id}` | 独立详情暂定扩展；未知对象/非本主体404，非客户身份403 |

- 客户DTO独立于内部报价DTO；Mock使用显式字段投影。只模拟蓝海电商主体，X-Mock-Identity只作开发授权，不代表真实认证/DataScope。
- 内部与客户报价读取共享customerQuoteRecords；DRAFT不可对客见。新增一张明确标记的蓝海FORMAL开发样例供接受验收，不表示内部草稿已经正式化。接受回执单独保存在Mock内存，内部回执展示尚未接入。
- 400：分页/接受请求不合法或缺幂等键；403：非客户身份；404：对象不可见；409：报价不允许接受/已接受/Key冲突。未知响应失败由统一Axios处理。接受重放权限及归属检查在前，不因缓存越权。
- 账单、通知、合同快照和价目表均为独立明确样例，不声称与上游变更、真实计费或合同取价自动同步。整页刷新重置Mock数据与接受回执。
- 待后端确认：正式字段/分页/状态、客户主体映射、可见报价规则、TEMP接受/合同转换、接受回执展示及重复接受语义、账单透传、通知正文/已读协议。未新增充值付款接口。

| 路径                    | 用途与正式接入处理                                                                                          |
| ----------------------- | ----------------------------------------------------------------------------------------------------------- |
| POST `/{id}/verify`     | 独立演示人工验证；设计将验证记录归入创建／维护，接入时需确认是否合入 PUT                                    |
| GET `/retirements`      | Mock 内查询记录的预留入口，页面未使用；正式审批查询路径待确认                                               |

详情GET为PROVISIONAL的{sku,mock_extensions?}：基础字段复用SKU，协议/验证历史/退役申请单独标注Mock扩展。列表、创建和维护已经使用后端 SKU wire DTO，不把未定义的官方价/成本/协议字段补进正式响应。Legacy Draft 仅保留给旧业务层测试兼容，不再是页面 HTTP 保存请求。

人工验证仍为PROVISIONAL POST：仅接受result/字符串note，说明去空白后1~1000字为Mock暂定限制，未知字段和非POST拒绝。通过仍由Mock转为PURCHASABLE、不通过保留PENDING_VERIFY，不自动上架；上架只允许POST空对象且由接口校验权限/当前状态。两者均确认后固定请求、禁止重复发送、成功后重新查询；同幂等键重放不重复写入，已保存后查询失败不提示重新提交。

退役 API 和页面已使用 ModelDeprecationImpactDTO / DeprecateModelRequest 的线缆字段；旧 impactId/offlineAt 仅保留在 Legacy Mock 内部。Mock 适配成 references/replacements，引用实体仍为演示数据，不可视为真实依赖关系。页面不把 generated_at 当作通知起点。模型提交申请后是否立刻进入 DEPRECATING 尚待确认：Mock 保留原审批优先的 PUBLISHED；页面接受服务端两种结果并显示实际返回状态，不自行改状态。replacement_sku_id 尚未开放，Mock 显式拒绝，不静默忽略。

## 校验与错误

- 403：角色不允许；404：模型或接口不存在；405：禁止 GET 写入。
- 409：重复标识／别名、状态冲突、幂等键语义冲突。不会盲目重试。
- 422：缺少必填字段、缺报告、Mock 日期校验不通过。
- 503：模拟瞬时失败；数据不变，用户可重试。
- 批量接口返回每一项结果，不把部分失败显示为全部成功。普通写操作按钮在等待期间禁用。

只有完整成功的幂等请求结果会被 Mock 缓存；其有效期为当前页面内存生命周期。真实后端需要独立实现持久幂等、并发冲突、对象级授权以及最终校验。

## 后续对齐清单

查询排序、分页上限、完整字段约束、财务字段可见范围、验证状态转换、允许的批量转换、别名删除与合并语义、通知时间来源、退役第二审批人和删除规则。现阶段不增加生产数据库或外部接口。

## 后端模型契约纳入与局部对齐（2026-09-13）

来源：用户提供的 `04-models.md`（阶段 4 M1）和同目录接口 `README.md`。只将它们作为接口依据，不将其中“唯一契约源头”等描述视为覆盖本项目需求/详细设计优先级的新指令。

- 已纳入 `models.types.ts`：SKU snake_case DTO、固定 capability key、列表 Query、创建/维护请求、查重/合并、批量部分成功、退役影响/提交和上架响应类型。旧页面 Model/Draft 与 wire DTO 分开，未使用交叉类型假装字段相同。
- 已对齐：编辑路径为 PUT /models/{id}，创建/维护请求及完整SKU响应已采用文档字段；别名 POST 为全量 aliases 覆盖；查重 GET 为 /{id}/aliases/suggest?keyword=，返回 suggestions 包。模型 Mock 错误使用数值 code、data:null，并使响应头 X-Request-Id 与响应体 requestId 一致。
- 通用 Axios：409/code 10005 保留原幂等 Key，由用户主动重试，不自动重发；423 默认提示冻结并联系管理员。校验后为“门户＋用户 ID＋作用域＋请求体”的 SHA-256 指纹生成 UUID；字段变化使用另一 Key，但保留旧未知结果的 Key，改回原请求仍可重试。成功或确定性失败清除对应 Key，不清除其他待定操作。
- 列表已改用文档 Query 和 SKU snake_case DTO；固定 capability 对象映射为展示标签，null 上下文/能力明确提示未提供，名称位置展示 SKU 编码。创建/维护使用厂商/系列ID、原厂币种、固定能力对象，不传名称或协议。详情GET文档未定义，采用明确标注PROVISIONAL的SKU/Mock扩展分离结构；人工验证、完整合并及退役仍有暂定行为，不能标记真实可用。
- 待后端补充：厂商/系列选项与归属查询；view=family 系列项及精简 children 的完整结构；GET /models/{id} 详情、人工验证接口；model_type/tier_tag 枚举全集；已有界面官方价/协议/验证历史的数据来源。列表默认 page=1,size=20，上限100，keyword 1~64 已记录在后端 Query 类型关联说明。
- int64：文档示例为 JSON number，当前前端 ID 为字符串且 Mock 样例超过 JS 安全整数。编码是否统一返回字符串待确认；类型暂用 number|string，查重响应中的不安全 number 明确拒绝，不转 Number(ID)。
- 合并请求和响应已对齐文档；当前模型作为来源，查重候选作为保留目标，确认后提交两个实际ID。Mock仅登记未占用别名，不删除来源或改变生命周期；来源编码作为别名会触发全局唯一检查。来源编码迁移、供应商回填及合并后的生命周期仍待确认，不能声称完成真实SKU合并。旧targetId格式仅保留给历史业务层测试，HTTP入口拒绝旧格式。
- 批量：已对齐后端 sku_ids/action/payload 和 {total,succeeded,failed}。API 将失败清单映射回提交的 ID，页面用本次所选模型编码显示结果；数量不一致、重复/非本批 ID、不安全数值 ID 或失败信息缺失明确报响应错误，提示刷新核对，不假定整批成功。页面与 Mock 限制1~200且不可重复，未开放 ADD_TAG/REMOVE_TAG。
- 上架：响应已改为 {sku_id,lifecycle_status,published_at}，页面只刷新服务端数据；Mock 的 published_at 在首次操作时生成并缓存，同 Key 重放不重新生成时间。
- 退役冲突：后端响应直接 DEPRECATING，原需求规定审批流程权威控制；当前演示保持原已上架状态+待审批标识。未改变状态迁移，需需求方/后端确认。影响接口第7节示例缺 impact_snapshot_id，但第10节定稿明确返回，类型依定稿定义为必填；有效期/expires_at 响应字段仍待确认。
- 全量覆盖不等于并发安全：别名写入缺版本/ETag 契约，跨用户覆盖保护仍需后端补充。

### 模型列表 Mock 方案（PROVISIONAL 部分明确隔离）

- `view=sku` 的 total 与 page/size 以 SKU 为单位；`view=family` 以匹配系列为单位，先筛选再按系列分页，children 包含该系列符合筛选的 SKU，避免一个系列被拆进多个 SKU 页。筛选后的 sku_count 为匹配的子项数。
- 文档未给出完整系列 schema：Mock 使用 `{id,family_name,vendor_id,vendor_name,sku_count,children}`，children 暂为完整 SKU DTO，不声称这是后端最终精简 children 定义。
- `/options` 是并行开发的暂定选项接口，返回全部 Mock 厂商与系列及归属；ID 在当前 Mock 进程内稳定，新记录沿用既有分配。没有写死页面选项或从当前页推导查询选项；最终接口待后端确认。
- 单次 Query 只接受文档列出的字段；默认 view=family,page=1,size=20，非法页码/数量/关键字/ID/状态/分级返回400。原 capability 筛选改为文档 tier_tag 筛选，未增加未定义 Query。
- 列表不返回官方金额、成本、margin、protocol、verification；详情仍是明确标记的旧 Mock 数据。导出只包含当前加载/选择的列表字段，不补价零值。
- 所有金额仍为字符串，SKU int64 样例继续字符串编码防止精度损失。生产环境的真实权限与 ID 编码仍需后端验证。

### 模型创建/维护 Mock 方案

- 页面选择厂商/归属系列并发送ID，编辑禁用厂商/系列选择且PUT体不带这两个ID。SKU编码1~128，原厂币种三位大写，上下文非负整数，可选能力仅允许八个布尔key及max_output_tokens非负整数/null。
- 前端复用结构校验作交互提示；Mock服务端仍执行结构、对象归属、SKU/别名全局唯一、权限和可维护状态校验，全部通过后才写入同一模型记录。未知key（含lifecycle_status、价格、name、protocol）拒绝，不允许借维护迁移或改状态。
- 保存前二次确认，确认期间表单锁定且使用固定请求快照；成功后重新查询列表。编辑直接使用列表返回的原能力对象，不从中文能力标签反推，保留最大输出长度。既有别名未在表单编辑时省略aliases，后端保留原别名。
- 创建省略context_window/capability/tier_tag时响应对应null；维护省略字段时保留原值。文档没有定义清空已有上下文/分级的请求语义，页面不擅自传null清空；最大输出长度可按明确契约传null。
- model_type完整枚举仍未给出：Mock暂定对话/推理/多模态/向量/嵌入/图像/语音，页面明确提示，等待后端枚举。`capability`采用文档§10定稿，不采用§2遗留“结构待定”描述。
- Legacy模型业务层为兼容其他演示流程仍有名称/协议及零金额占位；创建/维护/列表的SKU响应不返回这些字段，它们不是实际官方价格或计算依据。旧详情请求仍为PROVISIONAL，真实保存/权限/并发/ID编码尚未联调。

## 供应商报价（PROVISIONAL）

- `GET /api/internal/quotes`：分页查询，当前支持 `search`、`supplierId`、`status`。
- `GET /api/internal/quotes/{id}`：返回供应商、SKU 四列价格对比、供给约束、毛利预演、告警和可执行动作。
- `POST /api/internal/quotes/{id}/approve`：整单通过，可选意见；只从 `APPROVING` 进入 `APPROVED_PENDING`。
- `POST /api/internal/quotes/{id}/reject`：整单驳回，原因必填；进入 `REJECTED`，保留原版本。
- 审批接口使用 `Idempotency-Key`；材料未就绪、无权、已被处理分别返回可理解的错误，不允许逐行改价。

## 官方价格同步（PROVISIONAL）

- `GET /api/internal/official-prices/jobs`：分页查看采集与差异比对任务；本阶段不开放页面重跑。
- `GET /api/internal/official-prices/staging`、`GET /api/internal/official-prices/staging/{id}`：分页查询暂存价格并查看来源证据。
- `POST /api/internal/official-prices/staging/{id}/confirm`、`/discard`：模型运营确认来源或丢弃，均使用 `Idempotency-Key`；确认后生成变更单。
- `GET /api/internal/official-prices/changes`、`GET /api/internal/official-prices/changes/{id}`：分页查询官方价格变更和审批步骤。
- `POST /api/internal/official-prices/changes/{id}/approve`、`/reject`：完成当前审批步骤，使用 `Idempotency-Key`。
- 涨价 Mock 为模型运营、定价运营两级审批，降价为模型运营一级审批；正式分级规则待后端确认。
- 审批全部完成只进入 `APPROVED`（已批准待生效）；页面不提供 `EFFECTIVE` 转换接口，真正激活由后端任务负责。

## 内部 Supplier Profile 查询（已按 Go 契约接线，待实库验收）

- `GET /api/internal/suppliers`：`keyword/status/qual_status/page/size`，返回 `list/total/page/size`，关键词匹配真实主体名称；统计为当前有效报价和去重 SKU，不虚构供应商编码。
- `GET /api/internal/suppliers/{id}`：真实档案、归属采购、结算币种（CNY/USD）及按权限剔除的商务字段；不存在 404，存在但越域 403。详情不返回联系人、附件、资质审核材料或供给区域等无接口来源的展示。
- 两接口均只读，复用 M3:V 和既有数据范围；财务或档案归属采购可见结算商务字段，其他人员响应中没有这些键。Mock 仅模拟该查询契约，不能替代 v28 迁移和真实数据库验收；Supplier Channel/URL/Domain/IP/Key 不在本轮范围。

## 供应商门户（PROVISIONAL）

- `GET /api/supplier/home`：返回当前供应商的报价指标、待办、资质概况和通知。
- `GET /api/supplier/quote-options`：返回当前主体可报价的 SKU/组件、币种、单位、税费口径和上次供应价；不返回内部官方价、成本或毛利数据。
- `GET /api/supplier/quotes`、`GET /api/supplier/quotes/{id}`：只返回当前主体报价，不包含内部成本对比、毛利预演、最优价或审批动作字段。
- `POST /api/supplier/quotes`：创建 `DRAFT`；校验报价项属于当前可报价清单、同一 SKU 组件不重复、金额大于 0 且最多 8 位小数、结束时间晚于生效时间；写操作使用 `Idempotency-Key`。
- `PUT /api/supplier/quotes/{id}`：仅允许当前主体修改 `DRAFT`，复用创建请求 DTO 和完整校验；保留报价 ID、编号及版本，更新价格项和时间；使用 `Idempotency-Key`，已提交返回 409，无权返回 403，对象不存在或不属于当前主体返回 404。正式接口及并发版本控制仍待确认。
- `POST /api/supplier/quotes/{id}/submit`：仅允许本主体的 `DRAFT` 提交。当前 Mock 在服务端接受提交后同步初始化审批流，因此响应状态为 `APPROVING`，不是页面直接改状态；正式后端采用同步还是异步初始化仍待确认。
- `GET /api/supplier/quotes/import/template`：返回最新 CSV 模板文件名与内容；真实接入时是否改为文件流待确认。
- `POST /api/supplier/quotes/import/validate`：上传文件名与 CSV 内容，返回预检批次和逐行错误；真实上传媒体类型待确认。
- `POST /api/supplier/quotes/import/commit`：使用预检批次和 `Idempotency-Key` 生成一份 `DRAFT`；存在错误行时整批拒绝。
- 供应商和内部采购接口在 Mock 中读取同一份报价记录：供应商提交后内部可见，内部通过后状态为 `APPROVED_PENDING`，驳回原因也会回显；不会模拟为已生效。
- 当前 Mock 依据开发供应商身份模拟数据隔离；供应商响应通过独立 DTO 投影剔除内部对比与审批字段。真实环境必须由后端根据 Token 中的主体、DataScope 和对象归属做强制校验。

## 供应商新模型申请（PROVISIONAL）

- `GET /api/supplier/model-applications`、`GET /api/supplier/model-applications/{id}`：分页筛选当前供应商申请，查看申请资料、进度、审核意见和驳回原因。
- `POST /api/supplier/model-applications`：新建 `DRAFT`，保存厂商、模型标识、官方资料、能力和申请原因；使用 `Idempotency-Key`。
- `POST /api/supplier/model-applications/{id}/submit`：仅允许本主体的 `DRAFT → SUBMITTED`；内部审核、驳回、模型入库、验证和发布接口待后端确认。
- 页面不把 `APPROVED` 解释为模型已上架；模型主数据状态由内部后端流程权威决定。

## 供应商档案与资质（PROVISIONAL）

- `GET /api/supplier/profile`：返回当前供应商主体、联系、服务区域与结算概况。
- `PUT /api/supplier/profile`：仅更新联系人、手机、邮箱、联系地址和服务区域；法定主体、登记信息、结算币种与账期不在请求 DTO 中。
- `GET /api/supplier/qualifications`：返回当前主体的资质材料、有效期、附件名、审核状态和意见。
- `POST /api/supplier/qualifications`：使用 `Idempotency-Key` 提交材料并进入 `SUBMITTED`；当前仅登记附件名，正式文件上传、扫描、存储凭证和审核流待确认。

## 供应商结算与对账（PROVISIONAL）

- `GET /api/supplier/reconciliation`：分页查询当前供应商对账单，支持编号、周期和状态筛选。
- `GET /api/supplier/reconciliation/{id}`：返回应付金额、调整、税额、脱敏结算账户、票据状态和逐 SKU 明细。
- 所有金额和用量为十进制字符串；页面不使用 JS 浮点数重算合计。
- 当前只读；对账确认、差异申诉、发票、付款和结算状态更新均由后端或财务系统权威处理。

## 告警与审计（PROVISIONAL）

- `GET /api/internal/alerts`、`GET /api/internal/alerts/{id}`：分页筛选与详情。
- `POST /api/internal/alerts/{id}/acknowledge`、`POST /api/internal/alerts/{id}/resolve`：确认和解决，解决原因必填，均使用幂等键。
- `GET /api/internal/audit-logs`、`GET /api/internal/audit-logs/{id}`：只读查询服务端保存的前后值、来源和影响。
- `GET /api/internal/audit-logs/export`：开发阶段返回按当前筛选生成的 CSV 内容；正式环境的文件流、异步任务和下载权限仍待确认。

## 成本与价目表（PROVISIONAL）

- `GET /api/internal/cost/baselines`：分页查询当前成本基线。
- `GET /api/internal/cost/baselines/{skuId}`：完全成本、组成项、参数/汇率快照、主供应商及重算任务。
- `GET /api/internal/cost/baselines/{skuId}/compare`：服务端标准化的多供应商价格对比与选主理由。
- `GET /api/internal/price-books`、`GET /api/internal/price-books/{id}`：价目表版本和 SKU 组件售价详情。
- `POST /api/internal/price-books`、`PUT /api/internal/price-books/{id}`：创建和更新草稿，金额为十进制字符串。
- `POST /api/internal/price-books/{id}/submit`：仅允许 `DRAFT → APPROVING`；必须有计划生效时间且所有 SKU 通过服务端最新 floor 校验。
- 页面不得直接改写成本或将待生效版本改为生效；生产环境中的成本重算、价目表审批和激活均由后端负责。

## 成本管理（PROVISIONAL）

- `GET /api/internal/cost/baselines`：成本基线分页查询，支持 `search`、`supplierId`、`status`。
- `GET /api/internal/cost/baselines/{skuId}`：返回当前已保存基线、完全成本组成、主供应商、来源报价、汇率和参数快照。
- `GET /api/internal/cost/baselines/{skuId}/compare`：返回同口径供应商报价、标准化成本、资质、供给状态及服务端选主结果。
- `RECALCULATING` 只表示后端任务正在生成新版本，页面仍展示当前已保存基线，不自行覆盖成本。
- 本阶段不开放锁主供应商、损耗、通道费、税项和汇率编辑；相应权限、评分规则与写接口待业务和后端确认。

## 定价策略（PROVISIONAL）

- `GET /api/internal/pricing/policies`、`GET /api/internal/pricing/policies/{id}`：分页筛选定价策略并查询详情。
- `POST /api/internal/pricing/policies`、`PUT /api/internal/pricing/policies/{id}`：创建和更新草稿；写操作使用 `Idempotency-Key`。
- 当前只开放目标毛利率 `TARGET_MARGIN` 与官方价锚定 `OFFICIAL_ANCHOR` 两类基础策略，金额、比例和舍入步长均使用十进制字符串。
- 只有 `DRAFT` 可编辑；`ACTIVE`、`INACTIVE` 在页面保持只读，前端不自行切换状态或生成权威售价。
- 策略优先级、舍入执行顺序、红线校验、审批与激活接口仍待业务和后端确认。

## 客户与客户报价（PROVISIONAL）

- `GET /api/internal/customers`、`GET /api/internal/customers/{id}`：分页查询客户并返回脱敏主体信息、当前价目表和合同概况。
- `GET /api/internal/customer-quotes`、`GET /api/internal/customer-quotes/{id}`：分页查询客户报价版本和 SKU 报价详情。
- `POST /api/internal/customer-quotes`、`PUT /api/internal/customer-quotes/{id}`：创建和更新客户报价草稿，使用 `Idempotency-Key`；只有 `DRAFT` 可更新。
- 金额字段均为十进制字符串；前端仅提示低于 floor，保存和后续状态转换仍由后端重新校验。
- 页面不实现 `DRAFT → TEMP/FORMAL/CONTRACT`；正式化、审批、客户确认、合同转换和过期任务接口待确认。
- 冻结或停用客户是否允许报价由后端决定；当前 Mock 拒绝新建和更新，不以隐藏按钮替代安全校验。

## 财务信息（PROVISIONAL）

- `GET /api/internal/finance/fx-rates`：分页查询月度汇率快照，支持币种/来源搜索、月份和状态筛选。
- `GET /api/internal/finance/accounts`、`GET /api/internal/finance/accounts/{customerId}`：查询客户授信、押金、结算条件和近期变动。
- 所有金额和汇率均为十进制字符串；页面只展示服务端返回的额度、占用和余额，不用前端浮点数重算。
- 本阶段不定义汇率锁定、授信调整、冻结解除、押金入账/退回等写接口。
- 正式接口需确认财务数据域、字段物理剔除、预警阈值和账户冻结规则。

## 内部工作台（PROVISIONAL）

- `GET /api/internal/workbench`：返回当前登录身份可见的关键指标、待办和业务提醒。
- 汇总结果必须由后端按权限与数据域生成；前端只负责展示，并再次隐藏无路由权限的快捷入口。
- 待办跳转使用已登记的内部路由；工作台不直接执行审批、锁定或状态迁移。
- 指标口径、缓存时效和刷新频率仍待后端确认，当前 Mock 的数量仅用于界面验证。

## 组织与权限（PROVISIONAL）

- `GET /api/internal/org-permissions/organizations`：返回组织树、组织类型、成员数量和负责人。
- `GET /api/internal/org-permissions/roles`：返回角色、Portal、数据域、功能权限编码和字段限制说明。
- 当前页面只读，不定义组织新增、人员分配、角色授权或字段策略修改接口。
- Router Guard 只消费路由权限；`SELF`、`DEPT`、`DEPT_SUB`、`ALL` 和字段物理剔除必须由后端执行。
- 正式接口需确认组织数据来源、角色继承、数据域语义、字段策略格式和变更审计要求。

## 系统对接（PROVISIONAL）

- `GET /api/internal/integration/overview`：返回开发环境中的接口通道、同步任务、业务事件和 MCP 能力状态。
- 状态只表示服务端返回的观测结果；页面不主动探测生产地址，也不把 MSW 响应标记为真实连通。
- 本阶段不定义任务重跑、密钥管理、事件重放或 MCP 启停接口。
- 生产写操作必须使用真实调用方身份、权限校验、幂等键和审计，不信任客户端角色。
- 正式接口需确认健康检查来源、任务运行记录、事件契约版本、MCP 授权与敏感信息剔除规则。
## 2026-09-13 供应商申请审核与客户回执（PROVISIONAL）

- `GET /api/internal/supplier-reviews/{kind}`：kind为qualifications或model-applications；page、size、search、status；返回list和total。
- `GET /api/internal/supplier-reviews/{kind}/{id}`：返回申请资料、供应商名称、状态和本轮处理记录。当前共享记录均属于开发主体云桥科技，不代表已实现真实多供应商数据域。
- `POST /api/internal/supplier-reviews/{kind}/{id}/review`：Idempotency-Key必填；请求result为APPROVED或REJECTED，reason为最多300字字符串，驳回必填；返回更新后详情。仅SUBMITTED/REVIEWING允许处理；重复Key同内容回放，不同内容409；已处理记录新请求409。权限暂沿用模型运营M3:A（资质）/M1:A（模型申请），正式编码、长度限制、审核状态流程待确认。
- 共享Mock记录使供应商查询可看到结果及原因；模型审核不创建模型、不跳过模型验证与上架；资质审核不自动修改整个供应商的资质或冻结状态。历史种子只有已有审核意见，本轮处理记录单独展示，不伪造历史操作人。
- 客户接受回执存于共享Map；客户与内部报价详情读取同一回执，内部详情新增acceptedAt（未接受时字段不存在），不修改报价状态、最终取价或合同。权限仍由Mock处理器校验；整页刷新重置。
- 价目表需求v1.4场景7明确双人审批，第二审批人为财务/上级；现有财务权限表仅M7查看。第二审批人授权契约、自审限制、驳回后的价目表状态尚缺。不可直接复用M7:E，也不可假设单人M7:A审批即完成；审批处理接口暂不定义，自动激活继续留给后端。
## 2026-09-14 基础功能补齐（PROVISIONAL）

- CSV模板支持scope=HISTORY（默认，本主体历史且仍可报价的SKU）/ALL、search；自动预填当前选项元数据、上次供应价和明日时间，保持既有9列。不会输出其他供应商价格或平台成本。仍不含官方价只读列、倍率/Excel导入。界面锁定文件读取、预检及确认提交，并绑定本次文件与预检批次快照。
- 普通供应商草稿提交时重新验证SKU、倍率基准及时间；若结束时间已过则409且不迁移状态；过去的计划生效时间钳制为提交时刻并记录内部警示。幂等回放不重新更新时间。特权历史补录未实现，审批通过仍仅待生效。

- 可报价选项新增officialPrice和basisVersion（明确Mock版本）。价格项支持pricingMode=ABSOLUTE/MULTIPLIER；倍率模式额外保存multiplier字符串及basisVersion。界面支持逐组件及整单倍率，绝对价对应倍率只作约值预览。接口使用Decimal独立64位精度验证官方价×倍率等于供应价；倍率最多20位整数/8位小数，精确结果超过8位小数拒绝、不自动舍入，正式规则待确认。基准版本不符409，换算不一致400；两端查询可回显模式/倍率。未实现官方调价后的静默跟随版本，不代表汇率选择、缓存维度或完整倍率生命周期完成。

- `GET/PUT /api/internal/cost/baselines/{skuId}/parameters`：GET沿用M5查看；PUT暂沿用定价运营M5:E。保存lossRate/channelFeeRate（0~100的百分数字符串，最多8位小数）、reason（必填最多300字）、revision（当前配置修订号）；Idempotency-Key必填。返回独立配置与修改人/时间，新Key旧revision409；重复Key同内容回放。SKU配置不覆盖历史基线、不自动触发或伪造重算完成，不涉及汇率、税项或供应商级覆盖。范围及精度限制为Mock约定待后端确认。

- `POST /api/supplier/quotes/{id}/renew`：请求沿用报价草稿字段，支持可选supplyConstraints；Idempotency-Key必填。创建独立DRAFT，previousQuoteId指向来源，保留quoteNo，version取本供应商同编号现有最大版本+1。来源仅允许EFFECTIVE/EXPIRED/VOIDED/REJECTED，本主体校验；正式续报状态、版本编号策略待后端确认。编辑新草稿保留前版对比价格；旧报价不修改、不自动关闭或覆盖。
- supplyConstraints整单适用：maxConcurrency/rpm/tpm/actualContextWindow为可选正整数字符串（最多10位，Mock暂定限制），compatibilityNote最多300字。留空表示未声明，不表示无限供给。内部详情同时展示结构化约束和已有对比表；没有配额单位与逐SKU约束，不自行增加解释。
- `GET /api/internal/customer-quotes/template?customerId=...`：限现有报价编辑权限；读取客户已分配的确切priceBookCode及EFFECTIVE记录，返回priceBookCode/name/items。未配置或未生效409，冻结客户423；金额字符串，当前只支持INPUT/OUTPUT。新增明确的蓝海GOLD价目表开发样例，与内部价目表查询共享记录。
- 创建/保存请求可带priceBookCode：保存时核对客户仍引用该表并重取SKU元数据、referencePrice和floorPrice；已移除SKU或分配变化409。不声称实现特价审批或正式化时的完整floor红线。
- `POST /api/internal/customer-quotes/{id}/clone`：请求沿用CustomerQuoteDraft，Idempotency-Key必填。创建同客户新DRAFT，previousQuoteId记录来源，不继承validFrom、接受时间、合同状态；截止时间若提供必须晚于当前时间。重复Key回放，不同内容409，换客户400。已有接受动作不受克隆影响。
- `GET /api/internal/official-prices/manual-options`和`POST /api/internal/official-prices/manual`：模型运营M2:E临时约定，options仅包含3个现有价格基准开发SKU。请求modelId/inputPrice/outputPrice/sourceUrl/note；Idempotency-Key必填。金额为非负十进制字符串（最多8位小数），链接限HTTP/HTTPS且不带用户名密码，依据必填最多500字。返回{id,status:PENDING}，随后查询暂存详情。
- 手工暂存保存baseline输入/输出快照，confidence=0（未自动验证），币种/单位由选项决定。确认时输入或输出任一上涨均PRICE_UP双签；两者未变409且不修改处理信息。仅模拟既有价格基准，不假装抓取、激活、重算、通知或真实存库完成。
