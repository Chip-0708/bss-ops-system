package cost

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestResolveParams_SupplierWins(t *testing.T) {
	rows := []Param{
		{ScopeType: ScopeGlobal, ScopeID: 0, LossRate: d("0.03"), ChannelRate: d("0.01")},
		{ScopeType: ScopeModel, ScopeID: 40, LossRate: d("0.04"), ChannelRate: d("0.02")},
		{ScopeType: ScopeSupplier, ScopeID: 7, LossRate: d("0.05"), ChannelRate: d("0.03")},
	}
	got, ok := ResolveParams(rows, 40, 7)
	require.True(t, ok)
	require.Equal(t, ScopeSupplier, got.ParamsScope)
	require.True(t, d("0.05").Equal(got.LossRate))
	require.True(t, d("0.03").Equal(got.ChannelRate))
}

func TestResolveParams_ModelOverGlobal(t *testing.T) {
	rows := []Param{
		{ScopeType: ScopeGlobal, ScopeID: 0, LossRate: d("0.03"), ChannelRate: d("0.01")},
		{ScopeType: ScopeModel, ScopeID: 40, LossRate: d("0.04"), ChannelRate: d("0.02")},
	}
	got, ok := ResolveParams(rows, 40, 7)
	require.True(t, ok)
	require.Equal(t, ScopeModel, got.ParamsScope)
	require.True(t, d("0.04").Equal(got.LossRate))
}

func TestResolveParams_ModelMismatchFallsToGlobal(t *testing.T) {
	rows := []Param{
		{ScopeType: ScopeGlobal, ScopeID: 0, LossRate: d("0.03"), ChannelRate: d("0.01")},
		{ScopeType: ScopeModel, ScopeID: 99, LossRate: d("0.04"), ChannelRate: d("0.02")},
	}
	got, ok := ResolveParams(rows, 40, 7)
	require.True(t, ok)
	require.Equal(t, ScopeGlobal, got.ParamsScope)
	require.True(t, d("0.03").Equal(got.LossRate))
}

func TestResolveParams_GlobalOnly(t *testing.T) {
	rows := []Param{
		{ScopeType: ScopeGlobal, ScopeID: 0, LossRate: d("0.03"), ChannelRate: d("0.01")},
	}
	got, ok := ResolveParams(rows, 40, 7)
	require.True(t, ok)
	require.Equal(t, ScopeGlobal, got.ParamsScope)
}

func TestResolveParams_SupplierMismatchDoesNotLeak(t *testing.T) {
	// 别的供应商的覆盖不得串到本供应商
	rows := []Param{
		{ScopeType: ScopeSupplier, ScopeID: 8, LossRate: d("0.09"), ChannelRate: d("0.09")},
		{ScopeType: ScopeGlobal, ScopeID: 0, LossRate: d("0.03"), ChannelRate: d("0.01")},
	}
	got, ok := ResolveParams(rows, 40, 7)
	require.True(t, ok)
	require.Equal(t, ScopeGlobal, got.ParamsScope)
}

func TestResolveParams_AllMissingReturnsFalse(t *testing.T) {
	got, ok := ResolveParams(nil, 40, 7)
	require.False(t, ok)
	require.Equal(t, "", got.ParamsScope)
}
