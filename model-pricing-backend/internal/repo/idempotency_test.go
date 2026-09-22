package repo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIdempotencyRepo_CreateProcessingWithNullResult(t *testing.T) {
	database := openTestDB(t)
	tx := database.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })

	requestID := fmt.Sprintf("repo-idempotency-%d", time.Now().UnixNano())
	repository := NewIdempotencyRepo(tx)
	require.NoError(t, repository.CreateProcessing(
		context.Background(), requestID, "sha256:test", "repo-test:"+requestID, time.Now().Add(time.Minute),
	))

	var stored row
	require.NoError(t, tx.Table("idempotency_key").Where("request_id = ?", requestID).Take(&stored).Error)
	require.Equal(t, "PROCESSING", stored.Status)
	require.Nil(t, stored.ResultJSON)
}
