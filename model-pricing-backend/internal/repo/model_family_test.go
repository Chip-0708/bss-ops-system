//go:build !short

package repo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"model_bss/internal/domain/model"
)

func TestListFamilies_PaginationAndCompleteChildren(t *testing.T) {
	database := openTestDB(t)
	tx := database.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })

	tag := fmt.Sprintf("fv%d", time.Now().UnixNano())
	var vendorID int64
	require.NoError(t, tx.Raw(`INSERT INTO vendor (code, name) VALUES (?, ?) RETURNING id`,
		tag, "Family View "+tag).Scan(&vendorID).Error)

	familyIDs := make([]int64, 0, 3)
	for i := 1; i <= 3; i++ {
		var familyID int64
		require.NoError(t, tx.Raw(`INSERT INTO model_family (vendor_id, name) VALUES (?, ?) RETURNING id`,
			vendorID, fmt.Sprintf("Family %d %s", i, tag)).Scan(&familyID).Error)
		familyIDs = append(familyIDs, familyID)
	}

	insertSKU := func(familyID int64, code string) int64 {
		t.Helper()
		var skuID int64
		require.NoError(t, tx.Raw(`INSERT INTO model_sku
			(vendor_id, family_id, sku_code, model_type, native_currency, tags, lifecycle_status)
			VALUES (?, ?, ?, '对话', 'USD', '[]'::jsonb, 'PUBLISHED') RETURNING id`,
			vendorID, familyID, code).Scan(&skuID).Error)
		return skuID
	}

	matchedID := insertSKU(familyIDs[0], "family-match-"+tag)
	insertSKU(familyIDs[0], "family-sibling-"+tag)
	insertSKU(familyIDs[1], "family-two-"+tag)
	insertSKU(familyIDs[2], "family-three-"+tag)
	require.NoError(t, tx.Exec(`INSERT INTO model_alias (sku_id, alias, source) VALUES (?, ?, 'MANUAL')`,
		matchedID, "alias-"+tag).Error)

	repository := NewModelRepo(tx)
	ctx := context.Background()

	page1, err := repository.ListFamilies(ctx, model.ListQuery{VendorID: &vendorID, Page: 1, Size: 2})
	require.NoError(t, err)
	require.Equal(t, int64(3), page1.Total)
	require.Len(t, page1.List, 2)
	require.NotEqual(t, page1.List[0].FamilyID, page1.List[1].FamilyID)

	page2, err := repository.ListFamilies(ctx, model.ListQuery{VendorID: &vendorID, Page: 2, Size: 2})
	require.NoError(t, err)
	require.Equal(t, int64(3), page2.Total)
	require.Len(t, page2.List, 1)

	keyword, err := repository.ListFamilies(ctx, model.ListQuery{
		VendorID: &vendorID, Keyword: "family-match", Page: 1, Size: 20,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), keyword.Total)
	require.Len(t, keyword.List, 1)
	require.Equal(t, familyIDs[0], keyword.List[0].FamilyID)
	require.Equal(t, 2, keyword.List[0].SkuCount)
	require.Len(t, keyword.List[0].Children, 2)

	var matchedAliases []string
	for _, child := range keyword.List[0].Children {
		if child.ID == matchedID {
			matchedAliases = child.Aliases
		}
	}
	require.Equal(t, []string{"alias-" + tag}, matchedAliases)
}
