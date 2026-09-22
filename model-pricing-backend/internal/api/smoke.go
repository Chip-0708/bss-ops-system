// Package api 的冒烟路由：仅在 MODEL_BSS_SMOKE=1 时挂载（生产关闭）。
// 这些路由仅用于手工/E2E 冒烟验证，不构成对外契约（不进 OpenAPI 文档）。
package api

import (
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"model_bss/internal/api/middleware"
	"model_bss/pkg/perm"
	"model_bss/pkg/response"
)

// smokeCallCounter 进程内计数器，用于证明业务只执行一次（重放时不增加）。
var smokeCallCounter int64

// smokeIdemHandler 幂等写接口：计数器 +1 并返回该计数；重放同 key 时不进入本 handler。
func smokeIdemHandler(c *gin.Context) {
	n := atomic.AddInt64(&smokeCallCounter, 1)
	result := gin.H{"calls": n}
	middleware.SetIdempotencyResult(c, result)
	response.Success(c, result)
}

// registerSmokeRoutes 挂冒烟路由（仅 MODEL_BSS_SMOKE=1 时挂载）。
func registerSmokeRoutes(g *gin.RouterGroup, idemStore middleware.IdempotencyStore) {
	g.GET("/smoke/models", middleware.RequirePerm(perm.M1View), smokeModelsHandler)
	g.GET("/smoke/models-edit", middleware.RequirePerm(perm.M1Edit), smokeModelsHandler)
	g.POST("/smoke/idem", middleware.Idempotency(idemStore, middleware.DefaultBizKey), smokeIdemHandler)
}

// smokeModelsHandler 原来的 M1:V 验证端点。
func smokeModelsHandler(c *gin.Context) {
	op := middleware.OperatorFrom(c)
	response.Success(c, gin.H{"operator_id": op.OperatorID, "perms_count": len(op.Perms)})
}
