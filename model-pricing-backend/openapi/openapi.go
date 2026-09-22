// Package openapi 把 swag 生成的 OpenAPI 产物嵌入二进制，供运行时在线调试页（Swagger UI）读取。
//
// 为什么要 embed，而不是 r.StaticFile("./openapi/swagger.json")：
//   - 静态文件版本依赖进程工作目录必须是仓库根目录，换个目录启动就 404；
//   - embed 后进二进制，容器里没有 openapi/ 目录也能用。
//
// 注意职责划分：
//   - 本目录下的 swagger.json / swagger.yaml 是**生成物**，由 `make swag`（swag init）产出，
//     不要手工编辑，改 swagger 注释后重新生成即可；
//   - 本文件是**手写**的胶水代码，唯一职责是把生成物暴露为 Go 变量。
//
// 另外：go:embed 不允许引用包目录之外的文件（不能用 ../），所以这个文件必须和 json/yaml
// 同目录——这不是随手选的位置。
package openapi

import _ "embed"

// SwaggerJSON 是 OpenAPI 2.0 规范的 JSON 全文（Swagger UI 读取的数据源）。
//
//go:embed swagger.json
var SwaggerJSON []byte

// SwaggerYAML 是同规范的 YAML 版本，给习惯看 YAML / 做 diff 的人用。
//
//go:embed swagger.yaml
var SwaggerYAML []byte
