// Package api 提供 OpenAPI（swag）注解模型：统一响应包、通用分页结构。
//
// 所有接口响应遵循设计文档 §8.0：{"code":0,"message":"ok","data":{...},"requestId":"..."}。
// code != 0 为业务错误（见 apperr 错误码表）。
package api

// APIResponse 是所有业务接口的统一响应包。
type APIResponse struct {
	Code      int         `json:"code" example:"0"`                               // 业务错误码，0=成功
	Message   string      `json:"message" example:"ok"`                           // 错误消息，成功时 "ok"
	Data      interface{} `json:"data,omitempty" swaggertype:"object"`            // 业务数据
	RequestID string      `json:"requestId" example:"01929d81-..." format:"uuid"` // 请求追踪号（X-Request-Id）
}

// PageResult 是分页列表的统一 data 结构。
type PageResult struct {
	List  []interface{} `json:"list"`
	Total int64         `json:"total" example:"100"`
	Page  int           `json:"page" example:"1"`
	Size  int           `json:"size" example:"20"`
}
