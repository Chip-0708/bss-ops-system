// Package main 是应用入口：加载配置 -> 初始化日志/数据库/鉴权依赖 -> 启动 HTTP 服务
// -> 监听 SIGTERM/SIGINT 进入优雅关闭。
// 每个启动阶段失败都会立刻退出（zap 记录后 os.Exit）。
package main

// @title 模型管理与定价中心 API
// @version 0.1
// @description 大模型中转平台：模型数据中心 + 进货/出货两条定价线。统一响应包 {code,message,data,requestId}；金额一律字符串传输；时间 ISO 8601 带时区。
// @description 错误码：400 参数校验、401 未认证、403 无权限、409 幂等/状态冲突、423 主体冻结、429 限流。
// @description 写操作（产生业务新版本/影响资金合同）必须携带 Idempotency-Key 头。
// @description 分页：?page=1&size=20 或 ?cursor=&size=，统一返回 {list,total,page,size}。
// @contact.name 后端
// @BasePath /
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Bearer <token>，登录后获得

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"go.uber.org/zap"

	"model_bss/internal/api"
	"model_bss/internal/domain/auth"
	"model_bss/internal/domain/cost"
	"model_bss/internal/domain/customer"
	"model_bss/internal/domain/model"
	"model_bss/internal/domain/price"
	"model_bss/internal/domain/pricing"
	"model_bss/internal/domain/supplier"
	"model_bss/internal/domain/workbench"
	"model_bss/internal/infra/config"
	"model_bss/internal/infra/db"
	infra_logger "model_bss/internal/infra/logger"
	"model_bss/internal/repo"
	"model_bss/internal/worker"
	"model_bss/pkg/cache"
	"model_bss/pkg/perm"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	log, err := infra_logger.New(cfg.Log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init logger: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = log.Sync()
	}()

	dbClient, err := db.New(cfg.Database)
	if err != nil {
		log.Fatal("connect database", zap.Error(err))
	}
	defer func() {
		if cerr := dbClient.Close(); cerr != nil {
			log.Error("close database", zap.Error(cerr))
		}
	}()

	authRepo := repo.NewAuthRepo(dbClient.DB)

	// 启动校验：代码中的 60 个权限点常量必须与 permission_point 表完全一致。
	// 拼错权限点会导致中间件静默放行，因此在启动阶段直接 fail fast。
	if err := perm.ValidateInDB(authRepo); err != nil {
		log.Fatal("validate permission points", zap.Error(err))
	}
	log.Info("permission points validated", zap.Int("count", len(perm.All())))

	authService := auth.NewService(authRepo, newCacheLimiter(cache.New()), auth.Config{})

	quoteRepo := repo.NewQuoteRepo(dbClient.DB)
	quoteSvc := supplier.NewQuoteService(quoteRepo)
	approveSvc := supplier.NewApproveService(repo.NewApproveRepo(dbClient.DB))
	lifecycleSvc := supplier.NewLifecycleService(repo.NewLifecycleRepo(dbClient.DB), approveSvc)

	// 6b-3 成本引擎消费者：业务写（基线版本切换）与任务状态推进严格分离（两个事务）。
	// 6b-4 读路径共用同一个 CostBaselineRepo（读写同一连接池，Store 接口分离）。
	costRepo := repo.NewCostBaselineRepo(dbClient.DB)
	costSvc := cost.NewService(costRepo, repo.NewCostTaskRepo(dbClient.DB), log)
	costReadSvc := cost.NewReadService(costRepo)
	costParamRepo := repo.NewCostParamRepo(dbClient.DB)
	costParamSvc := cost.NewParamService(costParamRepo, nil)
	costLockSvc := cost.NewLockService(costSvc, repo.NewCostLockRepo(dbClient.DB), nil)
	costCompareSvc := cost.NewCompareService(costSvc, costRepo)
	costJobs := worker.NewCostJobRepo(dbClient.DB)

	// 阶段 7a：官方价采集批次 + 暂存区（07-supplier-and-price-change.md §5/§6）。
	priceSyncSvc := price.NewSyncService(repo.NewPriceSyncRepo(dbClient.DB), nil)

	// 阶段 7b：确认入正式版本 + 审批回调（§7/§8/§9）。
	// ModelSvc 的 ApprovedHook 按 change_type 分发：PRICE_UP/PRICE_DOWN 注入生效连锁
	// （price_version 新版本 + 报价静默跟随 + COST_RECALC + official_price.changed + cache_version+1）；
	// 退役（DEPRECATE）走 store 内建分支不经此 hook。
	priceConfirmRepo := repo.NewPriceConfirmRepo(repo.NewSupplierRepo(dbClient.DB))
	priceConfirmSvc := price.NewConfirmService(priceConfirmRepo, nil)

	// 阶段 8a：定价策略 + 生成价目表草稿（08-pricing.md §1/§2）。
	pricingSvc := pricing.NewPricingService(repo.NewPricingRepo(dbClient.DB), nil)

	// 阶段 8b-1：价目表发布 + 回滚（08-pricing.md §3/§4）。
	pricingPublishRepo := repo.NewPricingPublishRepo(dbClient.DB)
	pricingPublishSvc := pricing.NewPublishService(pricingPublishRepo, nil)

	// 阶段 8b-2：涨价传导决策队列（08-pricing.md §5）。
	pricingUpconductionRepo := repo.NewPricingUpconductionRepo(dbClient.DB)
	pricingUpconductionSvc := pricing.NewUpconductionService(pricingUpconductionRepo, nil)

	// 阶段 9a：客户域（09-customer-quote.md §1/§2）。
	customerRepo := repo.NewCustomerRepo(dbClient.DB)
	customerSvc := customer.NewService(customerRepo, nil)

	// 阶段 9b：特价审批 + 刷新 + 导出（09-customer-quote.md §3/§4/§5）。
	customerSpecialRepo := repo.NewCustomerSpecialRepo(dbClient.DB)
	customerSpecialSvc := customer.NewSpecialPriceService(customerSpecialRepo)
	customerRefreshSvc := customer.NewRefreshService(customerSpecialRepo, nil)
	customerExportSvc := customer.NewExportService(customerSpecialRepo, nil, nil)

	// 阶段 9c：客户门户（09-customer-quote.md §6）。
	customerPortalRepo := repo.NewCustomerPortalRepo(dbClient.DB)
	customerPortalSvc := customer.NewPortalService(customerPortalRepo, nil)
	// 阶段 10a：工作台（10-workbench-audit.md §2 待办 + §3 指标卡）。
	workbenchRepo := repo.NewWorkbenchRepo(dbClient.DB)
	workbenchSvc := workbench.NewService(workbenchRepo, nil)
	// 阶段 10b：告警处理 + 审计日志（10-workbench-audit.md §4/§5）。
	alertRepo := repo.NewAlertRepo(dbClient.DB)
	alertSvc := workbench.NewAlertService(alertRepo, nil)
	auditRepo := repo.NewWorkbenchAuditRepo(dbClient.DB)
	auditSvc := workbench.NewAuditService(auditRepo)

	modelRepo := repo.NewModelRepo(dbClient.DB)
	modelSvc := model.NewService(modelRepo)
	modelSvc.ApprovedHook = func(changeType string) func(context.Context, int64, int64, string) error {
		if changeType == price.ChangePriceUp || changeType == price.ChangePriceDown {
			return func(ctx context.Context, changeRequestID int64, operatorID int64, requestID string) error {
				payload, err := modelRepo.LoadChangePayload(ctx, changeRequestID)
				if err != nil {
					return err
				}
				return priceConfirmRepo.ApplyOfficialPriceChange(ctx, repo.ApplyOfficialPriceChangeParams{
					ChangeRequestID: changeRequestID, Payload: payload,
					OperatorID: operatorID, OperatorRole: "INTERNAL", RequestID: requestID,
				})
			}
		}
		// 8b-1：价目表发布/回滚的生效连锁。
		if changeType == pricing.ChangePriceBookPublish || changeType == pricing.ChangePriceBookRollback {
			return func(ctx context.Context, changeRequestID int64, operatorID int64, requestID string) error {
				payload, err := modelRepo.LoadChangePayload(ctx, changeRequestID)
				if err != nil {
					return err
				}
				return pricingPublishRepo.ApplyPriceBookPublish(ctx, pricing.ApplyPublishParams{
					ChangeRequestID: changeRequestID, Payload: payload,
					OperatorID: operatorID, OperatorRole: "INTERNAL", RequestID: requestID,
				})
			}
		}
		// 9b：特价审批的批准生效连锁。
		if changeType == customer.ChangeSpecialPrice {
			return func(ctx context.Context, changeRequestID int64, operatorID int64, requestID string) error {
				return customerSpecialRepo.ApplySpecialPriceApproved(ctx, changeRequestID, operatorID, requestID)
			}
		}
		return nil
	}

	router := api.New(api.Deps{
		Config:                 cfg,
		Log:                    log,
		Health:                 dbClient,
		AuthService:            authService,
		SessionReader:          authRepo,
		IdemStore:              repo.NewIdempotencyRepo(dbClient.DB),
		ModelSvc:               modelSvc,
		SupplierSvc:            supplier.NewService(repo.NewSupplierRepo(dbClient.DB)),
		SupplierQuoteSvc:       quoteSvc,
		SupplierImportSvc:      supplier.NewImportService(quoteSvc, repo.NewImportRepo(dbClient.DB)),
		QuoteApproveSvc:        approveSvc,
		QuoteLifecycleSvc:      lifecycleSvc,
		CostReadSvc:            costReadSvc,
		CostParamSvc:           costParamSvc,
		CostLockSvc:            costLockSvc,
		CostCompareSvc:         costCompareSvc,
		PriceSyncSvc:           priceSyncSvc,
		PriceConfirmSvc:        priceConfirmSvc,
		PricingSvc:             pricingSvc,
		PricingPublishSvc:      pricingPublishSvc,
		PricingUpconductionSvc: pricingUpconductionSvc,
		CustomerSvc:            customerSvc,
		CustomerSpecialSvc:     customerSpecialSvc,
		CustomerRefreshSvc:     customerRefreshSvc,
		CustomerExportSvc:      customerExportSvc,
		CustomerPortalSvc:      customerPortalSvc,
		WorkbenchSvc:           workbenchSvc,
		AlertSvc:               alertSvc,
		AuditSvc:               auditSvc,
	})

	// worker 骨架（阶段 6a）：先注册任务，服务启动后 Start，优雅退出时
	// 先 httpServer.Shutdown()（等在途请求入队完）再 worker.Stop()（6a 裁决②）。
	// 每日时间按 db.timezone（Asia/Shanghai）解析。
	loc, err := time.LoadLocation(cfg.Database.TimeZone)
	if err != nil {
		loc = time.FixedZone("CST", 8*3600) // Asia/Shanghai 兜底
	}
	sched := worker.NewScheduler(worker.NewCronLocker(dbClient.DB), log, loc)
	nJobs := worker.RegisterJobs(sched, cfg.Worker, approveSvc, lifecycleSvc, costSvc, costJobs)
	if cfg.Worker.Enable && nJobs > 0 {
		sched.Start(context.Background())
		log.Info("worker started", zap.Int("jobs", nJobs))
	}

	addr := net.JoinHostPort(cfg.Server.Host, fmt.Sprint(cfg.Server.Port))
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  cfg.Server.Timeout,
		WriteTimeout: cfg.Server.Timeout,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server starting", zap.String("addr", addr))
		errCh <- httpServer.ListenAndServe()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("http server listen", zap.Error(err))
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// 优雅退出顺序（6a 裁决②）：先 httpServer.Shutdown() 等在途请求入队完，
	// 再 worker.Stop() 等当前批次跑完——在途请求入队的任务能被 worker 顺手处理，遗留 PENDING 更少。
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("http server shutdown", zap.Error(err))
	}
	sched.Stop()
	log.Info("server stopped")
}

// cacheLimiter 把 pkg/cache 适配为 auth.FailLimiter（登录失败计数，TTL 15 分钟）。
type cacheLimiter struct {
	c cache.Cache
}

func newCacheLimiter(c cache.Cache) *cacheLimiter { return &cacheLimiter{c: c} }

// Incr 对登录失败计数 +1，返回当前累计值。
func (l *cacheLimiter) Incr(key string, ttl time.Duration) int {
	return cache.IncrWithTTL(l.c, key, ttl)
}

// Get 读取当前计数（不自增）。
func (l *cacheLimiter) Get(key string) int {
	v, ok := l.c.Get(key)
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(string(v))
	if err != nil {
		return 0
	}
	return n
}

// Reset 登录成功后清零计数。
func (l *cacheLimiter) Reset(key string) { l.c.Delete(key) }
