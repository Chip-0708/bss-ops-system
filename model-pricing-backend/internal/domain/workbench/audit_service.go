// Package workbench 的 audit_service.go：10b §5 审计日志查询与导出。
//
// 权限（契约 F:V/F:E → M12:V/M12:E，stage10b 裁决 1）：
//   - GET /audit-logs → M12:V
//   - GET /audit-logs/export → M12:V
//
// 查询条件：action/target_type/target_id/operator_id/operator_role/source_type/time_range/page/size。
// 导出：CSV（UTF-8 BOM）/ XLSX（excelize），上限 10000 行（stage10b 裁决 6）。
// 行级过滤：不引入（审计日志全量可见，stage10b 裁决 2）。
// operator_name 解析：SYSTEM→"系统"，CUSTOMER→customer_profile→legal_subject.legal_name，
//
//	SUPPLIER→supplier_profile→legal_subject.legal_name，其他→internal_staff.name。
package workbench

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"time"

	"github.com/xuri/excelize/v2"
)

// AuditService 审计日志服务。
type AuditService struct {
	store AuditStore
}

// NewAuditService 构造。
func NewAuditService(store AuditStore) *AuditService {
	return &AuditService{store: store}
}

// AuditStore 审计仓储。
type AuditStore interface {
	// ListAuditLogs 审计日志列表（多条件 + 分页 + operator_name 解析）。
	ListAuditLogs(ctx context.Context, q AuditQuery) ([]AuditItem, int64, error)
	// ListAuditLogsForExport 查询导出数据（不分页，上限 10001 行判断是否超限）。
	ListAuditLogsForExport(ctx context.Context, q AuditExportQuery) ([]AuditItem, int64, error)
}

// AuditQuery 审计查询。
type AuditQuery struct {
	Action       string
	TargetType   string
	TargetID     *int64
	OperatorID   *int64
	OperatorRole string
	SourceType   string
	From         *time.Time
	To           *time.Time
	Page         int
	Size         int
}

// AuditItem 审计日志行（DTO）。
type AuditItem struct {
	ID           int64  `json:"id"`
	OperatorID   int64  `json:"operator_id"`
	OperatorName string `json:"operator_name"`
	OperatorRole string `json:"operator_role"`
	Action       string `json:"action"`
	TargetType   string `json:"target_type"`
	TargetID     int64  `json:"target_id"`
	BeforeValue  string `json:"before_value"`
	AfterValue   string `json:"after_value"`
	SourceType   string `json:"source_type"`
	SourceID     string `json:"source_id"`
	RequestID    string `json:"request_id"`
	CreatedAt    string `json:"created_at"`
}

// AuditListResult 审计列表结果。
type AuditListResult struct {
	Items []AuditItem `json:"items"`
	Total int64       `json:"total"`
}

// AuditExportQuery 审计导出查询。
type AuditExportQuery struct {
	Format string // csv | xlsx
	From   *time.Time
	To     *time.Time
}

// AuditExportResult 审计导出结果。
type AuditExportResult struct {
	Data        []byte
	Filename    string
	ContentType string
}

// ListAuditLogs 审计日志列表。
//
// 裁决 4：多条件 AND；page/size 兜底（page>=1，size 1~100 默认 20）。
func (s *AuditService) ListAuditLogs(ctx context.Context, q AuditQuery) (*AuditListResult, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 || q.Size > 100 {
		q.Size = 20
	}
	items, total, err := s.store.ListAuditLogs(ctx, q)
	if err != nil {
		return nil, err
	}
	return &AuditListResult{Items: items, Total: total}, nil
}

// ExportAuditLogs 导出审计日志（裁决 6：上限 10000 行）。
//
// 变异 #2 锚点：上限判断去掉 → 导出超 10000 行不报错。
func (s *AuditService) ExportAuditLogs(ctx context.Context, q AuditExportQuery) (*AuditExportResult, error) {
	// 上限校验（变异 #2 锚点：去掉本判断 → 导出超 10000 行不报错）
	items, total, err := s.store.ListAuditLogsForExport(ctx, q)
	if err != nil {
		return nil, err
	}
	if total > 10000 {
		return nil, fmt.Errorf("导出记录数 %d 超过上限 10000，请缩小时间范围", total)
	}

	// 生成文件
	var buf bytes.Buffer
	var filename, contentType string

	if q.Format == "xlsx" {
		f := excelize.NewFile()
		defer func() { _ = f.Close() }()

		sheet := "Sheet1"
		// 表头
		headers := []string{"ID", "操作员ID", "操作员姓名", "操作员角色", "动作", "目标类型", "目标ID", "前值", "后值", "来源类型", "来源ID", "请求ID", "创建时间"}
		for i, h := range headers {
			cell, _ := excelize.CoordinatesToCellName(i+1, 1)
			_ = f.SetCellValue(sheet, cell, h)
		}
		// 数据行
		for i, item := range items {
			row := i + 2
			_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), item.ID)
			_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), item.OperatorID)
			_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", row), item.OperatorName)
			_ = f.SetCellValue(sheet, fmt.Sprintf("D%d", row), item.OperatorRole)
			_ = f.SetCellValue(sheet, fmt.Sprintf("E%d", row), item.Action)
			_ = f.SetCellValue(sheet, fmt.Sprintf("F%d", row), item.TargetType)
			_ = f.SetCellValue(sheet, fmt.Sprintf("G%d", row), item.TargetID)
			_ = f.SetCellValue(sheet, fmt.Sprintf("H%d", row), item.BeforeValue)
			_ = f.SetCellValue(sheet, fmt.Sprintf("I%d", row), item.AfterValue)
			_ = f.SetCellValue(sheet, fmt.Sprintf("J%d", row), item.SourceType)
			_ = f.SetCellValue(sheet, fmt.Sprintf("K%d", row), item.SourceID)
			_ = f.SetCellValue(sheet, fmt.Sprintf("L%d", row), item.RequestID)
			_ = f.SetCellValue(sheet, fmt.Sprintf("M%d", row), item.CreatedAt)
		}

		if err := f.Write(&buf); err != nil {
			return nil, fmt.Errorf("write xlsx: %w", err)
		}
		filename = fmt.Sprintf("audit_logs_%s.xlsx", time.Now().Format("20060102150405"))
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	} else {
		// CSV（UTF-8 BOM）
		buf.WriteString("\xEF\xBB\xBF") // UTF-8 BOM
		w := csv.NewWriter(&buf)
		// 表头
		_ = w.Write([]string{"ID", "操作员ID", "操作员姓名", "操作员角色", "动作", "目标类型", "目标ID", "前值", "后值", "来源类型", "来源ID", "请求ID", "创建时间"})
		// 数据行
		for _, item := range items {
			_ = w.Write([]string{
				fmt.Sprintf("%d", item.ID),
				fmt.Sprintf("%d", item.OperatorID),
				item.OperatorName,
				item.OperatorRole,
				item.Action,
				item.TargetType,
				fmt.Sprintf("%d", item.TargetID),
				item.BeforeValue,
				item.AfterValue,
				item.SourceType,
				item.SourceID,
				item.RequestID,
				item.CreatedAt,
			})
		}
		w.Flush()
		filename = fmt.Sprintf("audit_logs_%s.csv", time.Now().Format("20060102150405"))
		contentType = "text/csv; charset=utf-8"
	}

	return &AuditExportResult{
		Data:        buf.Bytes(),
		Filename:    filename,
		ContentType: contentType,
	}, nil
}
