package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Pinger 抽象健康检查所依赖的数据库连接能力，便于在测试中注入 stub。
type Pinger interface {
	Ping(ctx context.Context) error
}

// healthzTimeout 保证健康检查接口快速返回，避免健康探测因数据库卡死而阻塞过久。
const healthzTimeout = 2 * time.Second

// HealthzHandler 返回 GET /healthz 的处理函数。
// 本接口是统一响应包的唯一例外，供 K8s/监控探针使用；
// 成功时：200 {"status":"ok","db":"up"}；数据库不可达时：503 {"status":"degraded","db":"down"}。
//
// @Summary 健康检查（运维探针，不走统一响应包）
// @Description 实际 Ping 数据库。200 {"status":"ok","db":"up"}；503 {"status":"degraded","db":"down"}。
// @Tags 运维
// @Produce json
// @Success 200 {object} map[string]string "status=ok, db=up"
// @Failure 503 {object} map[string]string "status=degraded, db=down"
// @Router /healthz [get]
func HealthzHandler(client Pinger) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), healthzTimeout)
		defer cancel()

		if err := client.Ping(ctx); err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"status": "degraded",
				"db":     "down",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
			"db":     "up",
		})
	}
}
