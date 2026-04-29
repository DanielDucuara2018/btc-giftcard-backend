package treasury

import "context"

// PurchaseTrigger decides whether a BTC purchase should be initiated and how
// much fiat to spend. Implementing as an interface allows manual admin triggers
// and automatic threshold-based triggers to share the same workflow.
type PurchaseTrigger interface {
	// ShouldPurchase returns:
	//   - should:  whether to proceed with a purchase
	//   - amount:  fiat amount to spend, in cents (only meaningful when should=true)
	//   - err:     any error evaluating the rule
	ShouldPurchase(ctx context.Context, fiatBalanceCents int64) (should bool, amountCents int64, err error)
}

// ThresholdTrigger fires automatically when the fiat balance exceeds FloorCents.
// It spends enough to bring the balance down to TargetCents.
//
// Example — FloorCents=1_000_000 (10 000 EUR), TargetCents=500_000 (5 000 EUR):
//
//	balance=12 000 EUR → spend 7 000 EUR (12 000 − 5 000)
//	balance= 8 000 EUR → no action (below floor)
type ThresholdTrigger struct {
	FloorCents  int64 // balance that triggers a purchase (e.g. 10_000_00 for 10k EUR)
	TargetCents int64 // balance to retain after the purchase  (e.g.  5_000_00 for  5k EUR)
}

func (t *ThresholdTrigger) ShouldPurchase(_ context.Context, balanceCents int64) (bool, int64, error) {
	if balanceCents <= t.FloorCents {
		return false, 0, nil
	}
	spend := balanceCents - t.TargetCents
	if spend <= 0 {
		return false, 0, nil
	}
	return true, spend, nil
}

// ManualTrigger always authorises a purchase of a fixed amount. It is used when
// an admin explicitly initiates a BTC purchase from the admin interface.
type ManualTrigger struct {
	AmountCents int64
}

func (t *ManualTrigger) ShouldPurchase(_ context.Context, _ int64) (bool, int64, error) {
	return true, t.AmountCents, nil
}
