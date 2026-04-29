package treasury

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// ThresholdTrigger
// ============================================================================

func TestThresholdTrigger_AboveFloor(t *testing.T) {
	tr := &ThresholdTrigger{FloorCents: 1_000_000, TargetCents: 500_000}
	should, amount, err := tr.ShouldPurchase(context.Background(), 1_200_000)
	require.NoError(t, err)
	assert.True(t, should)
	// spend = 1_200_000 − 500_000 = 700_000
	assert.Equal(t, int64(700_000), amount)
}

func TestThresholdTrigger_ExactlyAtFloor(t *testing.T) {
	// "at floor" should NOT trigger (balance <= floor)
	tr := &ThresholdTrigger{FloorCents: 1_000_000, TargetCents: 500_000}
	should, _, err := tr.ShouldPurchase(context.Background(), 1_000_000)
	require.NoError(t, err)
	assert.False(t, should)
}

func TestThresholdTrigger_BelowFloor(t *testing.T) {
	tr := &ThresholdTrigger{FloorCents: 1_000_000, TargetCents: 500_000}
	should, _, err := tr.ShouldPurchase(context.Background(), 800_000)
	require.NoError(t, err)
	assert.False(t, should)
}

func TestThresholdTrigger_ZeroBalance(t *testing.T) {
	tr := &ThresholdTrigger{FloorCents: 1_000_000, TargetCents: 500_000}
	should, _, err := tr.ShouldPurchase(context.Background(), 0)
	require.NoError(t, err)
	assert.False(t, should)
}

func TestThresholdTrigger_SpendAmountIsBalanceMinusTarget(t *testing.T) {
	cases := []struct {
		name          string
		floor         int64
		target        int64
		balance       int64
		expectedSpend int64
	}{
		{"large surplus", 1_000_000, 500_000, 5_000_000, 4_500_000},
		{"minimal surplus", 1_000_000, 500_000, 1_000_001, 500_001},
		{"target equals zero", 1_000_000, 0, 1_500_000, 1_500_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := &ThresholdTrigger{FloorCents: tc.floor, TargetCents: tc.target}
			should, amount, err := tr.ShouldPurchase(context.Background(), tc.balance)
			require.NoError(t, err)
			assert.True(t, should)
			assert.Equal(t, tc.expectedSpend, amount)
		})
	}
}

// ============================================================================
// ManualTrigger
// ============================================================================

func TestManualTrigger_AlwaysFires(t *testing.T) {
	mt := &ManualTrigger{AmountCents: 250_000}
	// Balance is irrelevant for a manual trigger.
	for _, balance := range []int64{0, 100, 1_000_000, -1} {
		should, amount, err := mt.ShouldPurchase(context.Background(), balance)
		require.NoError(t, err)
		assert.True(t, should, "ManualTrigger must always return true")
		assert.Equal(t, int64(250_000), amount)
	}
}

func TestManualTrigger_ReturnsConfiguredAmount(t *testing.T) {
	amounts := []int64{1, 100_000, 9_999_999}
	for _, a := range amounts {
		mt := &ManualTrigger{AmountCents: a}
		_, amount, err := mt.ShouldPurchase(context.Background(), 0)
		require.NoError(t, err)
		assert.Equal(t, a, amount)
	}
}
