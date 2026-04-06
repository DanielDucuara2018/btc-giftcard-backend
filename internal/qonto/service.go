package qonto

import "context"

// QontoService is the interface consumed by treasury.TreasuryManager.
// All money-movement methods must be idempotent: callers must supply a unique
// reference/key and may safely retry on transient errors.
type QontoService interface {
	// GetAccount returns the current balance and metadata of our fiat account.
	GetAccount(ctx context.Context) (*Account, error)

	// ListTransactions returns recent transactions, with optional filtering.
	//   side:   "credit" | "debit" | "" (both)
	//   status: "pending" | "completed" | "" (both)
	//   page:   1-based page number
	ListTransactions(ctx context.Context, side, status string, page int) (*TransactionListResponse, error)

	// SendTransfer initiates a SEPA transfer to the Crypto.com OTC account.
	// req.Reference must be a unique idempotency key (e.g., UUID).
	SendTransfer(ctx context.Context, req TransferRequest) (*TransferResponse, error)
}
