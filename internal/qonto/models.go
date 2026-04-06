package qonto

import "time"

// Account represents a Qonto bank account.
type Account struct {
	ID                     string    `json:"id"`
	IBAN                   string    `json:"iban"`
	Currency               string    `json:"currency"`
	BalanceCents           int64     `json:"balance_cents"`
	AuthorizedBalanceCents int64     `json:"authorized_balance_cents"` // includes pending debits
	UpdatedAt              time.Time `json:"updated_at"`
}

// Transaction is a Qonto bank transaction (credit or debit).
type Transaction struct {
	ID          string     `json:"transaction_id"`
	Amount      float64    `json:"amount"`       // absolute value, never negative
	AmountCents int64      `json:"amount_cents"` // absolute value in cents
	Currency    string     `json:"currency"`
	Side        string     `json:"side"`   // "credit" | "debit"
	Status      string     `json:"status"` // "pending" | "completed" | "declined"
	Label       string     `json:"label"`
	EmittedAt   time.Time  `json:"emitted_at"`
	SettledAt   *time.Time `json:"settled_at"`
	Reference   string     `json:"reference"` // free-text field (can store our idempotency key)
}

// TransactionListResponse wraps paginated transaction results.
type TransactionListResponse struct {
	Transactions []Transaction `json:"transactions"`
	Meta         struct {
		CurrentPage int  `json:"current_page"`
		NextPage    *int `json:"next_page"`
		TotalCount  int  `json:"total_count"`
	} `json:"meta"`
}

// TransferRequest is the body for initiating an outbound bank transfer.
// Reference is used as an idempotency key — safe to retry with the same value.
type TransferRequest struct {
	DebtorIBAN   string `json:"debtor_iban"`   // our Qonto IBAN
	CreditorIBAN string `json:"creditor_iban"` // Crypto.com OTC IBAN
	CreditorName string `json:"creditor_name"` // beneficiary legal name
	AmountCents  int64  `json:"amount_cents"`  // amount to transfer in cents
	Currency     string `json:"currency"`      // "EUR"
	Reference    string `json:"reference"`     // idempotency key + audit trail
}

// TransferResponse is returned after a transfer is accepted by Qonto.
type TransferResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"` // "pending" | "completed"
}
