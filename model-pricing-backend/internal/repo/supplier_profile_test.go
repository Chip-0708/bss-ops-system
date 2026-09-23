package repo

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"model_bss/internal/domain/supplier"
)

// 只验证生成的归属条件，无需连接数据库；列表和详情都从 profileQuery 起步。
func TestProfileQueryUsesExistingOwnerScope(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=127.0.0.1 user=test dbname=test"}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true})
	require.NoError(t, err)
	r := NewSupplierRepo(db)

	cases := []struct {
		name  string
		scope supplier.OwnerScope
		want  string
	}{
		{"self", supplier.OwnerScope{DataScope: "SELF", StaffID: 7}, "sp.owner_procurement_operator_id ="},
		{"dept", supplier.OwnerScope{DataScope: "DEPT", MyOrgID: 3}, "os_.org_unit_id ="},
		{"subtree", supplier.OwnerScope{DataScope: "DEPT_SUB", MyOrgID: 3, ScopePaths: []string{"/1/3/"}}, "ou_.path LIKE ANY"},
		{"all", supplier.OwnerScope{DataScope: "ALL"}, "JOIN legal_subject"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rows []supplier.ProfileSummary
			tx := r.profileQuery(context.Background(), tc.scope).Where("sp.id = ?", 42).Find(&rows)
			require.NoError(t, tx.Error)
			sql := tx.Statement.SQL.String()
			require.Contains(t, sql, tc.want)
			if tc.name == "all" {
				require.False(t, strings.Contains(sql, "os_.org_unit_id"))
				require.False(t, strings.Contains(sql, "sp.owner_procurement_operator_id ="))
			}
		})
	}
}

func TestProfileListCountsOnlyCurrentlyEffectiveQuotes(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=127.0.0.1 user=test dbname=test"}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true})
	require.NoError(t, err)
	r := NewSupplierRepo(db)
	var rows []supplier.ProfileSummary
	tx := profileListPageQuery(r.profileQuery(context.Background(), supplier.OwnerScope{DataScope: "ALL"}),
		supplier.ProfileQuery{Page: 1, Size: 20}).Find(&rows)
	require.NoError(t, tx.Error)
	sql := tx.Statement.SQL.String()
	require.Contains(t, sql, "COUNT(DISTINCT qi.sku_id)")
	require.Equal(t, 3, strings.Count(sql, "qs.status = 'EFFECTIVE'"))
	require.Equal(t, 3, strings.Count(sql, "qs.valid_from <= now() AND qs.valid_to > now()"))
	require.Contains(t, sql, "qs.valid_to <= now() + interval '30 days'")
	require.NotContains(t, sql, "channel_type")
}
