package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"model_bss/internal/api/middleware"
	"model_bss/internal/domain/supplier"
	"model_bss/pkg/perm"
)

type profileStatusStore struct{ err error }

func (s profileStatusStore) ListProfiles(context.Context, supplier.OwnerScope, supplier.ProfileQuery) (*supplier.ProfileListResult, error) {
	return nil, nil
}

func (s profileStatusStore) GetProfile(context.Context, supplier.OwnerScope, int64) (*supplier.ProfileDetail, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &supplier.ProfileDetail{ID: 1, OwnerProcurementOperatorID: 7}, nil
}

func TestGetSupplierProfileStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"不存在", supplier.ErrNotFound, http.StatusNotFound},
		{"超出数据域", supplier.ErrProfileOutOfScope, http.StatusForbidden},
		{"可见", nil, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set("auth.operator", &middleware.Operator{StaffID: 8, Roles: []string{perm.RoleSales}})
				c.Next()
			})
			h := NewSupplierProfileHandler(supplier.NewProfileService(profileStatusStore{err: tc.err}))
			r.GET("/suppliers/:id", h.GetProfile)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/suppliers/1", nil))
			require.Equal(t, tc.status, rec.Code)
		})
	}
}

func TestVisibleSupplierProfileCommercialFields(t *testing.T) {
	detail := &supplier.ProfileDetail{
		ID: 1, OwnerProcurementOperatorID: 7, LegalName: "供应商",
		SettleType: "MONTHLY", BillingCycle: 30, MinRecharge: "100.00",
		CreditLine: "1000.00", CreditUsed: "50.00", DepositAmount: "200.00",
	}
	cases := []struct {
		name string
		op   middleware.Operator
		show bool
	}{
		{"财务多角色", middleware.Operator{Roles: []string{perm.RoleSales, perm.RoleFinance}, StaffID: 8}, true},
		{"归属采购", middleware.Operator{Roles: []string{perm.RoleProcurement}, StaffID: 7}, true},
		{"非归属采购", middleware.Operator{Roles: []string{perm.RoleProcurement}, StaffID: 8}, false},
		{"管理员", middleware.Operator{Roles: []string{perm.RolePlatformAdmin}, StaffID: 8}, false},
		{"无角色", middleware.Operator{StaffID: 8}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(visibleSupplierProfile(detail, &tc.op))
			require.NoError(t, err)
			var got map[string]interface{}
			require.NoError(t, json.Unmarshal(b, &got))
			require.Equal(t, "供应商", got["legal_name"])
			for _, key := range supplierCommercialFields {
				if tc.show {
					require.Contains(t, got, key)
				} else {
					require.NotContains(t, got, key)
				}
			}
		})
	}
	maskedOwner := &middleware.Operator{Roles: []string{perm.RoleProcurement}, StaffID: 7, FieldMask: []string{"credit_line"}}
	got := visibleSupplierProfile(detail, maskedOwner)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	var object map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &object))
	require.NotContains(t, object, "credit_line")
}
