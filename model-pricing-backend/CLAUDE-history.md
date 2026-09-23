# CLAUDE-history.md — model_bss 阶段 0～7 完成日志（归档）

> 本文件是 `CLAUDE.md` 第九节「当前进度」中阶段 0～7 的原始详细日志，
> 因 CLAUDE.md 超过 40k 字符上限（claude CLI 截断告警）于 2026-09-17 拆分归档。
> 
> **CLAUDE.md 里只保留速览与遗留索引；这句话以下的内容全文未动、行序未变。**
> 回查方式：按阶段编号（如「6d-3」「7b-②」）在本文件内搜索即可定位原始断言与实测值。

---
## 九、当前进度

- [x] 0 项目骨架 — commit 253e41d `chore: project skeleton`
  - [x] 0+ 统一响应包与错误码地基 — commit 6ea7d24（pkg/response、pkg/apperr 对齐 §8.0）
- [x] 1 数据库迁移 — commit dba66f0（7 个迁移组、45+3 张表；部分唯一索引/EXCLUDE gist 全部生效，13 约束已验证；request_id 与审计字段 P0 补全）
  - 000001_account / 000002_subject / 000003_model / 000004_quote_cost / 000005_pricing / 000006_flow_audit / 000007_seed
- [x] 2 鉴权与权限中间件 — 登录/会话/AuthN/AuthZ/DataScope/字段剔除/CORS 全部落地并通过 E2E（200/401/403/429）；
      commits: 491c1c0, 3eb80fa, b1d4ade, e1355fd, 6925d54, 49577de, 63b7f6b, 5d4ff65, feaf1ac
  - [x] 2+ 接口文档 — swagger 注解 + openapi/swagger.json|yaml（前端契约），commit 118ec76
- [x] 3 幂等中间件 — commit 156031c/ba5804d/1517a45/dfb6765/cdfb6b5/6b26e43（中间件+状态机+真实 GORM 仓储+
      PROCESSING 卡死防护+8 条单测；E2E 实测同 key 不同 body→400、重放不重跑业务）
- [x] 4 模型管理 M1 — 4a(66065a4…ba72090) + 4b-1(786abd8…708cf2a, ae785a8) + 4b-2(3cb29ac/7647b8a/1ada520/7a7208d)
      接口：列表(双视图)/创建/维护/别名全量覆盖/查重建议/一键合并/批量(部分成功)/上架/退役影响分析/发起退役/审批动作
      数据字典与契约：docs/api/README.md + docs/api/04-models.md
      ⚠️ 遗留：RETRO_OP 的 M4:A/M4:P 是否收敛至 15（Stage 2 后复审）；上架前置 PURCHASABLE 需等报价链路可产生该状态
- [x] 5 供应商与报价 C — 契约：`docs/api/05-quotes.md`（b2c1fc2，14 接口 + 14 条已定稿决策）
  - [x] 5a-1 数据底座 + 供应商身份解析 + 可报价 SKU 列表
        commits: 4573c31(000012 补 6 列) / b302936(000013 供应商种子) / 2955f31(身份解析) / bcd3e62(GET /supplier/skus)
        + 29b0466(复查修复：txOf 约定 / 列表 N+1 → 固定 3 次查询 / lint 清零 / 补 000014 官方价种子)
        已验证：migrate 到 14；supplier_a 登录 200；GET /supplier/skus 200 且
        lifecycle 过滤生效（DRAFT/DEPRECATING 不出现）、官方价以字符串返回 8 位精度、
        page/size 规整；无 token→401、staff token 打供应商路由→401、供应商 token 打内部路由→403
  - [x] 5a-2 提交报价 + 历史 + 详情
        commits: 2a26d3a(领域+仓储：000015 rejected_at 列 / 九步校验 / 单事务落库+todo+audit / 22 条单测)
        + 1161371(三接口+路由+swagger)
        已验证：migrate 到 15；正常提交 200（3 行含 USD/CNY/绝对价，fx_tier "6.80" 数值等价落库 6.800）；
        同 key 重放不新增行；同 key 异 body→400；倍率不自洽→400（message 含官方价×倍率明细）；
        重复提交（已有 APPROVING）→409；并发同 body 不同 key→一 200 一 409；
        历史只回本主体（supplier_b total=0）；越权详情→404；缺幂等键→400；无 token→401；
        psql 实测 quote_sheet/quote_item/quote_component/todo_task/audit_log 五行齐备
        ⚠️ 遗留：E2E 并发走 DefaultBizKey 去重（相同 body 不同 key → 10005），业务互斥
        ExistsNonTerminalQuote 与 uk_quote_ver 兜底为单测覆盖，真实 23505 路径留待 5b 联调
  - [x] 5b 审批 + diff + 激活
        commits: b86ec74(领域+仓储) + c500c66(五接口+路由+swagger)
        已验证：pending 列表（buyer_a 见 1 条 / buyer_b SELF 域 total=0）；diff 三方对照
        （prev/official/market + delta_pct + 失真 + 毛利预演 floor=price/(1−0.15)）；
        approve 200→APPROVED_PENDING + todo DONE + task_job(ACTIVATE_QUOTE) + audit；
        同 key 重放返回首次结果；不同 key 再批→409；reject 200（rejected_at 写入）+
        同 key 重放 + 不同 key→409；activate-due 激活（旧 EFFECTIVE→EXPIRED valid_to=
        新 valid_from、PENDING_VERIFY SKU→PURCHASABLE、COST_RECALC 入队、event_outbox、
        audit QUOTE_ACTIVATE+SKU_PURCHASABLE）；immediate approve 事务后同步激活
        （id=8 EFFECTIVE、id=6 EXPIRED valid_to=新 valid_from）；越权 approve→403；
        不存在→404；supplier token 打内部→403；无 token→401
        ⚠️ 遗留：① 预约生效依赖定时扫描，阶段 6 引入 worker 骨架时补分钟级 ticker；
        ② G3 的"delay analysis"在 monolith kill list 但 golden prompt 没有——
        跨 prompt kill list 漂移，5d 复审
  - [x] 5b+ DefaultBizKey 空 body 跨路径误判修复 + publish 补挂幂等 — commit 5408abe
        修复：DefaultBizKey 纳入 c.Request.URL.Path（**不能用 c.FullPath()**——路由模板
        对 id=9/10 完全相同，加了等于没加）；publish 补挂幂等（坑位表"发布都要"）
  - [x] 5c 批量导入
        commits: d1e803b(领域+仓储) + d74c517(三接口+路由+swagger+契约 §6.1 改写)
        已验证：模板下载动态列（official_/multiplier_/price_ × 4 组件并集排序、BOM、
        valid_from/valid_to 不在 CSV、last_valid_to 只读参考）；?format=xlsx→400；
        上传预览（3 行 OK/OK/ERROR、ERROR 不入 preview_items、行号从 2 开始）；
        confirm 200（source=IMPORT + audit QUOTE_IMPORT + APPROVING + todo_task，
        不跳过审批）；同 key 重放不新增行；无 token→401、staff token 打导入路由→401、
        缺幂等键→400
        ⚠️ 手工改数声明：为释放互斥，E2E 中把 id=16 置 REJECTED、todo 置 DONE
  - [x] 5c+ 模板列改全部 12 组件（无官方价 SKU 可导入）+ preview 回填 sku_code — commit 3ee3875
        修复：动态列从「官方价组件并集」改为全部 12 个 component_type（有官方价的排前面），
        否则无官方价 SKU（如 embedding 新模型）的组件不在并集里无法通过 CSV 导入
  - [x] 5d 到期闭环 + 异常 + 特权补录
        commits: cc28a82(领域+仓储) + 984d9d9(五接口+路由+swagger)
        已验证：expiring 核字段值（days_left/grace_until 实时算/alert_level/single_point）；
        扫描去重真库连跑两次 todo/alert 行数不增长；confirm-remove 未过宽限期→409、
        过宽限期执行（EXPIRED + remove_confirmed + COST_RECALC(EXPIRE_REMOVE) +
        event_outbox(quote.expired) + audit）；retro-effective 集合路径 200
        （source=RETRO/retroactive=true/立即激活 EFFECTIVE/关闭旧 EFFECTIVE valid_to=
        effective_time/COST_RECALC payload 带 effective_time/audit QUOTE_RETRO+
        QUOTE_ACTIVATE 同 request_id）；retro 同 key 重放不新增行；无 token→401、
        scan type 非法→400
        ⚠️ 手工改数声明：E2E 中为构造场景改过 id=19 的 valid_to（5 天后→4 天前）、
        id=19 的 status（EXPIRED→EFFECTIVE）、id=22 的 status（EXPIRED→EFFECTIVE）、
        id=23 置 REJECTED（buyer_a 误批准，因 PROCUREMENT 持 M4:P——遗留见下）
        ⚠️ 遗留：① 分钟级/每日 ticker 待阶段 6 worker 骨架；② audit_log.operator_id
        命名空间撞号（internal_staff 与 subject_operator 都有 id=1），F 阶段统一治理；
        ③ RETRO_OP 与 PROCUREMENT 的 M4:A/M4:P 权限重叠（buyer_a 也能调 retro-effective），
        Stage 2 遗留复审；④ COST_RECALC 只入队不消费（阶段 6 成本引擎）；
        ⑤ diff 的 margin_preview 是简化预演（真正预演需 M5+M7，Stage 6）；
        ⑥ activate-due/immediate 激活失败的孤儿风险（5b 同 5d 的补录激活失败告警）
  - [ ] 6 成本引擎 D — 契约：`docs/api/06-cost.md`（9a78638 校正稿：fx 档位/字段口径/D5 推迟/四因子公式/角色加固）
        拆分：6a worker 骨架（不注册 cost-recalc Job，防吞任务）→ 6b 成本参数+基线 → 6d 四因子+锁定+比价
  - [x] 6b-2 成本域 + 仓储 — commit db24a88
        已落地：internal/domain/cost（param 三级解析 + baseline 公式/值未变含 formula_version +
        factor 四因子 6b 中性占位 + compare range/trend/market_best/代表组件 + service 编排
        CalcSKU/RecalcSKU/RecalcFromTask）+ internal/repo/cost_param.go / cost_baseline.go
        （cost.Store GORM 实现：FOR UPDATE 行锁 + 23505/23P01→ErrVersionConflict、
        ApplyNewVersion 单事务五连写）。单测 58 条全绿，golangci-lint 0 issues。
        ⚠️ 关键实现裁决（6b-3/6b-4 必读）：
        ① ListRecalcTargets = 该 sheet 的 sku ∪ 该供应商当前 EFFECTIVE 的 sku，
           **不依赖 payload sheet 当前状态**（交接 §6-3）；
        ② LoadRecalcInput 按 **SKU 取全部供应商 EFFECTIVE**（主供应商选择必须看全集），
           币种与模型不一致 → 报错（数据事故不止于跳过）；
        ③ price 归一化的输入用「代表组件**完全成本**」（合同口径 supplier_cost=完全成本列）；
        ④ 快照 formula_version 参与值未变判定——读侧 baselineFromRow 必须从
           calc_snapshot 还原 FormulaVersion，否则旧版本永远判 changed；
        ⑤ audit action=COST_BASELINE_RECALC、event=cost.baseline.changed（同事务）。
  - [x] 6b-3 COST_RECALC 消费者 — commit 4789b3a
        已落地：cost.Service.ConsumeTask（Claim PENDING→RUNNING 条件更新被抢不报错 /
        NO_QUOTE 按 DONE 记 / 业务错误 MarkFailed 且 cause 必带约束名 / effective_time 覆盖
        消费时刻 / nil TaskStore 报错）+ 启动复位 ResetStaleRunning（RUNNING→PENDING 且
        retry_count+1，6b-3 复核注意点 3，并入本批）+ 退避 1min/5min/第 3 次置 DEAD 并
        原子写 alert(COST_RECALC_DEAD, CRITICAL, TASK_JOB/<id>) NOT EXISTS 去重 +
        worker 注册 cost-recalc（cfg enable 默认 1m，先 ResetStaleRunning 再 PollDue(50)）+
        main.go 装配 cost.NewService(NewCostBaselineRepo, NewCostTaskRepo, log) +
        NewCostJobRepo。单测 +7（ConsumeTask 全路径），全量 build/vet/test/lint 0 issues。
        ⚠️ 业务事务与任务状态推进严格分离（6b-3 复核注意点 1）：认领/推进只动 task_job；
        基线版本切换在 Service 里用 txOf 独立事务——业务回滚不会把 RUNNING 拖成孤儿。
        ⚠️ 13 条存量（id=17~31）：RETRO 任务的 effective_time 落在 2026-09-11~13，
        撞 ex_cost_no_overlap(23P01) 属预期 → FAILED + last_error 含约束名；不重写逻辑；
        单条失败不拖垮同批其他 11 条（c 独立事务，每条独立 claim）。E2E 验证留给 6b-4。
        ⚠️ 6b-2 复核修复并入本批：FactorInput 只留 UnitCost（完全成本 = quote×(1+loss)×
        (1+channel)），删 PriceRaw/RepresentCost；原始单价经 SupplierCost 字段透传
        calc_snapshot，只记录、不参与任何计算；字段注释写死"归一化与排序的输入是
        完全成本，不是原始报价"——避免 6c 接手再犯 min/raw>1 的归一化错位。
  - [x] 6b-3+ 真库 E2E 修复 — commit 785652f
        真库消费撞出三处 bug，已修并通过验收：
        ① cost_baseline.LoadRecalcInput 用错列名——model_sku 的币种列 native_currency
           误写成 currency（SQLSTATE 42703），13 条首次消费全部失败。
        ② Claim 只允许 PENDING——FAILED 被永久锁死（退避不兑现）；改为 (PENDING|FAILED)
           双谓词 + next_run_at<=now 守卫，FAILED**不是终态**（等价"等退避"）。
        ③ Claim Exec 参数绑定顺序错乱——误传 (now, now, id) 导致 timestamptz 收 bigint。
        E2E 验收结果：task_job 13 条全 DONE；cost_baseline 2 行 is_current（sku40/sku41）；
        cost_component 3 行验收常量全对（2.60075000 / 12.48360000 / 62.41800000 / 2.50000000 /
        12.0 / 60.0 supplier_cost）；快照含 formula_version=6b-v1 + params_scope=GLOBAL +
        scores[].supplier_cost；alert 表 0 行（无 DEAD）。full build/vet/lint/test 0 issues。
        ⚠️ 复核注意点 2 退避已生效：13 条先被撞到 FAILED(retry_count=1, +1min)，
        服务重启后第一个 tick 重置为 PENDING+retry_count=2（ResetStaleRunning 真的复位了
        RUNNING），再 Claim 成功消费。
  - [x] 6b-4 只读接口（cost/baselines + history）+ 路由 M5:V + 字段剔除挂接 + swagger + 真库 E2E — commit 2912aa4
        已落地：internal/domain/cost/read.go（ReadService，与重算 Service 共用同一份纯函数
        RepresentativeComponent/Floor/StringFixed(8)，注释互指防漂移——裁决1）+
        repo ListBaselines（固定 2 查询：count + paged JOINs，含 supplier-count 子查询，
        无 N+1；DISTINCT ON 代表组件 input 优先否则字母序；keyword ILIKE；OnlySinglePoint）
        / ListBaselineHistory（asOf 半开区间 [valid_from, valid_to)；{sku} 数字→id 否则
        sku_code，多行=数据事故报错，不存在→ok=false→404）/ LoadMinGrossMargin（sys_config，
        读失败→floor_price=null 不 500，读路径降级）+ api/cost.go（maskAndSuccess 按端点
        挂 fieldmask.Apply，op=nil 直通——裁决2，非全局 hook）+ RequirePerm(perm.M5View) +
        swagger 双路径。单测 +9（fakeReadStore：floor 计算 2.60075000→3.05970588、
        margin 缺失→null 不报错、margin=1 除零护栏→null、asOf 透传、code 解析、JSON key 锁定）。
        真库约束验证：internal/repo/cost_baseline_constraint_test.go（//go:build !short）
        uk_cost_current=23505 / ex_cost_no_overlap=23P01 / 无关错误不误判，全 PASS；
        psql D1/D2 直插复现两条 SQLSTATE（显式 ROLLBACK 无残留）。取代了原"消费者撞约束"方案
        （区间中插版本是合法分裂，不产生冲突）。
        E2E 三视角（jq has() 断言）：smoke_admin unmasked 对照（sku40 unit_cost=2.60075000
        floor=3.05970588，history 含 calc_snapshot 12 子键）；smoke_sales masked 侧
        （unit_cost/unit_cost_basis/cost_min/cost_max/cost_weighted/supplier_cost/
        calc_snapshot 七键 has()=false，floor_price 仍在——000017 关键边界）；
        buyer_a（000019 后）200 total=2。边界用例：code 解析、asOf 命中/未命中
        （200+空 list 非 404）/非法 asOf→10001、未知 sku→404 全对。
        M2 真库参数变更闭环：loss_rate 0.0300→0.0500 + 手工入队 task id=32（PARAM_CHANGE）
        → sku40 v2（input unit_cost=2.65125000 supplier_cost=2.50000000，API floor=
        3.11911765 与手算 2.50×1.05×1.01/0.85 一致，valid_to 链 v1→v2 衔接）；还原
        0.0300 + task id=33 → sku40 v3 current 回到 2.60075000。终态：task_job 18 条
        全 DONE，alert 无 COST_RECALC_DEAD。
        ⚠️ 手工改数声明：① 000018 重置 smoke_sales 密码为 Test@1234（原密码遗失；
        登录才能拿到含新 mask 的 role_snapshot）；② M2 两次 UPDATE cost_param.loss_rate
        （0.0300→0.0500→0.0300，已还原）；③ 手工入队 task_job id=32/33（PARAM_CHANGE，
        均已被消费为 DONE）；④ sku40 现为 v3（v2 是 0.0500 实验版本，已关闭）；
        ⑤ D1/D2 与约束单测行全部在事务内 ROLLBACK，无残留。
        ⚠️ 遗留登记：
        ① history asOf 未命中语义 = 200 + 空 list（非 404），后续消费方勿按 404 处理；
        ② fieldmask 首个挂接点在 api/cost.go maskAndSuccess（按端点）；未来若评估
           response.Success 全局挂接，必须先做全接口字段扫描再动，勿直接改全局；
        ③ SUPPLIER/CUSTOMER 的 cost mask（000017）本批不可达——SUPPLIER 无 M5:V 且在
           供应商门户，/api/internal/* 物理不可达（伪命题）；真库效果待阶段 11 供应商/
           客户门户出现成本字段时验证，已 SQL 确认 role.field_mask 落库正确；
        ④ 000019 修设计缺口：PROCUREMENT 原只有 M5:E 无 M5:V，与设计 §8.7「采购可见 ALL」
           不符；写侧由 pkg/perm 角色函数把关，补 V 不扩大写权；
        ⑤ representCompJoin 现为全表子查询 JOIN（扫全表 cost_component 再回贴），
           数据量上来后建议改 LEFT JOIN LATERAL（... WHERE cc.cost_baseline_id = cb.id
           ORDER BY ... LIMIT 1）——阶段 F 性能优化项，当前规模不是瓶颈；
        ⑥ 代表组件口径有 Go（compare.go.RepresentativeComponent）与 SQL（representCompJoin）
           两份实现，一致性由 internal/repo/cost_baseline_represent_test.go（!short，真库）
           钉住——任何一侧改口径前先看这个测试，两侧必须同步改。
  - [x] 6a worker 骨架 — commit a2b58ba
        commits: a2b58ba(fix(worker): cron_lock 测试改纯 Go fake，修复 go.mod/vet 断裂)
        已落地：httpServer.Shutdown 在前 + worker.Stop 在后（cmd/server/main.go:147-151）；
        每日时间按 db.timezone（Asia/Shanghai）解析；单任务 panic defer recover 不带走 goroutine；
        6b 预埋 RUNNING 复位注释；cron_lock(job_name, run_date) PRIMARY KEY ON CONFLICT DO NOTHING 防重；
        4 个 Job注册（activate-quote 默认开，quote-expire-scan/final/anomaly-scan 默认关）；
        **不注册 cost-recalc**（防吞 16 条 COST_RECALC）
        本批 fix：worker_test.go 删 sqlite → 抽 `CronLocker` 接口（TryLock），
        真库 GORM 实现 + 纯 Go fake（map[job|date] 真实模拟主键冲突）；
        jobs.go 三个 daily 默认值兜底（07:00/07:10/07:20）
        已验证：go build/vet/test/golangci-lint 全绿；cron_lock PG 真库 E2E——
        首跑插入 `(quote-expire-scan, 2026-09-14, +08)`；改时间再触发同日日任务
        → 日志 `job already locked today, skip`，cron_lock 仍 1 行，
        todo_task/alert 与基线持平（9/9，近 1 小时新增 0）
        ⚠️ 手工改数声明：本批未改业务数据；cron_lock 只有 worker 自己插入的 1 行，
        未做"清会怎样"的破坏性实验。
        ✅ 裁决（2026-09-14）：cron_lock **永久保留，不清理**——5 job × 365 天一年才 1825 行，
        且"哪天跑过"本身就是运维审计信息；若阶段 F 觉得必要，再补一个"清理 90 天前记录"的每日任务。
        ⚠️ 遗留（按层划分）：① task_job 层（6b 闭环）——条件更新 PENDING→RUNNING 认领、
        retry_count、指数退避、超限置 DEAD 并写 alert，COST_RECALC 消费者的必需能力；
        ② worker 调度层（Scheduler 那一级）的退避与告警推到 F 阶段——task_job 与 Scheduler 是两层，不混；
        ③ stage 6a 的四个 Job 都没有指标/追踪，F 阶段治理。
        ⚠️ 遗留（开工前登记）：
        ① **角色权限过宽专项**（阶段 F 统一治理）：`M5:E` 被 6 角色持有——PRICING_OP/PROCUREMENT/FINANCE
           应有，MODEL_OPS/RETRO_OP 为种子历史遗留越权（补录专员能改成本参数、能锁主供应商）；
           与既有「RETRO_OP 与 PROCUREMENT 的 M4:A/M4:P 权限重叠」合并为同一条专项。
           阶段 6 治标：`pkg/perm.CanEditCostParam/CanLockPrimary/CanLockFx` 按角色 code 收敛 + 单测。
        ② 幂等 FAILED 语义（"业务部分写入 + 500" 概率极低但存在），阶段 10 治理，不动中间件。
        ③ 6b→6c 版本抖动：6b 用成本最低占位、6c 换四因子评分，因 primary_supplier_id 参与
           值未变判定，6c 上线时评分口径变化的 SKU 会批量产生新版本——验收时显式对比
           6b 结束的 version 最大值与 6c 跑完后的新增版本 SKU 清单，确认数量符合预期。
           【已按 6d-1 §8.5 验收关闭：sku40 v3→v5、sku41 v1→v2，新增 SKU={40,41} 与预期一致】
  - [x] 6d-1 四因子评分真实化 — commit 1776582（factor 三因子接真值 + 主供应商换总分 +
        StabilitySince 断档聚合 + constraints 容错取值 + 快照 stability_since/four-factor +
        FormulaVersion 6b-v1→6d-v1 + 迁移 000020 演示装置）
        已落地：internal/domain/cost/factor.go（ScoreSuppliers 加 now 入参；compatIncompatible
        六值集合 trim+小写；StabilitySince 丢脏区间/首尾相接不断档/curEnd max 延伸）+
        service.go（sortSupplierResults 三级平手 Total desc→cost asc→id asc，BackupSequence
        同序；constraintsInt64/constraintsCompat 类型容错不 panic 不传 0；快照四值 Round(6)、
        stability_since nil→JSON null）+ repo LoadRecalcInput 补生效区间单条查询
        （EFFECTIVE+EXPIRED、JOIN quote_item 按 sku 过滤防 sheet19 混单、无 N+1）。
        单测 76 条（+18）全绿：TestCalcSKU_PrimaryByTotalScore 是逆直觉验收核心
        （B 完全成本 2.39269 < A 2.60075 但 A 因配额 1.0+稳定 09-10 当选）。
        变异验证六处（区间 max→覆盖、断档 >→>=、total desc→asc、quota max nil、
        trim+lower 脱壳、int64 去整校验）全部被抓回。
        真库 E2E 天然对照：旧 6b 进程（8080）消费 task 35 → sku40 v4 primary=2 rule=6b-lowest-cost；
        新 6d 进程（18080）消费 task 36 → v5 primary=1 rule=four-factor、
        A total=0.681493/stab=0.061973/quota=1/stability_since=2026-09-09T16:00:00Z（=+08 09-10 00:00，
        历史扫描真跑了的铁证——当前单 sheet27 是 09-13 12:00）、
        B total=0.653290/quota=0.5（无约束兜底）；版本抖动 sku40 v3→v5、sku41 v1→v2 符合预期；
        floor=3.05970588/unit_cost=2.60075000 未变；excluded=[]；无 DEAD 告警；loss_rate 0.0300 未动。
        API 三键：GET /cost/baselines sku40 v5 pri=1 cnt=2 single_point=false。
        ⚠️ 手工改数声明：① task_job 手工入队 id=34（误含 request_id 后删）→35（6d1-demo-manual）
        /36（6d1-reprobe），全 DONE；② 迁移 000020 down/up 各跑一次验证可回滚（up 再入后
        quote_item 新 id=43，组件 id 复用 identity 序列不保证 42）；
        ③ configs/config.yaml 端口临时 8080→18080（未提交，已工作区还原）。
        ⚠️ 遗留：① 兼容性因子（权重 0.08）无数据源：库里/导入模板/提交接口都没有
        compatibility 字段，本阶段恒 0.5，是否补采集单独决策（本批不动）；
        ② quote_sheet 历史区间存在 valid_to<valid_from 脏数据（种子遗留，sheet 19/24/25），
        StabilitySince 已丢弃这类区间，根因未清，建议阶段 F 清洗；
        ③ 两台 go run server 仍在跑：8080=旧 6b 二进制（留存对照，下次开机前 kill）、
        18080=新 6d（PID 见任务输出 bbmksu62x）——**重新部署/交接时先用新码重启**，
        否则 6b 代码会继续消费 COST_RECALC 把口径打回「成本最低」（本次 v4 就是这么来的）；
        ④ golangci-lint 本地 v2.13.2 无法加载（go1.26 vs 1.24 工具链），v2.8.0 兼容——
        若 CI 钉版本需统一处理。
  - [x] 6d-2 成本参数读写接口 + 复核批 — commit 65a71e9（功能）+ 本批 commit（复核 Task 1-4）
        已落地（65a71e9）：GET/PUT /api/internal/cost/params；domain ParamService（裁决：
        defaults 只读 400 / overrides 全量替换 DELETE+INSERT / GLOBAL 双保险 / parseRate 严格
        校验拒绝科学计数法 / AnyRoleCan 服务层角色收敛 PRICING_OP / 缺失 overrides=400
        空集合法清空 / 事务同队 audit+task_job 入队）；repo CostParamRepo（scope_type 限定
        DELETE、audit COST_PARAM_UPDATE、task_job payload 带 sku_id+PARAM_CHANGE）；
        ConsumeTask 新增 SKUID 分支（带 sku_id 直调 RecalcSKU，否则走 legacy 展开）；
        服务层 maskAndSuccess 同挂接点；swagger 注解 + openapi/ 重新生成。
        单测 +15 全绿、变异 4 处抓 3（DELETE-scope 被 fake 吞——已登记 6d-2-④）。
        真库 E2E 三层证据链（v6 loss=0.05 → v7 loss=0.03 current；unit_cost 与独立 Python
        decimal 对账一致；sku41 未受牵连只在 GLOBAL 集合内被重算）；幂等双崩塌验证
        （smoke_sales 无 M5:V→403；buyer_a 有 M5:E 非 PRICING_OP→403）；
        用户复核的变异注入四例全过。
        复核批改动（本批）：**Task 2 落 A**——新增 StoredParam（字符串字段，原样透传
        numeric(8,4) 入库字符）+ ParamStore.ListStoredParams；ListParams 改走
        ListStoredParams（不再经 decimal），修复 6d-2 功能批里「注释写原样输出」与
        「实际 StringFixed(4) 重造」的自相矛盾。pin 测试 TestListParams_PreservesTrailingZeros
        钉死写入 "0.0500" 后 GET 仍返 "0.0500"（不是 decimal.String() 的 "0.05"）；
        TestStringTrimsTrailingZeros 把「String() 会 trim 尾零」用单测钉死，未来若依赖库
        反转该行为本测试会立即挂。写侧 StringFixed(4) 保持不变（PUT body 可能给 "1"/"0.5"
        这类省略字面量，与 numeric(8,4) 对齐需要规整）；审计 before 仍过 ListAllParams+
        formatRate（审计 JSON 需与写库字符串对齐以便 before/after肉眼可比对，非 API
        响应不受「原样透传」约束）。
        ⚠️ 手工改数声明：本批零改数（全部通过 API 完成：PUT 写入 MODEL/40 loss_rate
        "0.0500"→GET 验证 "0.0500"→PUT 清空 overrides 回到 GLOBAL-only；中间产物 sku40
        v6/v7 版本切换均由 API 触发的 COST_RECALC 消费完成）。
        ⚠️ 遗留登记：
        ① **defaults 写是否永远只读**：本批裁决 PUT body 带非空 defaults 直接 400（绝不静默
           忽略——静默吞输入等同 bug）；是否开放 defaults 写待产品确认（若开放需补
           「defaults 变更触发 GLOBAL 全 SKU 重算」测试）。错误消息里包含 6d-2-① 引导排查。
        ② **MODEL scope 语义不一致**：契约/直觉是「按 model」，实现 service.go:277 用
           `ResolveParams(input.Params, input.SKUID, q.SupplierID)`——MODEL.scope_id=
           sku_id 粒度而非 model_id。ListParamAffectedSKUs 的 MODEL 分支因此 JOIN 条件
           是 cb.sku_id=scope_id。本批冻结（改成 model_id 粒度会改变 6b/6d 已落
           版本的计算口径）；产品决定「改文档 vs 改实现」——6d 收尾时统一裁决。
        ③ **GET 返回 "0.0300"（as-stored）vs 旧契约 "0.03"**：已改契约为 4 位小数字符串
           （docs/api/06-cost.md §7），并显式告知前端**不要按字符串相等比较**（"0.0300"
           ≡ "0.03" 数值同但字符串异），必须 parseFloat/decimal 比较。本批 Option A 落地后
           该口径物理生效。
        ④ **DELETE 不触 GLOBAL 的约束测试缺口（REPLACE）**：fakeParamStore.ReplaceOverridesTx
           不跑真 SQL DELETE，因此「DELETE WHERE scope_type IN (MODEL,SUPPLIER)」的过滤
           条件是否真把 GLOBAL 排除**未被测试钉住**（上述 4 个变异点里唯一没被 fake 抓住的）。
           下一批按 internal/repo/cost_baseline_represent_test.go 的 !short 真库模式补一条
           约束测试：种一行 GLOBAL+一行 MODEL，调 ReplaceOverridesTx deletes 里塞
           [MODEL, GLOBAL]，断言 GLOBAL 行仍在、MODEL 行被删。
        ⑤ **PUT 四字段必填 vs 缺省允许**（登记待产品确认）：当前 parseRate("") 拒绝空串
           ⇒ 每条 override 必须四字段齐。是否允许部分提交（缺字段=保留旧值）属产品决策，
           本批不动；如要开放，需把「保留旧值」的合并逻辑并入全量替换语义+
           受影响 SKU 计算。
        ⚠️ 角色权限过宽专项新增证据（并入阶段 F 治理清单，与既有 M5 专项同条）：
           真库 SELECT 显示 **SALES 持有 M5:V**——销售可通过 GET /cost/params 拿到
           loss_rate/channel_rate。鉴于 000018 已允许销售看 floor_price（=unit_cost×
           (1+loss)×(1+channel)/0.85 公式里的两个系数输入），增量风险有被部分遮蔽，
           但仍属契约意外，并入阶段 F 的「角色权限过宽」治理一并复审。
  - [x] 6d-3 手动锁定主供应商 — commit 0d9856c
        已落地：POST /api/internal/cost/baselines/{sku}/lock-primary（M5:E + 服务层
        角色收敛 PROCUREMENT）；LockService 四项校验（reason 非空 400 / supplier 404 /
        该 SKU 有 EFFECTIVE 报价 409 / 冻结或 INACTIVE 409，前置只读、失败不写）；
        重复锁同对象 = 合法 no-op（Unchanged=true 返回旧版本号，不产新版本不写冗余审计）；
        非 no-op 路径复用 RecalcSKU 不可变版本管道（uk_cost_current / ex_cost_no_overlap
        同其他触发）；audit COST_BASELINE_LOCK_PRIMARY 单条带 before/after
        {primary_supplier_id, locked_manual, version}、target_id=新版本 id
        （RecalcOutcome.BaselineID 回填，不二次反查）。
        RecalcSKU 增加 manualLockSupplierID 形参 + sticky 继承：prev.LockedManual=true 时
        非 MANUAL_LOCK 触发也维持锁（calc.Primary→calc.All[idx]），primary_selection_rule
        = "manual-lock"；失效 → ErrLockedSupplierInvalid（绝不自动降级/自动解锁）。
        Unchanged() 把 LockedManual 纳入值未变判定——锁状态翻转必产新版本（陷阱 2 防线）。
        calc_snapshot 其余四值（loss_rate/channel_rate/components/represent_cost）也来自
        被锁供应商的 SupplierCalcResult，与 primary 同源，不再混用算法冠军。
        swagger 注解 + openapi/swagger.json|yaml 重生成。
        单测 +30（lock_service_test.go 24：no-op/sticky/snapshot rule/防线五连 + handler
        角色门 cost_lock_test.go 6：穿越/平台首元素被拒/次元素含 PROCUREMENT 放行）。
        变异验证 4 处全抓回（实测 logs 见会话）：① Unchanged() 删 locked_manual 判定 →
        TestUnchanged_LockFlippedIsChange(_SecondDefense) 红；② RecalcSKU LockedManual
        硬编码 false → TestRecalcSKU_LockInheritedOnNormalTrigger /
        TestLockPrimary_SuccessCreatesVersion / TestLockPrimary_UnchangedWithUnlockedPrevIsError
        红；③ handler AnyRoleCan→Roles[0] → TestLockPrimary_RoleGate_ProcurementSecondAlsoAllowed
        红（[PLATFORM_ADMIN, PROCUREMENT] 被错误 403）；④ 被锁失效分支跳过不报错
        → TestRecalcSKU_LockedSupplierFrozenFails 红。
        E2E 真库验收（psql 字段级核验）：
        smoke_admin [PLATFORM_ADMIN/MODEL_OPS/PRICING_OP] → 403（"不含 PROCUREMENT"）；
        buyer_a 锁 sku40→supplier2 → cost_baseline v10 primary=2 locked_manual=true
        rule=manual-lock；audit target=181 before {1,false,9}→after {2,true,10}；
        PUT params (MODEL 40 loss 0.0300→0.0500) → worker tick → v11 primary=2 locked=true
        rule=manual-lock loss_rate=0.05（**sticky 实证**——锁的是供应商选择、不是冻结成本
        数字）；清空 overrides → v12 primary=2 locked=true loss_rate=0.03。
        ⚠️ 手工改数声明：**零改数**。全部通过 API 驱动（登录 / PUT cost/params / POST
        lock-primary）；v10/v11/v12 的版本切换由 worker 消费 COST_RECALC 完成。
        ⚠️ 遗留登记：
        ① **解锁接口（unlock）**：本批不含删除锁的 API；要解锁需走
           「再次调 lock-primary 换锁到别家」或等手工 SQL（治理流程另立）。
        ② **MANUAL_LOCK 触发的跨 SKU 一致性议程**：锁的是「一个 SKU 选择哪一家」，不是
           「供应商层全 SKU 偏好」——同供应商跨多 SKU 是否要同一锁定状态属业务决策，
           本批单 SKU 粒度已够用；阶段 F 复审。
        ③ **幂等 DONE 重放 data:null**：same key replay 返回 `{code:0, data:null}`
           （result_json 中间件未落盘——**既有 bug 与 6d-2 PUT params 同样命中**；
           前端若依赖重放拿到 data 需注意，可改为重放时查当前基线或修复落盘）。
        ④ **重复锁同对象 no-op 200 vs 409**：本批裁决 200 + Unchanged=true（不构造
           前后一致的假审计、不污染版本链）；若产品认为应返回 409 提示用户，需补
           LockService 的状态判别。
        ⚠️ 既有 bug 顺带发现（与 6d-2 同根）：idempotency_key 表 result_json 在
           MarkResult 时只把 handler 通过 SetIdempotencyResult 挂的快照落盘，
           未挂的快照 → NULL → 重放返回 `data:null`。**已有 PUT /cost/params 两条记录
           实测为 NULL**，本次锁定接口也命中。修复需要 response.Success 全局挂接
           （需先做全接口字段扫描，与 6b-4 遗留②同一条）。
  - [x] 6d-4 比价 + 议价机会 — commit 0b70271
        已落地：internal/domain/cost/compare_service.go（CompareService = *Service 复用
        CalcSKU 纯函数 + BaselineReadStore 读侧 + now 可注入；Compare 实现 §4 比价
        实时计算不走 calc_snapshot；Opportunities 实现 §5 bargain/single_point）+
        service.go 加导出 LoadRecalcInputForCompare（私有 store 的只读装配入口，
        陷阱 2）+ repo/cost_baseline.go 新四方法（ListBaselineTrend 子查询 + DISTINCT ON
        + Asia/Shanghai 日历日 + representCompJoin 复用 / ListSKUIDs /
        LoadSysConfigDecimal / ListPrimarySupplierNames + ListPrimarySupplierIDs）+
        api/cost_compare.go（静态段 /cost/opportunities 先于 /cost/baselines/:sku/compare
        注册，与 6b-4 同款纪律）+ router.go 挂两路由 + main.go 装配。
        单测 +16（suppliers 展开/range 手算锚点/fallback 直测/days 规整/404/NO_QUOTE 跳过/
        bargain 阈值严格 >/边界 == 不入选/空 list 非 null/单 SKU NO_QUOTE 不拖垮整单/
        single_point 复用/非法 type/阈值读错上抛）。
        变异验证 4 处全抓回（实测 logs 见会话）：
        ① ComputeRange Σtotal=0 fallback 改成不除 → TestCompare_RangeArithmeticFallback
           红（实得 5 期望 2.5——sumW=0 且 unit_cost 非零组合 handler 路径物理不可达，
           只能靠 ComputeRange 直测）；
        ② MarketBest min→max → TestCompare_RangeAnchors 红（market_best 期望 2.39269
           实得 2.60075）；
        ③ bargain !GreaterThan→LessThan → TestOpportunities_BargainBoundaryNotIncluded
           红（deviation=0.3=阈值时错误入选，严格大于语义防漂移）；
        ④ ComputeTrend 按天→按小时 → TestComputeTrend_TimezoneDayBoundary 红。
        真库 E2E（三视角）：smoke_admin compare sku40 全部字段对（suppliers=2、
        s1 unit_cost=2.60075000 is_primary=true total=0.68308、s2 unit_cost=2.39269000
        is_backup=true、range.cost_min=2.39269000 cost_max=2.60075000
        cost_weighted=2.49891287 与手算 2.49891285 一致、market_best=2.39269000、
        trend 3 点 2026-09-14/15/16 升序 YYYY-MM-DD）；compare sku41 单点 1 家
        cost_min==cost_max=12.48360000；bargain type=bargain 空 list（真库主供应商
        都是 market_best deviation=0<0.30）；single_point list 只含 sku41 supplier_count=1
        sku40 不入选；type 缺省/foo → 10001；未知 sku → 404；无 token → 401；
        buyer_a (PROCUREMENT M5:V) → 200。
        ⚠️ 手工改数声明：**零改数**（全部为只读接口；测试过程只发 GET 请求）。
        ⚠️ 关键实现裁决（6d-4 必读）：
        ① **compare 不从 calc_snapshot 读**（陷阱 1）——snapshot.scores 没有 constraints/
           逐组件成本/unit_cost，且受 task_job 消费延迟影响最多 1 分钟。契约 §4:144
           明说「range：实时计算，不落库」——走 LoadRecalcInputForCompare + CalcSKU
           实时算，报价生效立即反映。
        ② **bargain 的 primary_supplier_id 来自 cost_baseline 当前行**，不是
           CalcSKU.Primary——若该 SKU 被人工锁定（locked_manual=true，6d-3），
           算法冠军是便宜那家、被锁的贵那家才是真实主供应商；CalcSKU.Primary 会读错
           对象把"贵供应商被锁定"的场景漏掉（这正是 bargain 的核心场景）。
           ListPrimarySupplierIDs 单条 SQL 拿 sku_id→primary_supplier_id，不 N+1。
        ③ **trend SQL 必须用子查询暴露 date 别名**（SQLSTATE 42P10 实测踩过）——
           PostgreSQL DISTINCT ON 要求与 ORDER BY 表达式物理一致，且 ORDER BY 不能用
           SELECT 别名。最终形态 = 内层 SELECT to_char(...) AS date + 外层
           DISTINCT ON (date) ORDER BY date, version DESC, id DESC。
        ④ **MarketBest 独立调用而非复用 range.cost_min**——让 market_best 语义独立
           于 range（变异验证 2 的挂接点：改 MarketBest 取 max 立即被 RangeAnchors
           测试抓回，若复用 cost_min 就漏这个防漂移点）。
        ⑤ **suppliers[] 顺序 = calc.All 的四因子排序序**（主供应商在前，确定性输出）。
        ⚠️ 遗留登记：
        ① buildSnapshot.scores 暂未补 constraints/components/unit_cost（本批 compare
           走实时计算不从 snapshot 读；将来若改 snapshot 读侧需补字段，与契约 §10-9
           对齐）；
        ② bargain N 次重算性能（当前真库 2 个 SKU 不是问题；若规模上来需要缓存
           或批处理）；
        ③ trend 代表组件 SQL 与 Go RepresentativeComponent 是两份实现，由
           internal/repo/cost_baseline_represent_test.go（!short，真库）钉住——
           改任何一侧前先看那个测试；
        ④ trend SQL 的子查询形态是 42P10 后修正版——后续若要加 where 条件（如
           is_current 过滤、change_reason 过滤），先看子查询完整性再改；
        ⑤ 本批真库 sku40 当前版本是 v12（primary=1，locked=true）但 compare 算出
           primary=1 是四因子实时冠军——compare 不感知 locked_manual（契约未要求），
           前端如果要"显示锁定状态"应走 §2 列表/§3 history 的 locked_manual 字段。
- [ ] 7 官方价变更 B
  - [x] 7a 采集批次 + 暂存区 + 差异比对（§5/§6）— commit 39eed7f
        已落地：POST/GET /api/internal/price-sync/jobs（M2:E/M2:V）+ POST/GET
        /api/internal/staging-prices（POST 挂幂等，红线 5）；领域层
        internal/domain/price/sync_service.go（SyncService + ComputeDiff 纯函数）+
        repo/price_sync.go（单事务 INSERT+audit；LoadCurrentPriceVersions 固定 2 发
        查询无 N+1）+ api/price_sync.go + router/main 装配 + 契约 07 §5/§6 定稿 +
        swagger 重生成。审计 PRICE_SYNC_JOB_CREATE / STAGING_PRICE_SUBMIT（红线 10）。
        单测 24 条全绿；变异验证 3 处全抓回：① ComputeDiff 的 Equal→!Equal 反转 →
        6 条测试红；② 删 delta_pct 除零保护 → OldZeroDeltaNull 红 + decimal panic；
        ③ UNMATCHED 硬填 sku_id → RawCodeUnmatched 红。
        E2E 真库验收（smoke_admin）：job 创建即 status=SUCCESS、started_at=finished_at、
        item_count=3；录入 4 行 created_count=4；列表 diff 四连全对——sku40 "2.50"→
        UNCHANGED（数值等价 "2.50000000"）、sku40 "2.80"→CHANGED（old_price=
        "2.50000000" DB 原文透传、delta_pct="0.120000"）、sku43→NEW（old_price=null）、
        unknown-sku→UNMATCHED（sku_id=null、diff_detail=[]）；同 key 重放返回首次结果
        不新增行；同 key 异 body→400；缺幂等键→400；sku 不存在/payload 非法/空 payload/
        未知组件→400；job 不存在→404；无 token→401；smoke_sales（持 M2:V 无 M2:E）
        GET 200 / POST 403；buyer_a 同。
        ⚠️ 与提示词的裁决偏差（红线 4 收敛）：裁决 2 原文 status="DONE"，但 000006 DDL
        枚举是 RUNNING/SUCCESS/FAILED——按 DDL 落 SUCCESS（语义等同"创建即完成"），
        不自创状态串。E2E 期望 old_price="2.50"，实际按 DB numeric(20,8) 原文透传
        "2.50000000"（数值等价；契约已写明前端勿按字符串相等比较）。
        ⚠️ 手工改数声明：E2E 边界脚本首次内联执行时输出被终端吞掉但请求实际发出，
        edge-001 误写入 staging_price id=5（sku41 "9.99"）——已手工 DELETE 该行；
        audit_log id=90 仍引用该已删行（审计表只插不改，留痕）；idempotency_key
        残留 7a-e2e-edge-001(DONE)/002~005(FAILED)/7a-e2e-staging-001(DONE)/
        7a-fresh-* 若干（FAILED 键复用永远 409 是预期语义，不清理）。
        除此之外零改数（price_version/cost_baseline 等生产表未动）。
        ⚠️ 遗留登记：
        ① 7a-① sync_job 状态机：MVP 创建即 SUCCESS；P1 自动采集时补 PENDING/RUNNING/
           FAILED 流转与 error_msg 写入；sku_ids 明细目前只留 item_count（表无 payload 列）。
        ② 7a-② staging_price.payload 结构本批裁决为 {component_type: 字符串} 扁平 map；
           将来若需要嵌套（阶梯价 tier_rules 等）再改，改前先读 sync_service.go 包注释。
        ③ 7a-③ UNMATCHED 行只标记不处理（丢弃/别名申报待产品确认——契约 07 §10-4）。
        ④ diff 是读侧实时计算（表无 diff 列）：7b confirm 时若需固化 diff 快照到
           change_request.payload，直接复用 ComputeDiff 纯函数，禁止再写一份口径。
        ⑤ SALES 持 M2:V（种子既有）：销售可看官方价暂存区与采集批次。官方价是对外
           主数据、非成本机密，评估为可接受；并入阶段 F「角色权限过宽」专项一并复审。
  - [x] 7b 确认入正式版本 + 审批 + 生效连锁（§7/§8/§9）— commit 079b33a
        已落地：POST /staging-prices/confirm（M2:E + 幂等）；ConfirmService 纯函数
        （mergeStagingsBySKU 同 SKU 取 max staging.id 整行覆盖 / detectDirection 任一组件
        新>旧 或 缺失旧组件为正 → UP / buildMarginPreview 仅 UP / buildChangePayload /
        representativeComponent / resolveCostParam MODEL>GLOBAL）；PriceConfirmRepo.
        ConfirmStaging 单事务 4 写（change_request[MarginPreview/CreatedBy=OperatorRole] +
        approval_steps[BizType=change_type] + staging.processed=true 条件更新 + audit
        PRICE_CHANGE_CONFIRM）；审批走既有 DecideApproval + 新增 DecideApprovalByType
        动态步数（UP=[MODEL_OPS,PRICING_OP] 2 步 / DOWN=[MODEL_OPS] 1 步）+ ApprovedHook
        回调；连锁 ApplyOfficialPriceChange 单事务 6 写（price_version 新版本 source=
        "SYNC" + 静默跟随 quote_sheet[EXPIRED 旧 + SILENT_FOLLOW 新 submitted_by=nil，
        倍率组件重算=新官方价×倍率、绝对价保留] + task_job COST_RECALC + event_outbox
        official_price.changed + cache_version+1 + audit OFFICIAL_PRICE_CHANGE_APPLY）。
        单测 17 条（confirm 15 + model DecideApprovalByType 2）全绿；变异验证 3 处全抓回
        （floor 公式 Add→Sub / detectDirection 反转 / ApprovedHook 失效）。
        E2E 真库验收（smoke_admin）：confirm UP crId=4 step_count=2 margin_preview
        floor=3.42687059（2.80×1.03×1.01/0.85）biz_type=PRICE_UP 步骤 MODEL_OPS+PRICING_OP；
        approve step1→PENDING；approve step2 同操作人→10001（自批拦截）；future→10001(400)、
        已 processed→10005(409)、不存在→10004(404)；DOWN crId=5 step_count=1 approve→
        APPROVED 连锁全库：price_version sku41 v1(MANUAL)闭 v2(SYNC)当前 input=10.00000000、
        quote_sheet sup=2 29(MANUAL v2)→EXPIRED + 31(SILENT_FOLLOW v3)EFFECTIVE submitted_by
        =NULL、sku41 input 倍率 0.8 重算=8.00000000、output 倍率但官方价未变保持 60.00000000、
        sku40 绝对价保持 2.30000000、task_job COST_RECALC reason=OFFICIAL_PRICE_CHANGE
        sku_id=41、event_outbox official_price.changed PENDING、cache_version official_price
        version=1。
        ⚠️ 手工改数声明：① 删 approval_step id=5,6（首个 bug 版 biz_type=CHANGE_REQUEST
        写入后清理）；② 删 change_request id=3（同上）；③ staging id=6 processed 复位后
        重新 confirm；④ cr id=5 连锁 500（cache_version scope_key→cache_key bug）后 APPROVED
        -without-effects 脏态复位 PENDING + 清步 decision，修复后经 API 重新审批成功。
        ⚠️ 遗留登记：① 7b-① scheduled effective_time（>now）不支持（本批 >now→400，
        与 5b 预约生效同类，待 worker 分钟级 ticker 统一）；② 7b-② DecideApproval 非事务
        ——连锁失败会留 APPROVED-without-effects 脏态（本次 cache_key bug 实测命中，手工
        复位）；修复方向=DecideApproval 包事务或连锁可重试，阶段 F 治理；③ 7b-③
        approval_step.biz_type 约定 = change_request.change_type（DEPRECATE/PRICE_UP/
        PRICE_DOWN，与 M4 "DEPRECATE" 一致）——DDL 注释里的枚举（CHANGE_REQUEST/
        PRICE_BOOK/...）是旧建议性描述，与现有约定不符，阶段 F 时**更新注释对齐现状**
        （不是改代码对齐注释）；④ 7b-④ UNMATCHED staging 行不可
        confirm（by design，待产品确认——契约 07 §10-4 与 7a-③ 同）。
  - [x] 7b+ 复核反馈收尾 — 本批 commit
        已落地：internal/repo/price_confirm_test.go（!short 真库）钉住倍率静默跟随重算锚点——
        TestApplyOfficialPriceChange_MultiplierAnchor 种 legal_subject(USCC≤18)+
        supplier_profile+EFFECTIVE 报价单（input 倍率 0.8=12.00 / freight 绝对价 2.30），
        db.WithDB 注入外层事务 + savepoint，连锁后断言：旧单 EXPIRED / 新单 SILENT_FOLLOW
        EFFECTIVE submitted_by=NULL / 倍率组件 10×0.8=8.00000000 / 绝对价 2.30000000 保留 /
        price_version 新版本 SYNC input=10.00000000；事务外 count 校验零残留。
        变异验证：off.Mul(mult)→off.Mul(mult).Add(1) → 期望 8.00000000 实得 9.00000000
        确定性抓回（复核反馈指出的「编译器抓的不算数」缺口已补）。
        ⚠️ 遗留 7b-③ 描述已改「约定优先于注释，注释待阶段 F 更新」。

---

- [x] 9a 客户列表 + 生成报价 — commit 88bbd28
      已落地：migrations/000023（customer_quote.quote_type NOT NULL DEFAULT 'APPLY' +
      ck 枚举 APPLY/CLONE/TEMP/SPECIAL/CONTRACT + idx_cq_customer_type）+ internal/
      domain/customer/service.go（CustomerItem/TransferInput/TransferImpact/
      TransferResult/QuoteItemInput/GenerateQuoteInput/GeneratedQuote/FloorViolation/
      QuoteFloorError/Unwrap=ErrBelowFloor；错误 9 个；常量 quote_type × 5 +
      status × 6；纯函数 ValidQuoteType[9a 只支持 APPLY/CLONE/TEMP]/CalcFloor
      [委托 cost.Floor]/CheckFloor[严格 LessThan]；Store 接口 13 个方法；Service.
      ListCustomers[page/size 护栏]+TransferPreview[confirm=false]+Transfer[confirm
      =true：LoadSalesOperator 校验 ACTIVE+SALES / TransferToSelf / TransferCustomerTx
      单事务]+GenerateQuote[类型校验 → CheckCustomerScope → LoadCustomerLevel →
      按类型取 items[APPLY: LoadEffectivePriceBook by level_code + items 空=全量
      否则 LoadPriceBookItemsForSKUs + 数量对齐] / [CLONE: LoadQuoteOwner 跨客户
      校验 + LoadQuoteItems 复制] / [TEMP: valid_to 必填 + 手工解析 unit_price] →
      LoadCurrentUnitCosts + LoadMinGrossMargin + 逐项 CalcFloor + Currency 补 →
      CheckFloor → QuoteFloorError → GenerateQuoteTx]）+ internal/repo/customer.go
      （行模型读写分离；applyCustomerOwnerScope[ALL/DEPT/DEPT_SUB/SELF on
      owner_sales_operator_id + JOIN org_unit path LIKE ANY]；ListCustomers
      flat-scan；LoadTransferImpact/LoadSalesOperator/LoadCustomerLevel/
      TransferCustomerTx[单事务 UPDATE 3 表 + audit.Record action=CUSTOMER_
      TRANSFER OperatorRole="STAFF"]；LoadEffectivePriceBook/LoadPriceBookItems
      [JOIN LATERAL component 取代表组件 input 优先 + 字母序]；GenerateQuoteTx
      [version_no=MAX+1，uk_cq_ver 23505 包装为「并发版本冲突」]）+ internal/
      api/customer.go（3 接口 + customerErrToAppErr[404/409 冲突/400 参数/500
      默认] + scopeOf 投影 + transferBody 主管 perm.AnyRoleCan + floorErr
      errors.As 解包返 409 携带违规数量）+ pkg/perm 新增 RoleOpsAdmin="OPS_ADMIN"
      + router（M8:V list / M8:E+Idem transfer / M9:E+Idem generate）+ main
      装配 + swagger 重新生成。
      单测 23 条全绿（fakeStore maps；覆盖 ListCustomers page guard/Transfer 主管
      校验 confirm/toSelf/GenerateQuote 全 3 类型全分支/CheckFloor 边界严格性/
      ValidQuoteType 全集/CalcFloor 3.00/0.85=3.52941176）；变异验证 3 处全抓回：
      ① CheckFloor LessThan→LessThanOrEqual → Boundary 红（== 被误判违规）；
      ② APPLY 删除 ErrNoEffectivePriceBook 短路 → ApplyNoEffectiveBook 红；
      ③ CLONE 删除跨客户校验 → CloneCrossCustomer 红。
      E2E 真库（smoke_admin + smoke_sales；迁移 v23 已应用）：
      login 双 200（admin PLATFORM_ADMIN+MODEL_OPS+PRICING_OP；sales SALES
      field_mask 已含 margin_before/after）；GET /customers admin total=2
      （customer 1 GLOBAL owner=2 冒烟销售 + customer 2 BASIC owner=1 冒烟管理员）、
      sales total=1（行级过滤 SELF 生效）；no-token 401；missing-idem-key 400；
      APPLY 全量带出 → 409「存在低于 floor 的报价，必须走特价审批（违规 1 项）」
      （真库 sku40 价 3.41812858 < floor 3.52941176，是 8b-2 fixture 抬高成本
      后价目表未重发的真实现状，floor 校验工作正常）；APPLY 指定 items=[sku41]
      → 200 quote_id=2 version_no=2 itemCount=1 pbVer=1；TEMP 缺 valid_to → 400；
      TEMP sku40 价 1.00 → 409 floor；TEMP sku40 5.00 + sku41 11.00 → 200
      quote_id=3 version=1（首次）/ version_no=3（重跑）；CLONE source_quote_id=99999
      → 404 源报价不存在；CLONE 跨客户 → 9a E2E 因 DefaultBizKey dedup 拦在 409
      （body 不带 source_quote_id 时已在 400 阶段拦下）——单测层覆盖跨客户冲突；
      TRANSFER preview confirm=false → 200 from=2 冒烟销售 to=7 smoke_sales_b
      pbCount=0 quoteCount=2（TEMP id=1 + APPLY id=2）；TRANSFER execute → 200
      migrated=3；listSalesAfterTransfer total=0（确认 customer 1 owner 已迁 7）；
      transfer to-self → 409「移交目标就是当前归属（no-op）」；transfer 非主管 →
      403「仅主管（OPS_ADMIN/PLATFORM_ADMIN）可移交客户」。
      DB 落库核验：customer_quote id=1/2/3（TEMP/APPLY/TEMP，DRAFT，version 1/2/3，
      owner=7 origin=2 留档）；customer_quote_item 5 行含 unit_price + floor_price
      快照正确；audit_log 4 行 CUSTOMER_QUOTE_GENERATE×3 + CUSTOMER_TRANSFER×1
      operator_id=1 operator_role=STAFF reason="9a-demo"。
      ⚠️ 手工改数声明：① tmp/9a-fixture-customer.sql（legal_subject '客户甲_9a'/
      '客户乙_9a' + customer_profile 2 行：customer 1 GLOBAL owner=2, customer 2
      BASIC owner=1）；② tmp/9a-fixture-sales-b.sql（smoke_sales_b staff id=7 +
      account login_id=smoke_sales_b + role_grant SALES，bcrypt 复用 smoke_admin
      的 Test@1234 hash）——属数据 fixture 不改任何业务表关键路径；
      ③ 业务行（customer_quote 1/2/3、audit_log 115~118）全部 API 驱动非手工。
      ⚠️ 遗留登记：
      ① 9a-① quote_type 列由 000023 追加（原契约 0.1 提及但 000005 遗漏）。
      ② 9a-② customer_quote 无 level_code 列，以表结构为准；「按 customer.level
         _code 定价目表」在 service 层显式读 customer_profile.level_code 而非
         customer_quote 快照。
      ③ 9a-③ customer_quote.price_book_version 是版本号（int），以表结构为准。
      ④ 9a-④ customer_quote 无 cost_baseline_id 列，从 price_book_item.baseline_
         version 间接关联。
      ⑤ 9a-⑤ customer_quote_item.floor_price/below_floor 快照在 item 上（聚合值
         计算），列名以表结构为准。
      ⑥ 9a-⑥ ErrInvalidQuoteType 在部分场景被复用为「items 长度不齐 / items 为
         空 / CLONE 缺 source_quote_id」的兜底参数错（APISchema 层提示，未来如有
         需要可拆 ErrItemsMismatch / ErrSourceQuoteRequired）。
      ⑦ 9a-⑦ 既有 bug 同 8a-⑨/8b-1-⑨/8b-2-⑦：idempotency result_json 落盘
         NULL（MarkResult 未挂 SetIdempotencyResult 的场景）——publish/generate
         200 时同 key 重放拿 data:null，本次 E2E 暂未触发成功路径重放，阶段 10
         治理。
      ⑧ 9a-⑧ LoadSKUCode 失败被 `_ =` 吞掉（sku_code 仅用于 FloorViolation 响应
         构造，失败不阻断业务，但 sku_code 会是空字符串）。
      ⑨ 9a-⑨ E2E 中 DefaultBizKey dedup 拦截了 applyPartialSku 的二次相同 body
         （虽换了 key）——业务键 dedup 是设计行为不算 bug，但 UX 不友好；阶段 10
         治理。

（9a 详细日志已归档到 CLAUDE-history.md「九、当前进度」的「9a 客户列表 + 生成报价」一节）

---

## 九之续：9b 特价审批 + 刷新 + 导出（详细日志归档）

- [x] 9b 特价审批 + 刷新 + 导出 — commit fb14a08
      3 接口：POST /customer-quotes/:id/special-price（DRAFT-only；重复 PENDING→409；
      reason+expected_margin(decimal) 必填；margin_impact 预演；change_request[SPECIAL_PRICE]
      sku_id=NULL + 2 步 approval_step[PRICING_OP→FINANCE]；全步通过 → ApplySpecialPriceApproved
      单事务 special_price_status='APPROVED' + event_outbox('customer.special_price.approved')
      + audit SPECIAL_PRICE_APPROVED）+ POST /customer-quotes/:id/refresh（unit_price 不动，
      只重算 floor_price；floor 变了才生成新版本 version_no=MAX+1 per-customer，旧版本
      EXPIRED；值未变则 unchanged=true 不产新版；可刷新状态 DRAFT/PENDING）+
      GET /customer-quotes/:id/export?format=xlsx（excelize 渲染；TEMP 带水印「临时报价，
      有效期至 X，仅限 Y 使用」；TEMP 且 valid_until<now → 409；format!=xlsx → 400 PDF 登记
      9b-②；直返文件流非统一信封）。
      单测 22 条全绿（fakeStore：margin_impact 精确值 / Request 各守卫 / Refresh Unchanged
      +Changed+Terminal+PendingAllowed+缺 baseline / Export 成功 + 404 + 400 + 409 + TEMP
      水印含客户名 + APPLY 无水印 + below_floor 实时计算）；3 处变异验证全抓回：
      ① 让 Refresh 即使 Unchanged 也走 RefreshTx 产新版 → TestRefresh_Unchanged 红；
      ② 让水印对所有 quote_type 生效 → TestExport_ApplyNoWatermark 红；
      ③ SpecialPriceApproved 常量 'APPROVED'→'EFFECTIVE' → TestSpecialPriceStatus_StringEnum
      红（repo 用同一常量）。
      E2E 真库（smoke_admin + smoke_finance）：
      special-price quote id=1 → cr_id=8 step_count=2 status=PENDING
      margin_impact={current_price=5.00000000, current_margin=0.03743316, target_price=5.00000000,
      target_margin=0.08, delta_gap_distance=0.41176471}；DB special_price_status=PENDING ✓；
      step1 PRICING_OP approve → 200；step2 FINANCE approve → 200 final_status=APPROVED；
      DB special_price_status=APPROVED ✓；event_outbox id=50 customer.special_price.approved
      PENDING ✓；audit 121=SPECIAL_PRICE_REQUEST + 122=SPECIAL_PRICE_APPROVED ✓。
      refresh quote id=2/1 → 200 unchanged=true old=new=2/1 changed_items=[]（floor 与当前
      baseline 一致）；同 key 重放 → 200 同 data；不存在 → 404；无 token → 401；手工改
      status='APPROVED' → 409（ErrQuoteTerminal）。手工改 sku40 baseline 3.0→3.5 触发
      changed → refresh quote id=1 → 200 new quote id=4 version_no=4（customer 1 MAX+1
      per-customer），changed_items[0]={sku40, 3.52941176→4.11764706}，unchanged=false；
      老 quote id=1 status=EXPIRED special_price_status=APPROVED（历史记录保留）；
      新 quote id=4 special_price_status=NULL（refresh 清空）items floor=4.11764706/10.58823529
      ✓；audit id=123 QUOTE_REFRESHED ✓。事后已删脏 baseline id=344 + 恢复 336 为当前。
      export quote id=2 (APPLY) → 200 ct=spreadsheetml 6292 bytes；quote id=1 (TEMP 未来
      到期) → 200 6573 bytes；format=pdf → 400；手工改 quote id=3 valid_until=昨天 →
      export → 409（ErrQuoteExpired）。
      手工改库声明：① UPDATE customer_quote SET special_price_status=NULL WHERE id=3
      （前一轮 ApplySpecialPriceApproved 因 repo Scan bug 失败留下的脏 PENDING 回滚）；
      ② 临时改 sku40 baseline 3.0→3.5 触发 changed refresh（baseline id=344 PARAM_CHANGE），
      事后 DELETE 344 + UPDATE 336 恢复 is_current=true valid_to=NULL；
      ③ UPDATE customer_quote SET valid_until=now()-1d WHERE id=3 → 测 409 后 UPDATE 还原。
      业务表变更：仅以上 3 处；9b 业务行（change_request id=7/8 + approval_step id=12/13/14/15
      + customer_quote id=4 + items id=6/7 + event_outbox id=50 + audit id=120/121/122/123）
      均 API 驱动落库。
      ⚠️ 遗留登记：
      ① 9b-① 审批步骤 role 硬编码 ["PRICING_OP","FINANCE"]（与 8b-1-③ 同款）；未来按金额
         /客户分层需读 sys_config 或 pricing_policy。
      ② 9b-② PDF 导出本批不支持（format=pdf → 400 ErrExportFormatUnsupported）；excelize
         只做 XLSX。
      ③ 9b-③ margin_impact 字段对销售角色的 field_mask 完备性：当前 SALES 角色 M9:V 可见
         customer_quote，margin_impact 在 special-price 响应中透出 current_margin/target_margin
         ——若 SALES 也能调 M9:E 申请特价（待 F 复审），margin 可能泄露。需复审
         role_field_mask 是否要给 SALES 加 margin_impact.current_margin/target_margin 到
         hide 列表。
      ④ 9b-④ Refresh Unchanged 与 6b/7b 语义对齐（值未变不产新版，排除 reason/operator
         等运行字段）；allSame 判定按 StringFixed(8) 截断后比较，与 numeric(20,8) 落库
         口径一致。
      ⑤ 9b-⑤ SPECIAL_PRICE_REJECTED 路径未实现（本批只走 ApprovedHook；DecideReject 对
         SPECIAL_PRICE 无联动——7b-② 同根 DecideApproval 非事务问题）；
         audit SPECIAL_PRICE_REJECTED 需要时补一个 RejectHook。
      ⑥ 9b-⑥ customer_quote_item 表无 below_floor 列（000005 DDL 只有 unit_price+floor_price），
         export 时按 UnitPrice.LessThan(FloorPrice) 实时计算——契约文档 docs/api/09 §0.x
         若声明 below_floor 字段，需注明是 derived 不是 stored。
      ⑦ 9b-⑦ uk_cq_ver (customer_id, version_no) 决定 version_no 必须 per-customer 单调
         递增；RefreshTx 用 `WHERE customer_id=?` MAX+1 正确，但 refresh 后**新版本号不复用
         旧版**——quote id=1 v1 EXPIRED 后新 quote id=4 是 v4 不是 v1'。前端展示若假设
         「refresh 后还是 v1」会困惑，需在 09 契约里注明。
      ⑧ 9b-⑧ 既有 bug 同 8a⑨/8b-1⑨/8b-2⑦（idempotency result_json 落盘 NULL）：refresh
         /special-price 200 时 result_json 仍 NULL，重放拿 data:null；阶段 10 治理。
      ⑨ 9b-⑨ 同 7b-② DecideApproval 非事务：ApplySpecialPriceApproved 失败留下
         APPROVED-without-effects 脏态（本批 E2E 第一轮就踩到，change_request id=7
         status=APPROVED 但 special_price_status 卡 PENDING）。修复方向=包事务或可重试。
- [ ] 9c 客户报价剩余子项（详情/编辑/生效/合同价）

---

## 九之续：9c 客户门户 6 接口（详细日志归档）

- [x] 9c 客户门户 6 接口 — commit 9d96294
      migration 000024 customer_notification（id IDENTITY PK + customer_id FK→customer_profile +
      type varchar(32) ck(PRICE_UP/DEPRECATE/SYSTEM) + title varchar(128) + content text +
      read_at timestamptz NULL（NULL=未读）+ created_at/updated_at/request_id/created_by/updated_by；
      idx_cust_notify(customer_id, read_at, created_at DESC)）。
      6 接口（全部挂 /api/customer/*）：
      ① GET /price-book：按 customer_profile.level_code 取 price_book WHERE status='EFFECTIVE'
         + price_book_item JOIN model_sku + LEFT JOIN LATERAL price_book_component（input 优先）
         拿代表组件 unit_price。**不 SELECT floor_price / baseline_version / policy_id**——
         字段剔除裁决 6 在 SQL 层物理剔除（DTO 也不含这些 key）。
         404 分支：客户不存在 / 当前等级无生效价目表。
      ② GET /quotes：customer_quote + LATERAL agg(item_count, SUM(unit_price), MAX(currency))
         WHERE customer_id=? ORDER id DESC 分页；UNION ALL customer_price_book 行映射为
         SourceKind=CONTRACT Status=EFFECTIVE QuoteType=CONTRACT。Total=count+len(contracts)。
      ③ POST /quotes/:id/accept：幂等中间件 + 服务层校验链（存在+归属→404 / APPROVED 或
         special_price_status=APPROVED→否则 409 / valid_until 未过期→否则 409）→
         AcceptQuoteTx 单事务：UPDATE customer_quote SET status='EFFECTIVE' WHERE id=? AND
         customer_id=? AND (status='APPROVED' OR special_price_status='APPROVED')（RowsAffected=0
         → ErrPortalQuoteNotApprovable 防并发）+ INSERT customer_price_book 每 item 一行
         （contract_from=now, contract_to=valid_until 或 now+1yr, source_quote_id）+
         audit CUSTOMER_QUOTE_ACCEPTED（OperatorRole=CUSTOMER, SourceType=HUMAN）。
      ④ GET /billing：customer_profile 5 字段（credit_limit/credit_used/deposit_amount/
         deposit_status/billing_cycle）StringFixed(2)，bills=[]interface{}{} 占位（9c-①）。
      ⑤ GET /notifications：customer_notification WHERE customer_id=? ORDER created_at DESC,id DESC
         分页 + count。标记已读接口未做（9c-②）。
      ⑥ GET /home：service 先 LoadCustomerLevelCode 再透传给 repo.LoadHome(ctx, customerID, levelCode)
         （变异验证 #2 锁点）；repo 聚合 balance(4 字段) + pending_count(status IN DRAFT,PENDING) +
         unread_count(read_at IS NULL) + common_models(levelCode→EFFECTIVE price_book→
         price_book_item JOIN model_sku，只 sku_id/sku_code/currency)。
      权限裁决 7：不挂模块权限点（CUSTOMER 角色零内部权限点，RequirePerm 必 403）；
      AuthN + resolveCustomer 校验 op.OperatorType=='CUSTOMER'（否则 401）+ owner_id 行级过滤。
      单测 17 条全绿（fakePortalStore 捕获 customerID/levelCode/items/operatorID/requestID）：
      price-book found/404 客户不存在/404 无生效价目表/NoCostFields（JSON 无 7 个禁用 key）；
      quotes 行级过滤透传+合同行 SourceKind；accept happy/特价单 DRAFT+sps=APPROVED 可接受/
      他人报价 404/不存在 404/非 APPROVED 409/过期 409/无 valid_until 不判过期；
      billing 字段+bills=[] 非 nil；notifications 行级过滤+DESC 顺序；
      home 聚合+PassesLevelCodeToRepo（变异 #2 锁点）+CustomerNotFound+CommonModels 仅 3 key。
      变异验证 3 处全抓回：
      ① AcceptQuote 删 `quote.CustomerID != customerID` → NotFound_OtherCustomer 红（got nil）；
      ② GetHome 不透传 levelCode（传 ""）→ PassesLevelCodeToRepo 红（got ""）；
      ③ PortalPriceBookItem 加回 FloorPrice json:"floor_price" → NoCostFields 红。
      E2E 真库（smoke_customer/Test@1234，account id=10 portal=CUSTOMER owner_id=1）：
      login → operator_type=CUSTOMER operator_id=1 ✓；
      price-book → GLOBAL v1 USD 2 items（sku40=3.41812858, sku41=11.88914286），
      items[0] keys=sku_id,sku_code,currency,unit_price（无成本 key）✓；
      quotes → total=4（4:DRAFT:TEMP | 3:DRAFT:TEMP | 2:DRAFT:APPLY | 1:EXPIRED:TEMP）✓；
      accept quote 2 DRAFT → 409 ✓；手工 UPDATE→APPROVED → accept 200
      {quote_id=2, new_status=EFFECTIVE, contract_cnt=1}；cpb_count=1；quote2=EFFECTIVE ✓；
      同 key 重放 → code=0 result 复用（quote_id=2 status=EFFECTIVE cnt=1）✓；
      billing → credit_limit=0.00 credit_used=0.00 deposit=0.00/UNPAID cycle=30 bills=0 ✓；
      notifications → total=2 first=PRICE_UP(id=2 已读) ✓；
      home → pending=2 unread=1 common=gpt-5-2026-04-11,claude-opus-4-2026-05（仅 3 key）✓；
      row-filter → INSERT customer_id=2 quote id=7（APPROVED+1 item）→ smoke_customer accept → 404 ✓；
      未认证 /home → 401 ✓；audit id=128 CUSTOMER_QUOTE_ACCEPTED target=2 operator=1 HUMAN ✓。
      ⚠️ E2E 实际是**跑了 4 轮**才全绿（前 3 轮脚本自身 bug 中断，accept 已成功但后续步骤未跑完）：
      audit 125 (13:39:27) / 126 (13:40:08) / 127 (13:41:10) / 128 (13:43:56) 四条 CUSTOMER_QUOTE_ACCEPTED，
      request_id 各不相同（非幂等重放），对应 cpb id=1/2/3（已手工删）/ id=4（保留）。序列跳号即证据。
      第 1 轮：accept 成功（audit 125）→ 8a `INSERT ... RETURNING id` 输出多行导致 `[int64]` 转换失败，8b/8c 全错；
      第 2 轮：改两步查询后 accept 成功（audit 126）→ `SELECT MAX(id)` 经 docker exec 返回 Object[]（多空行）又失败；
      第 3 轮：修 MAX 后 accept 成功（audit 127）→ 第 7 步 `$home` 撞 PowerShell 只读内置变量，脚本崩溃；
      第 4 轮：改名 `$homeResp` 后全绿（audit 128，cpb id=4 保留）。
      手工改库声明（**完整版，复核后补全**）：
      ① tmp/9c-fixture-customer-account.sql：INSERT account (CUSTOMER,CUSTOMER,1,smoke_customer,
         复用 smoke_admin bcrypt hash Test@1234, ACTIVE)——fixture 保留；
      ② tmp/9c-fixture-notifications.sql：INSERT customer_notification ×2（customer_id=1：
         PRICE_UP 已读 read_at=now()-1d + DEPRECATE 未读）——fixture 保留（created_by/updated_by=0，
         脚本里中文经管道进 psql 会乱码，实际用 docker exec -c 英文标题插入）；
      ③ E2E 4b：UPDATE customer_quote SET status='APPROVED' WHERE id=2（E2E 驱动 accept，
         事后保留为 EFFECTIVE + cpb 1 行——这是接口的正常业务结果，不回滚）；
      ④ E2E 8a/8c：INSERT customer_quote(id=7, customer_id=2)+item → 测 404 后 DELETE（已清理）；
      ⑤ **重跑清理 ×3（原报告漏报，复核后补）**：前 3 轮脚本中断后，为把 quote id=2 重置回可测状态，
         每轮手工执行 `DELETE FROM customer_price_book WHERE customer_id=1 AND source_quote_id=2;
         UPDATE customer_quote SET status='DRAFT' WHERE id=2;`——共 3 次 DELETE cpb（id=1/2/3）
         + 3 次 UPDATE status→DRAFT（对应 audit 125/126/127 之后的清理）。
         原因：E2E 脚本自身 3 个 bug（RETURNING 多行 / docker exec 返回 Object[] / $home 只读变量）
         导致中断重跑，不是业务接口问题。副作用：cpb 序列永久缺口 id=1/2/3，
         audit 125~127 无对应 cpb 行（看似「幽灵 accept」）——**本声明即为解释**。
         教训：E2E 重跑清理也属于「手工改库」必须当场写进报告，不能事后由复核者从 audit 反推。
      ⚠️ 遗留登记：
      ① 9c-① billing.bills=[] 占位——账单明细待计费系统接入（设计文档 §12 边界外）。
      ② 9c-② 通知标记已读接口（POST /notifications/:id/read）未做——本批只读列表。
      ③ 9c-③ accept 立即转合同价，无内部确认环节——若未来需要销售复核，需加状态或审批流。
      ④ 9c-④ /home 单接口串行聚合 4 段查询——数据量大后可拆并行（errgroup）或前端并行请求。
      ⑤ 9c-⑤ 通知不自动生成——PRICE_UP/DEPRECATE 通知的生产端（涨价传导/退役连锁时
         INSERT customer_notification）未接，等阶段 10/11 事件出站时统一做。
      另：AcceptQuoteTx 的 UPDATE 带状态守卫（RowsAffected=0→409）防并发双 accept；
      customer_price_book uk_cpb(customer_id,sku_id,contract_from) 同事务多 item 不冲突
      （sku_id 不同）；contract_to 兜底 now+1yr（quote 2 valid_until=NULL 场景）。

---

## 九之续：阶段 8（8a/8b-1/8b-2）详细日志归档

- [ ] 8 定价与价目表 E
  - [x] 8a 定价策略 + 生成价目表草稿（§1/§2）— commit 56cbde5
        已落地：internal/domain/pricing/service.go（PricingService + CalculatePrice 4 种
        price_method[MARGIN=cost/(1−param) / COST_UP=cost×(1+param) / OFFICIAL_ANCHOR=
        official×param / FIXED=param] + MatchPolicy[ALL/VENDOR/FAMILY/SKU 命中 + level_code
        叠加过滤 + priority 最小优先不叠加 + 非 ACTIVE 跳过] + ceil8[rounding_rule='CEIL'
        向上取整 8 位] + GenerateDraft 编排[无命中策略/无成本基线/OFFICIAL_ANCHOR 无官方价
        →跳过]）+ internal/repo/pricing.go（策略 CRUD + LoadSKUContexts[PUBLISHED 在架] +
        LoadCurrentUnitCosts[representCompJoin 同一份 SQL 片段取代表组件 unit_cost+basis+
        version] + LoadCurrentOfficialPrices[LATERAL input 优先] + LoadOldPrices[上一
        EFFECTIVE 版本] + SaveDraft 单事务：price_book[DRAFT, version_no=MAX+1, created_by
        NOT NULL] + price_book_item + price_book_component[代表组件类型透传不硬编码]）+
        api/pricing.go（4 接口 + swagger）+ router/main 装配。
        单测 24 条全绿；变异验证 4 处全抓回：① MARGIN /(1−param)→/param → 2.94→16.67 红；
        ② COST_UP ×(1+param)→×param → 2.75→0.25 红；③ priority 最小优先反转 → 命中 id=2
        →id=1 红；④ floor <→<= → 边界==被误判违规红。
        E2E 真库验收（smoke_admin）：创建策略 id=1（GLOBAL-10 COST_UP 0.10 ALL priority=1
        rounding_rule 自动 'CEIL'）；列表 total=1；草稿 draft_id=1 item_count=2 blocked=2——
        sku40 new=2.63195900（2.39269000×1.10）floor=2.81492942（2.39269000/0.85 ceil8）
        violation=True、sku41 new=9.15464000（8.32240000×1.10）floor=9.79105883 violation=True；
        old_price/delta_pct 空（首次生成）；幂等重放 draft_id=1 相同；缺幂等键→400；
        无 token→401。DB 落库核验：price_book id=1 version_no=1（MAX+1 非 0）status=DRAFT
        created_by=1 has_diff=t；price_book_item sku40 floor=2.81492942 baseline_version=12 /
        sku41 floor=9.79105883 baseline_version=3；price_book_component input 2.63195900 /
        9.15464000。第二次草稿 id=2 version_no=2 递增**未撞 uk_pb_ver**（实证约束 2 裁决正确）。
        ⚠️ 与提示词 E2E 锚点偏差：提示词写 sku40 floor=2.81580941，实际 2.39269000/0.85=
        2.81492941... ceil8 向上=2.81492942——提示词锚点算错，以真库计算为准。
        ⚠️ 手工改数声明：**零改数**（全部写新表 pricing_policy/price_book/price_book_item/
        price_book_component，未动 cost_baseline/price_version 等既有业务表）。
        ⚠️ 遗留登记：
        ① 8a-① rounding_rule（DDL NOT NULL DEFAULT 'CEIL'）本批落了默认值且按 CEIL 8 位
           向上取整实现，但未支持其他取整规则（FLOOR/ROUND 等）——如需多规则再扩。
        ② 8a-② price_method=TIERED（阶梯）DDL 无此枚举，代码拒绝非 4 种（ErrInvalidPriceMethod）。
        ③ 8a-③ price_book_item.baseline_version 与契约 cost_baseline_id 不一致（以表结构
           为准——版本号可定位 cost_baseline 的 sku_id+version）。
        ④ 8a-④ 策略优先级冲突消解已定：多条命中只取 priority 最小，不叠加（待裁决点 1）。
        ⑤ 8a-⑤ scope_type=MODEL_TYPE 本批不支持（scope_id bigint 与 model_type varchar
           类型不匹配），ErrModelTypeUnsupported→400。
        ⑥ 8a-⑥ pricing_policy.floor_rule DDL 无此列——floor 校验在生成草稿时用 floor_price
           比较；是否加 floor_rule 列（BLOCK/WARN/ALLOW）待产品确认。
        ⑦ 8a-⑦ 契约 0.1 字段名（markup_type/markup_value/floor_rule）与真实 DDL
           （price_method/param_value/rounding_rule）不一致——已按 DDL 为准，契约文档
           docs/api/08-pricing.md §0.1/§1 待更新。
        ⑧ 8a-⑧ price_book_component 本批只落代表组件一行（非逐组件全量）——契约 0.2 说
           "逐组件售价"，如需全组件售价（每组件独立定价）待产品确认后扩展 DraftItem。
        ⑨ 8a-⑨ 既有 bug 顺带观察：idempotency result_json 落盘 `failed to parse field:
           ResultJSON` 仍在（6d-3 遗留③同款），重放返回 data 正常但落盘快照为 NULL——
           与 6d-2/6d-3 同根，阶段 10 治理。
  - [x] 8b-1 发布 + 回滚 — commit 612ae4c
        已落地：migrations/000021（change_request.sku_id DROP NOT NULL，裁决 A1——非 SKU
        粒度变更允许 SKU=NULL）+ internal/domain/pricing/publish_service.go（Publish/
        Rollback + ErrInvalidMode/ErrScheduledNotSupported/ErrGrayNotSupported/
        ErrFloorViolation/ErrRollbackToSelf/ErrRollbackTargetNotFound/ErrPriceBookNotDraft/
        ErrPriceBookNotEffective；payload {price_book_id, sku_ids, effective_time, mode}，
        rollback 加 target_version_no + reason；Rollback 重校验目标价 vs 当前 floor——
        LoadCurrentUnitCosts + LoadMinGrossMargin + cost.Floor + ceil8）+ internal/repo/
        pricing_publish.go（Publish 单事务 3 写：price_book DRAFT→APPROVING + change_request
        [map Create 绕 GORM 零值 int64 不写 NULL；sku_id=nil / risk_level="MID" /
        created_by="staff:%d" / request_id=幂等 key] + 2 条 approval_step[PRICING_OP,
        FINANCE]；crID 用 SELECT id FROM change_request WHERE request_id=? ORDER BY id
        DESC 回填；ROLLBACK 走 copyPriceBookVersion[nextVer=MAX+1, rollback_of=target.
        VersionNo, items+components 全量复制]；ApplyPriceBookPublish 单事务 5 写：book
        APPROVING→EFFECTIVE(+effective_time) + 旧 EFFECTIVE 同 level_code 置 RETIRED +
        valid_to=effective_time + event_outbox('price.effective') + cache_version
        ('price_book')+1 + audit PRICE_BOOK_PUBLISH 或 PRICE_BOOK_ROLLBACK[payload.target_
        version_no>0 时切 rollback action]）+ api/pricing_publish.go（publishBody/
        rollbackBody + pricingPublishErrToAppErr[404/409/400 三档] + 完整 swagger 注解）
        + router 挂 2 路由（M7:E + Idempotency）+ main.go ApprovedHook 第二分支
        （PRICE_BOOK_PUBLISH/PRICE_BOOK_ROLLBACK → ApplyPriceBookPublish）。
        单测 17 条全绿（fakePublishStore maps：books/items/components/unitCosts + margin=0.15，
        ErrSkuIDNull 通过断言 approval role=["PRICING_OP","FINANCE"]+sku_id NULL 间接钉死）；
        变异验证 3 处全抓回：① Publish floor_violation 校验 if false → TestPublish_FloorViolation
        红；② Rollback 删除 floor 校验（+`_ = uc`, `_ = margin` 编译通过）→ TestRollback_FloorViolation
        红；③ SCHEDULED 被允许 → TestPublish_ScheduledNotSupported + TestPublish_EffectiveTimeFuture
        双红。
        E2E 真库验收（smoke_admin）：policy id=2 8B1-E2E-MARGIN30（MARGIN 0.30 ALL priority=99
        ACTIVE）；draft id=3 GLOBAL v1（blocked=0，floor 通过）；publish IMMEDIATE → cr_id=6
        step_count=2，price_book id=3 → APPROVING；DB 核验 change_request id=6 sku_id NULL
        change_type=PRICE_BOOK_PUBLISH status=PENDING created_by=staff:1；approval_step
        step_no=1 role=PRICING_OP（已被批）+ step_no=2 role=FINANCE；approve step1 200
        final_status=PENDING；step2 同操作人 → 403 自批拦截；SCHEDULED/GRAY → 10001
        （参数校验失败，先于 book status 校验）；no-token → 401；missing-key → 10001 参数校验
        （幂等中间件前置）。
        ⚠️ 手工改数声明：**零改数**（migration 000021 是 schema 变更非数据；policy/draft/
        publish/approve 全部 API 驱动；幂等键 8b1-e2e-policy-001 / draft-001 / publish-001 /
        ap-s1-001 / ap-s2-001 / sched-001 / gray-001 落库；interim 误触交互终端两次 400
        是幂等中间件直接拒，无业务行产生）。
        ⚠️ 遗留登记：
        ① 8b-1-① change_request.sku_id DROP NOT NULL 是单向迁移（down.sql 想 SET NOT
           NULL 会失败如果已有 NULL 行）；下游跨域影响见 000021 注释。
        ② 8b-1-② Publish 自身不写 audit_log（仅 ApplyPriceBookPublish 写）——7b confirm
           同款约定（confirm 也不写 audit，approve 生效时写）；如需 publish 立即留痕，
           PublishTx 内补一条 audit(action=PRICE_BOOK_PUBLISH_SUBMIT, before/after=
           status 迁移)。
        ③ 8b-1-③ 审批步骤 role 列表硬编码 ["PRICING_OP","FINANCE"]（裁决 B2 两步静态）；
           未来若按金额/客户分层，改成读 sys_config 或 pricing_policy 上的列。
        ④ 8b-1-④ ApplyPriceBookPublish 里 cache_version 现在 upsert 'price_book'（无
           scope_key 细分）；若 F 阶段需要 level_code 维度分粒度缓存，需扩展 cache_version
           键结构或加 scope_key。
        ⑤ 8b-1-⑤ Rollback 未要求 reason（rollbackBody.reason 必填但 service 只校验非空）；
           reason 写进 payload 但**不进 audit_log.message**——后续如需在审计查看回滚
           原因，需补一个列或 audit_message。
        ⑥ 8b-1-⑥ SCHEDULED 预约生效的 ticker 与 5b/7b 同类（待 worker 分钟级调度统一）；
           GRAY 整批留待后续 Stage。
        ⑦ 8b-1-⑦ 拒绝后 price_book 状态机未细抠（当前 REJECTED → DRAFT 是否允许回填
           / 是否新建版本）待产品确认；本批 Approve 路径只处理了 PENDING→APPROVED，未实现
           DecideReject 对 price_book 的回写。
        ⑧ 8b-1-⑧ change_request.created_by 用 "staff:%d" 格式（staff:1），与早期 7b 用
           "PLATFORM_ADMIN"/"MODEL_OPS"（角色字符串）不一致——8b-1 是首批 staff: 前缀，
           阶段 F 治理 operator_id 命名空间撞号时统一。
        ⑨ 8b-1-⑨ 既有 bug 同 8a-⑨（idempotency result_json 落盘 NULL）——publish 200 时
           result_json 仍写不进，重放拿 data:null；与 6d-3③/6d-2/8a⑨ 同根，阶段 10 治理。
  - [x] 8b-2 涨价传导决策队列 — commit 02dfd68
        已落地：migrations/000022（price_upconduction 表：CHECK(status IN
        PENDING/FOLLOWED/NOT_FOLLOWED) + idx_price_upconduction_status(status, created_at DESC) +
        idx_price_upconduction_sku(sku_id, level_code)；同事务 UPDATE SALES role field_mask
        加 margin_before/margin_after）+ internal/domain/pricing/upconduction_service.go
        （UpconductionService + GenerateQueue[rising pairs → 每 SKU×level 一行 / dedup 锚点
        (sku_id,level_code,cost_after) IN (PENDING,FOLLOWED) / 无 EFFECTIVE 价目表 SKU →
        SkippedSKUIDs 不静默吞] + Decide[PENDING 校验 + NOT_FOLLOW 护栏：price_current <
        floor_price → ErrUpconductionBelowFloor 409 必须走特价审批] + 纯函数
        CalculateCostDeltaPct[before<=0 报错] / CalculatePriceSuggested[=price_current×
        (1+delta)] / CalculateMargin[price<=0 报错] + DefaultFrozenDays=7 + frozen_until=
        now+7d）+ internal/repo/pricing_upconduction.go（LoadRisingCostSKUs[representCompJoin
        + LATERAL 取上一版本 input 优先组件 unit_cost，WHERE cur>prev] / LoadEffectivePrice
        [price_book EFFECTIVE + item(sku) + LATERAL component input 优先] /
        InsertQueueRow[dedup + 单事务 INSERT + audit PRICE_UPCONDUCTION_GENERATE] /
        ListQueue / LoadQueueRowByID / Decide[条件 UPDATE WHERE status='PENDING'
        RowsAffected==0 → NotPending + audit PRICE_UPCONDUCTION_DECIDE]）+
        api/pricing_upconduction.go（3 接口 + pricingUpconductionErrToAppErr[404/409/400/500]）+
        router（M7:V list / M7:E generate+decide 挂 Idempotency）+ main 装配 + swagger。
        单测 14 条全绿（fakeUpcStore：delta 0.200000/floor 3.52941176/mb 0.090909/ma -0.090909/
        frozen +7d 精确/dedup/multi-level/suggested 锚点/FOLLOW 落库/NOT_FOLLOW below-floor
        拦截/NotPending 409/InvalidAction/override_price 透传）；变异验证 3 处全抓回：
        ① CalculateCostDeltaPct 删 .Div(costBefore) → Normal 红（0.1→1）；②
        CalculatePriceSuggested 删 one.Add → UsesDelta 红（14.40→2.40）；③ frozenUntil=now
        → GenerateOneRow 红（09-16 vs 09-23）。
        E2E 真库（smoke_admin + smoke_sales，迁移 v22 已应用）：
        generate（幂等重放 gen-001）dedup count=2 skipped=[]（rows 1,2 已 PENDING 被跳过）；
        list admin total=2 全字段对——sku40(gpt-5) delta=0.253819(=(3−2.39269)/2.39269)
        sug=4.28571430(=3.41812858×1.253819) floor=3.52941176(=3/0.85) mb=0.300000
        ma=0.122327、sku41(claude-opus-4) delta=0.081419 cur=11.88914286 sug=12.85714286
        floor=10.58823529 mb=0.300000 ma=0.243007；list sales total=2 margin_before/after
        物理不存在（PSObject.Properties.Name has=False）；below-floor NOT_FOLLOW sku40
        → 10005 "NOT_FOLLOW 后售价低于 floor（红线），必须走特价审批"；NOT_FOLLOW sku41
        → NOT_FOLLOWED decided_by=1；FOLLOW sku40 → FOLLOWED；re-decide 同 body 异 key →
        10005 幂等冲突（DefaultBizKey dedup 先到）；异 body 异 key → 10005 "仅 PENDING 状态
        可决策"（NotPending 真值）；no-token → 10002；invalid decision → 10001；unknown id
        → 10004；same-key replay → 200 status=FOLLOWED（既有 result_json NULL bug 不影响
        状态）。DB 核验：price_upconduction 2 行字段全对；audit_log 4 行
        PRICE_UPCONDUCTION_GENERATE×2(operator_role=PRICING_OP) + PRICE_UPCONDUCTION_DECIDE×2
        (operator_role=PLATFORM_ADMIN——op.Roles[0]，smoke_admin 首角色是 PLATFORM_ADMIN)。
        ⚠️ 手工改数声明：① smoke_finance fixture（tmp/8b2-fixture-finance.sql，staff
        13900000099 + account id=8 + role_grant FINANCE）——8b-1 E2E 必需的 FINANCE 审批者
        补建（8b-1 当时缺该账号）；② cost_baseline 版本推进（tmp/8b2-bump.sql）——关闭
        sku40 v12(id=183)/sku41 v3(id=268)，插入 v13(id=336, input=3.00000000)/
        v4(id=337, input=9.00000000 output=62.41800000 未动)，change_reason=PARAM_CHANGE
        request_id='8b2-e2e-bump-40/41'——**这是手工模拟成本上涨事件触发涨价传导**；
        ③ price_book id=3 GLOBAL v1 → EFFECTIVE 是 API 驱动（smoke_finance approve step2，
        key 8b2-e2e-ap-s2-001），非手工 SQL；④ 8b-2 的 queue 行 id=1,2 与 audit 行
        id=111~114 都是 API 驱动落库，非手工。
        ⚠️ 遗留登记：
        ① 8b-2-① **自动生成与超时 worker 扫描**：GenerateQueue 目前只能人工触发
           （POST /generate）；未来需在 cost_baseline 生效后自动入队 + worker 扫描
           frozen_until<now 的 PENDING 行做"超时未决策"告警（与 5d 报价到期扫描同骨架）。
        ② 8b-2-② **FOLLOW 自动发布**：Decide FOLLOW 当前只改状态，不联动价目表发布；
           真正的"跟涨生效"需 8b-3 或后续把 FOLLOW → 生成新价目表草稿 → 走 publish 管道。
        ③ 8b-2-③ **price_suggested 算法备选**：当前=price_current×(1+cost_delta_pct)
           （按涨幅等比传导）；备选口径（如新成本×(1+目标毛利)）待产品确认后扩展。
        ④ 8b-2-④ **margin 剔除靠 SALES mask**：000022 给 SALES 加 margin_before/after 到
           hide 列表；**未来新角色（如 CUSTOMER 若给 M7:V）必须同步补 mask**，否则
           margin 泄漏——阶段 F 治理「角色权限过宽 + mask 完备性」时一并复审。
        ⑤ 8b-2-⑤ **冻结期 DefaultFrozenDays=7 硬编码**：未走 sys_config；如需按客户/级别
           差异化，需扩展配置。
        ⑥ 8b-2-⑥ **审计 operator_role 用 op.Roles[0]**（smoke_admin 首角色 PLATFORM_ADMIN，
           不是 PRICING_OP）——操作语义上有点错位，阶段 F 统一「多角色操作归属」口径。
        ⑦ 8b-2-⑦ **既有 bug 同 8b-1-⑨**（idempotency result_json 落盘 NULL）：generate/decide
           200 时 result_json 仍 NULL，重放拿 data:null——8b-2 E2E 实测 same-key replay
           状态正确（FOLLOWED）但 data 为 null；与 6d-2/6d-3/8a/8b-1 同根，阶段 10 治理。
        ⑧ 8b-2-⑧ **本批清零了 11 条存量 lint**（6d/7b/8a/8b-1 期间 golangci-lint 本地
           工具链 go1.24 < 项目 go1.26 损坏漏检；本批用 `go install golangci-lint v2.13.2`
           重编 go1.26 版后修复）：errorlint(err==→errors.Is)×2 / fmt.Errorf %v→%w×1 /
           unused 死代码（cacheVersionRow + upconductionRowToItem）×2 / revive stutter
           nolint（PricingService/Store/Policy 改名属破坏性重构，nolint 挂起）×3 /
           indent-error-flow×1 / exported 注释 nolint×2。**CI 必须钉 golangci-lint 版本**
           与 go.mod go 指令对齐，否则同类漏检会再发生。
---

## 10a ����ۺ� + ָ�꿨��commit 27353ed��

### �ӿ�
- GET /api/internal/workbench/todos���м�����+ȥ��+deeplink��
- GET /api/internal/workbench/metrics������ɫ�ü���Ƭ��

### Ȩ�޲þ� 10
����ģ��Ȩ�޵㣬AuthN + ��ɫ�Զ��ü���PLATFORM_ADMIN ��ȫ����������ɫ�� assignee_id=operatorID ���ˡ�

### �ֶ��޳��þ� 9
SQL �㲻 SELECT + DTO �������� unit_cost/floor_price/margin/baseline��

### �� floor �ж�
unit_price < floor���ϸ�С�ڣ�price==floor ����Υ�棩��

### ��Ƭ
- PROCUREMENT 4 ����proc_pending_quotes / proc_effective_this_month / proc_expire_30d / proc_single_dep
- PRICING_OP 3 ����pricing_pending_books / pricing_floor_violations / pricing_pending_upconduction
- SALES 3 ����sales_my_customers / sales_pending_quotes / sales_quarterly_deal��ռλ 0.00��
- FINANCE 2 ����fin_fx_pending_month��ռλ 0��/ fin_deposit_unpaid

### ���� 16 ����
- TestListTodos_PROCUREMENT_onlyOwn / PLATFORM_ADMIN_seeAll / Dedup / PaginationBounds / DeeplinkFor
- TestListMetrics_PROCUREMENT_4Cards / PRICING_OP_3Cards / SALES_3Cards / FINANCE_2Cards / MultiRole_union
- TestListMetrics_FloorViolation_boundary / belowCount / AllCards_haveMetadata / DTO_no_cost_fields
- TestListTodos_StoreError / TestListMetrics_BaselineLoadError_belowFloor

### ���� 3 ��ȫץ��
1. ListTodos ownerScope Ӳ false �� TestListTodos_PROCUREMENT_onlyOwn FAIL��ץ�أ�
2. price.LessThan(floor) �� LessThanOrEqual �� TestListMetrics_FloorViolation_boundary FAIL��ץ�أ�
3. ȥ�� PROCUREMENT ��ɫ gating �� TestListMetrics_SALES_3Cards FAIL��ץ�أ�

### E2E ʵ��
- smoke_admin todos OPEN��total=6 list_len=5��id=21/22 ͬ biz_id=24 priority=MID ȥ�� �� ֻ�� 21��id=24/25 ͬ biz_id=26 �� priority ��ͬ �� ��������
- buyer_a todos OPEN��total=6 list_len=5��assignee_id=4 ȫ�� 6 �� OPEN todo��
- buyer_b todos OPEN��total=0���м�������ȷ��
- smoke_admin metrics��PRICING_OP 3 ����pending_books=2 / floor_violations=1 / upconduction=0��
- buyer_a metrics��PROCUREMENT 4 ����pending_quotes=0 / effective_month=1 / expire_30d=0 / single_dep=1��
- smoke_finance metrics��FINANCE 2 ����fx_pending=0 / deposit_unpaid=2��
- smoke_sales metrics��SALES 3 ����my_customers=0 / pending_quotes=0 / quarterly_deal=0.00��
- DTO �޳ɱ��ֶΣ�unit_cost/floor_price/margin/baseline ȫ false
- δ��֤ 401

### �ֹ��Ŀ�����
�ޣ�ֻ���ӿڣ��� fixture����

### ���� 10a-��..��
- 10a-�� ָ�꿨���棨������������붨ʱ����+���ܱ����� 10c worker ticker��
- 10a-�� ����"�����ȳɽ���"ռλ���޳ɽ�ϵͳ������ 0��
- 10a-�� �� floor ���۸�ھ�������ȷ�ϣ�vs ��������ھ���
- 10a-�� �����м�����Ȩ�޵㶨�壨�׶� 10 ȫ������ʱ�����ң�
- 10a-�� ����"�����������·�"ռλ�������¶�����δʵ�֣�

---

## 10b �澯���� + �����־��ѯ������commit 3d9642c��

### �ӿ��嵥��4 ����

| ���� | ·�� | Ȩ�� | ˵�� |
| --- | --- | --- | --- |
| GET | /api/internal/alerts | M12:V | �澯�б���severity/status/alert_type ɸѡ + ��ҳ�� |
| POST | /api/internal/alerts | M12:E + �ݵ� | �澯������HANDLE/RESOLVE/IGNORE/TO_TICKET ״̬������̬ 409��TO_TICKET �� todo_task�� |
| GET | /api/internal/audit-logs | M12:V | �����־��ά��ѯ��action/target_type/target_id/operator_id/operator_role/source_type/time_range��+ operator_name ���� |
| GET | /api/internal/audit-logs/export | M12:V | ���� CSV��UTF-8 BOM��/ XLSX������ 10000 �� |

### �þ�

1. **Ȩ��ӳ��**����Լ F:V/F:E �� M12:V/M12:E��M12 �ǹ���̨/���ģ�飬�� 10-workbench-audit.md ��1����
2. **������**�������־�������м����ˣ�ȫ���ɼ���������ԣ���
3. **operator_name ����**��
   - SYSTEM �� operator_id=0 �� "ϵͳ"
   - CUSTOMER �� customer_profile �� legal_subject.legal_name
   - SUPPLIER �� supplier_profile �� legal_subject.legal_name
   - ���� �� internal_staff.name
   - ȱʧ �� "Ա��(id=N, role=X)"
4. **��ҳ����**��page>=1��size 1~100 Ĭ�� 20��
5. **״̬��**����4.2����
   - HANDLE �� HANDLING, handle_note
   - RESOLVE �� RESOLVED, handle_note, resolved_at=now
   - IGNORE �� IGNORED, handle_note, resolved_at=now
   - TO_TICKET �� ״̬���䣬���� todo_task��biz_type='ALERT'��assignee_id=operatorID��assignee_role=operatorRole��
   - ��̬��RESOLVED/IGNORED���� 409���ظ�������
   - TO_TICKET ǿ�� create_todo=true�������������壩
6. **��������**��10000 �У����� 400����

### ���⣨14 ���̣�

**alert_service_test.go��8 ����**��
- TestListAlerts_Pagination��page=0��1, size=0��20
- TestListAlerts_Filter��severity/status/alert_type ͸��
- TestHandleAlert_NotFound���澯������ �� ErrAlertNotFound
- TestHandleAlert_Terminal����̬ �� ErrAlertTerminal������ #1 ê�㣩
- TestHandleAlert_Handle��HANDLE �� HANDLING
- TestHandleAlert_Resolve��RESOLVE �� RESOLVED
- TestHandleAlert_ToTicket��TO_TICKET ǿ�� create_todo=true
- TestHandleAlert_InvalidAction��action �Ƿ� �� ErrAlertActionInvalid
- TestHandleAlert_WithNow���Զ��� now

**audit_service_test.go��6 ����**��
- TestListAuditLogs_Pagination��page=0��1, size=0��20
- TestListAuditLogs_Filter��������͸����action/target_type/target_id/operator_id/operator_role/source_type/from/to��
- TestExportAuditLogs_CSV��CSV ������UTF-8 BOM��
- TestExportAuditLogs_XLSX��XLSX ����
- TestExportAuditLogs_OverLimit���� 10000 �� �� ���������� #2 ê�㣩
- TestExportAuditLogs_TimeRange��ʱ�䷶Χ͸��������ҿ���

**workbench_alert_test.go��repo �㣬1 ����Ч + 4 �� skip��**��
- TestResolveOperatorName_System��SYSTEM �� "ϵͳ"������ #3 ê�㣩
- ���� 4 �� skip������⣬E2E ���ǣ�

### ������֤��3 ��ȫץ�أ�

1. **��̬�ж�**��ע�͵� if alertTerminalStatus[alert.Status] �� TestHandleAlert_Terminal �죨panic: fakeAlertStore.HandleAlertTx nil����
2. **��������**��ע�͵� if total > 10000 �� TestExportAuditLogs_OverLimit �죨err=<nil>, want �������� 10000����
3. **operator_name ����**���� eturn "ϵͳ" �� eturn "" �� TestResolveOperatorName_System �죨name=, want ϵͳ����

### E2E ʵ�⣨tmp/10b-e2e.ps1��

**ǰ��**����¼ alert ��ʼ״̬��tmp/10b-alert-initial-state.txt����
- id=1 QUOTE_EXPIRE CRITICAL OPEN
- id=2 QUOTE_EXPIRE HIGH OPEN
- id=3 QUOTE_EXPIRE CRITICAL OPEN
- id=4 RETRO_LIMIT HIGH OPEN
- id=5 QUOTE_ANOMALY HIGH OPEN
- id=6 QUOTE_EXPIRE CRITICAL OPEN
- id=7 QUOTE_ANOMALY HIGH OPEN
- id=8 QUOTE_EXPIRE HIGH OPEN
- id=9 QUOTE_EXPIRE CRITICAL OPEN

**����**��
1. ��¼ smoke_admin �� token
2. GET /alerts?status=OPEN �� total=9, list_len=9
3. GET /alerts?severity=CRITICAL �� total=4��id 1,3,6,9��
4. GET /alerts?alert_type=QUOTE_ANOMALY �� total=2��id 5,7��
5. POST /alerts HANDLE alert_id=1 �� 200, status=HANDLING
6. POST /alerts RESOLVE alert_id=2 create_todo=true �� 200, status=RESOLVED, todo_id=28
7. POST /alerts �ظ����� alert_id=2 �� 409
8. GET /audit-logs?page=1&size=5 �� total=81, items_len=5��operator_name �ǿգ�ð�̹���Ա/?????9a��
9. GET /audit-logs?action=CUSTOMER_QUOTE_ACCEPTED �� total=4��id 125-128��
10. GET /audit-logs/export?format=csv �� 200, text/csv; charset=utf-8, 22666B
11. GET /audit-logs/export?format=xlsx �� 200, application/vnd.openxmlformats-officedocument.spreadsheetml.sheet, 15188B
12. GET /audit-logs/export ȱ from �� 400
13. δ��֤ �� 401

**��֤**��
- alert id=1 �� HANDLING, handle_note=handling
- alert id=2 �� RESOLVED, handle_note=resolved, resolved_at=2026-09-17 17:36:43
- todo_task id=28 �� biz_type=ALERT, biz_id=2, assignee_id=1, assignee_role=PLATFORM_ADMIN, status=OPEN
- audit_log id=130/131 �� action=ALERT_HANDLE, operator_id=1, operator_role=PLATFORM_ADMIN, source_type=INTERNAL, source_id=staff:1

### �ֹ��Ŀ�����

�ޣ�ҵ��ӿڱ�������ֹ� SQL����

### ����

- 10b-�� ��Լ F:V/F:E �� M12:V/M12:E �ĵ�������10-workbench-audit.md ��1 Ȩ�޵㶨�壩
- 10b-�� operator_id �����ռ��ͻ������0=ϵͳ���� / NULL=�˹����� + staff:N ǰ׺��񣬽׶� F ͳһ��
- 10b-�� todo_task.biz_type='ALERT' ��ö�� DDL ע�ͣ�000006_flow_audit.up.sql �貹�䣩
- 10b-�� todo ָ������δʵ�֣���ǰ assignee_id=operatorID����֧��ָ�����ˣ�
- 10b-�� ���� 10000 ���� + �������Դ��������� 400��δ���������첽���� + ��������

---

## 10c audit_log operator_id ������ + ���commit 7090353��

### Ǩ�� 000025_audit_operator_split

**up.sql**��
`sql
ALTER TABLE audit_log
  ADD COLUMN internal_operator_id bigint NULL,
  ADD COLUMN subject_operator_id bigint NULL;

UPDATE audit_log SET internal_operator_id = operator_id
WHERE operator_role IN ('STAFF', 'PLATFORM_ADMIN', 'MODEL_OPS', 'PRICING_OP', 'PROCUREMENT', 'SALES', 'FINANCE', 'OPS_ADMIN', 'RETRO_OP', 'AUDIT_READONLY')
  AND operator_id IS NOT NULL;

UPDATE audit_log SET subject_operator_id = operator_id
WHERE operator_role IN ('CUSTOMER', 'SUPPLIER', 'INTERNAL')
  AND operator_id IS NOT NULL;

COMMENT ON COLUMN audit_log.operator_id IS '�ѷ������ڲ���ɫ�� internal_operator_id���ⲿ��ɫ�� subject_operator_id��SYSTEM �� operator_id=0';
`

**down.sql**��
`sql
ALTER TABLE audit_log DROP COLUMN internal_operator_id;
ALTER TABLE audit_log DROP COLUMN subject_operator_id;
`

### �þ�

1. **ѡ��������С���ѡ�� A��**���ᣬֻ��� 2 �� + Ǩ�����ݡ�ǰ����Ķ���operator_id �����������ֶΣ������ݣ���
2. **Ǩ�ƻ�� operator_role ����**���ڲ���ɫ �� internal_operator_id���ⲿ��ɫ �� subject_operator_id��SYSTEM/operator_id=0 �� ���� NULL��
3. **д��� AuditRepo.Record ͳһ���**��auditLogRow �������У�Record �� operator_role ���ɡ�
4. **�����Ȳ�����**��internal_operator_id �� NULL �� internal_staff.name��subject_operator_id �� NULL �� legal_subject.legal_name��operator_id=0 �� ϵͳ���鲻�� �� δ֪(id=N, role=X)��
5. **operator_id ײ���ǡ����������ǡ�����**���������и������֣��׶� F ������ͳһ�������

### ���⣨15 ���̣�

**audit_operator_split_test.go��15 ����**��
- TestIsInternalRole��14 ������STAFF/PLATFORM_ADMIN/MODEL_OPS/PRICING_OP/PROCUREMENT/SALES/FINANCE/OPS_ADMIN/RETRO_OP/AUDIT_READONLY �� true��CUSTOMER/SUPPLIER/INTERNAL/SYSTEM �� false
- TestResolveOperatorName_System��SYSTEM �� "ϵͳ"

**skip������⣬E2E ���ǣ�**��
- TestAuditRecord_TwoColumns
- TestResolveOperatorName_TwoColumns
- TestResolveOperatorName_Unknown

### ������֤��2 ����

1. **isInternalRole �ĳ��ⲿ��ɫ**��case "CUSTOMER", "SUPPLIER", "INTERNAL" �� TestIsInternalRole �죨14 �� FAIL����
2. **internal_operator_id �� legal_subject**��	x.Table("legal_subject") �� E2E ���ǣ����� operator_name �������󣩡�

### E2E ʵ��

**Ǩ��ǰ�������Ա�**��
- Ǩ��ǰ��81 ��
- Ǩ�ƺ�81 �У�schema �����д���ݣ�

**������**��
`
 operator_role  | total | internal_cnt | subject_cnt 
----------------+-------+--------------+-------------
 CUSTOMER       |     4 |            0 |           4
 INTERNAL       |     2 |            0 |           2
 PLATFORM_ADMIN |    20 |           20 |           0
 PRICING_OP     |     2 |            2 |           0
 PROCUREMENT    |    28 |           28 |           0
 STAFF          |     8 |            8 |           0
 SUPPLIER       |     3 |            0 |           3
 SYSTEM         |    14 |            0 |           0
`

**д����֤**��
- accept quote id=3 �� audit_log id=133��operator_id=1, operator_role=CUSTOMER, internal_operator_id=NULL, subject_operator_id=1

**������֤**��
- GET /audit-logs?page=1&size=5��
  - id=133 operator_id=1 operator_name=?????9a role=CUSTOMER action=CUSTOMER_QUOTE_ACCEPTED
  - id=131 operator_id=1 operator_name=ð�̹���Ա role=PLATFORM_ADMIN action=ALERT_HANDLE
  - id=130 operator_id=1 operator_name=ð�̹���Ա role=PLATFORM_ADMIN action=ALERT_HANDLE
  - id=128 operator_id=1 operator_name=?????9a role=CUSTOMER action=CUSTOMER_QUOTE_ACCEPTED
  - id=127 operator_id=1 operator_name=?????9a role=CUSTOMER action=CUSTOMER_QUOTE_ACCEPTED

### �ֹ��Ŀ�����

- UPDATE customer_quote SET status='APPROVED' WHERE id=3��E2E ����׼����1 �Σ�

### ����

- 10c-�� qual-expire-scan ������Դ��ȫ�������ʱ����������׶� 11
- 10c-�� operator_id ��ʵײ��δ����������������������и�������ûͳһ������������׶� F
- 10c-�� todo_task.biz_type='ALERT'��10b ��ö��ֵ��DDL ע��δ���£�
- 10c-�� audit_log.operator_id ������operator_role ����Ľ�ɫ�ַ�����0.x���Ĵ����ڽ׶� F ����


---
## 11a ���Žӿ� 7 ���ӿ� �� commit dd186a0

### �ӿ��嵥

1. POST /api/open/auth/token �� client_id+secret �� 1h token��sys_config ���ã�
2. GET /api/open/aliases?since= �� �汾���ˣ�ȫ������
3. GET /api/open/sellable-models �� PUBLISHED/PURCHASABLE ��������
4. GET /api/open/routing/{sku} �� primary=�ɱ���������Ӧ�̣�backups=����������
5. GET /api/open/price-book?level= �� ��ǰ��Ч��Ŀ��
6. GET /api/open/cost-snapshot?sku=&asOf= �� �ۺ� unit_cost���������ϸ
7. GET /api/open/events?since=&limit= �� 30s ����ѯ��event_outbox ����

### �þ�

- �þ� 2�����Žӿڶ�����Ȩ��OpenAuthN�������� AuthN/RequirePerm/Idempotency��token ���Ľ��·�һ�Σ�����ֻ�� sha256 hex��
- �þ� 6/7���ֶ��޳��� SQL �㡪���� SELECT cost/margin/floor_price/calc_snapshot/supplier_cost��DTO ���������ڲ��ֶΡ�
- �þ� 8��sellable-models �������ڹ��� = IN ('PUBLISHED','PURCHASABLE')��ACTIVE �����ڣ������� 11a-�ۣ���

### ����

17 ���̣�auth 4 + aliases 3 + routing 4 + cost-snapshot 3 + events 2 + sellable-models 1����

### ������֤

1. aliases �� unit_cost �� TestGetAliases_SinceLessThanVersion �죨�ֶ��޳�ʧЧ��
2. events 30s��3s �� TestPullEvents_TimeoutEmpty ���岻�䣨mock ���� now������ʵ��ʱ�� E2E ��֤
3. token ���ڼ�� �� E2E ʵ�� 401��repo �� expires_at > now()��

### E2E ʵ��ֵ

- token exchange: code=0 token_len=64 expires_at=2026-09-17T15:07:06Z
- wrong secret: 401
- aliases: version=1 items=3��model_alias ��ʵ������
- aliases since=1: items=0
- sellable-models: items=5��sku_id=7/40/41/42/43��
- routing/40: primary=2 weight=100 backups=1��supplier=1 weight=0��
- price-book GLOBAL: version_no=1 items=2��sku40=3.41812858, sku41=11.88914286��
- cost-snapshot sku=40: unit_cost=3.00000000 currency=USD baseline_version=13
- events since=0: last_id=50 events=31
- no token: 401
- expired token: 401��UPDATE expires_at=now()-1h ��

### �ֹ��Ŀ�����

1. INSERT sys_config open_api.client_id/client_secret��fixture��2 �У�
2. INSERT cache_version model_alias=1��fixture��1 �У�
3. UPDATE open_api_token SET expires_at=now()-1h WHERE client_id='test_open_client'��E2E ���ڲ��ԣ�1 �Σ��º�ָ���

### ����

- 11a-�� aliases ���������У�model_alias �� version �У�ֻ��ȫ����
- 11a-�� ����ѯ���������δ����30s ��ʱӲ���룩
- 11a-�� level_tags ��Դδ������ǰ�� model_sku.tags ͸����
- 11a-�� token TTL Ӳ���� 1h���׶� F �������Ƿ���䣩
- 11a-�� routing ��������ϸ��ֻ���� supplier_id+weight�������������ӷ�����



---
## 11b �¼����� worker �� commit 2d36496

### ʵ��

- **EventJobRepo**��internal/repo/event_job.go����PollDue��COALESCE(next_run_at, created_at)<=now��+ Claim���������� PENDING|FAILED��RUNNING��+ MarkDone/MarkFailed/MarkDead + ResetStaleRunning + LoadWebhookConfig
- **DeliverClient**��internal/worker/delivery_client.go����HTTP POST + X-Event-Signature��HMAC-SHA256(body, webhook_secret)����secret Ϊ�ղ���
- **EventDeliverJob**��internal/worker/event_deliver.go����������λ + ���� + ���� + �˱ܣ�1min��5^(n-1)���� 5 �� DEAD + alert��
- **ע��**��jobs.go ע�� event-deliver��Ĭ�� 1 ���ӣ���config.go �� EventDeliver JobConfig��env.go ��Ĭ��ֵ

### �þ�

- �þ� 1��ͳһ webhook��sys_config.open_api.webhook_url���������߰� event_type �ַ�
- �þ� 2��body = {id, event_type, payload, created_at}��id ���������ݵȼ���
- �þ� 3���ɹ�=HTTP 2xx �� DONE��ʧ��=�� 2xx/��ʱ/���Ӵ��� �� FAILED + retry_count++ + �˱�
- �þ� 4�����������ͷ��루����ֻ�� event_outbox �У������� HTTP�������⣩
- �þ� 5��������λ��ÿ�� tick ǰ�ȳ� RUNNING ������
- �þ� 6��ǩ��ͷ X-Event-Signature��HMAC-SHA256(body, webhook_secret)����secret Ϊ�ղ���
- �þ� 7��һ��һ����50 �� + LIMIT 50������һ��������
- �þ� 8��webhook_url Ϊ��ʱ NOOP��״̬���ƽ������� HTTP��
- �þ� 9��next_run_at NULL ����ȥ������COALESCE(next_run_at, created_at)<=now�������ı�

### ����

8 ���̣�EventBackoff 4 + EventMaxRetry 1 + DeliverClient 5����

### ������֤

1. ��������ȥ�� FAILED �� E2E ���ǣ�FAILED �¼����ᱻ�����죩
2. �˱� retryCount��retryCount+1 �� TestEventBackoff_Series �죨25min��125min��
3. ǩ��ͷ�������� �� TestDeliver_NoSecret �죨�� secret ��Ӧ��ǩ��ͷ��

### E2E ʵ��ֵ

- 31 �� PENDING ȫ�� DONE��mock server �յ� 31 �� POST��
- 5 �� FAILED + retry_count=1��ͣ mock server ��
- id=8 retry_count=4 �� DEAD + retry_count=5 + alert id=10��EVENT_DELIVERY_FAILED/CRITICAL/EVENT_OUTBOX/target_id=8/OPEN��

### �ֹ��Ŀ�����

1. INSERT sys_config open_api.webhook_url/webhook_secret��fixture��2 �У�
2. UPDATE event_outbox SET status='PENDING', retry_count=0, next_run_at=now() WHERE id IN (8,9,10,11,12)��E2E ׼����1 �Σ�
3. UPDATE event_outbox SET status='PENDING', retry_count=4, next_run_at=now() WHERE id=8��DEAD ���ԣ�1 �Σ�
4. DELETE FROM sys_config WHERE config_key IN ('open_api.webhook_url', 'open_api.webhook_secret')��������
5. UPDATE event_outbox SET status='PENDING', retry_count=0, next_run_at=created_at WHERE id IN (8,9,10,11,12)��������
6. DELETE FROM alert WHERE alert_type='EVENT_DELIVERY_FAILED'��������

### ����

- 11b-�� model.published / model.deprecated / quote.approved �¼��Ĳ�����ûд����ҵ������û���������׶� 11 ��������
- 11b-�� �� webhook �� event_type ·��δ��������һ��ͳһ webhook����������Ʒȷ���Ƿ���Ҫ
- 11b-�� webhook ǩ����֤�������߲ࣩδ���������� sys_config.webhook_secret Ϊ�վ�����
- 11b-�� webhook �ļ�Ȩ��ʽǩ����ǩ��У���������߲࣬�����Ҫ���飩���������Ʒȷ���Ƿ�ÿ���� webhook_secret
- 11b-�� event_outbox ����ʷ��ѹ 31 �У���ǰȫ������ PENDING ״̬��������һ�� tick ��һ���� DONE

