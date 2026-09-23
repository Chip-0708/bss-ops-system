package repo

import (
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPortalAcceptUpdateGuardsEffectiveQuote(t *testing.T) {
	db, err := gorm.Open(postgres.Open("host=localhost user=test dbname=test sslmode=disable"),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	sql := db.ToSQL(func(tx *gorm.DB) *gorm.DB {
		return updatePortalAcceptedQuote(tx, 42, 7, 9, "test-request", time.Now())
	})
	for _, part := range []string{"customer_id =", "status <> 'EFFECTIVE'", "status = 'APPROVED' OR special_price_status = 'APPROVED'"} {
		if !strings.Contains(sql, part) {
			t.Fatalf("missing accept guard %q in SQL: %s", part, sql)
		}
	}
}
