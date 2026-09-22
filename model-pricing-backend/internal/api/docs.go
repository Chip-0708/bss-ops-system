// Package api 的 docs.go：在线接口调试页（Swagger UI）的路由装配。
//
// 为什么给前端同学用这个：
//   - swag 注解就在 handler 上方，Swagger UI 读的是 openapi/swagger.json，与代码同源，
//     不会出现「文档一套、实现一套」；改了注释跑 `make swag` 即可同步。
//   - 页面自带 Authorize，把登录接口返回的 token 粘进去（格式：`Bearer <token>`），
//     之后所有带 @Security BearerAuth 的接口都会自动带上 Authorization 头，可以直接发请求。
//
// 三个端点：
//   - GET /swagger/index.html    UI 页面本体（/swagger 会 302 跳过来）
//   - GET /openapi/swagger.json  Swagger UI 的数据源（也方便别人用 Postman/Apifox 导入）
//   - GET /openapi/swagger.yaml  同上，YAML 版
//
// 数据来源用 go:embed 嵌进二进制（见 openapi 包），不依赖进程工作目录。
//
// 开关：docs.enable（默认 true，见 internal/infra/config/env.go）。
// 生产部署请显式关闭——本页能列出全部接口与参数，属于内部调试资产。
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"model_bss/openapi"
)

// registerDocs 挂载调试页与 spec 端点。docs.enable=false 时完全不注册（路由表里看不见）。
func registerDocs(r *gin.Engine) {
	// swagger.json 是本服务同源路径，UI 不需要跨域去取。
	spec := func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json; charset=utf-8", openapi.SwaggerJSON)
	}
	specYAML := func(c *gin.Context) {
		c.Data(http.StatusOK, "text/yaml; charset=utf-8", openapi.SwaggerYAML)
	}
	r.GET("/openapi/swagger.json", spec)
	r.GET("/openapi/swagger.yaml", specYAML)

	// UI 静态资源由 swaggo/files 提供（自带 swagger-ui-dist，不依赖公网 CDN，内网可用）。
	ui := ginSwagger.WrapHandler(swaggerFiles.Handler, ginSwagger.URL("/openapi/swagger.json"))
	r.GET("/swagger/*any", ui)
	r.GET("/swagger", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/swagger/index.html")
	})
}
