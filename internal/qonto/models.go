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

// TransferRequest is the body for POST /sepa/transfers (Qonto v2 API).
//
// Before calling SendTransfer, the client automatically calls POST /sepa/verify_payee
// using BeneficiaryIBAN + BeneficiaryName and injects the resulting proof_token.
// Set X-Qonto-Idempotency-Key is set to Transfer.Reference automatically.
//
// API reference: https://docs.qonto.com/api-reference/business-api/payments-transfers/sepa-transfers/sepa-transfers/create
type TransferRequest struct {
	// VopProofToken is injected by SendTransfer from the verify_payee response.
	// Populate it directly only if you already hold a pre-obtained token.
	VopProofToken string `json:"vop_proof_token,omitempty"`

	// Transfer contains the payment details sent to Qonto.
	Transfer TransferDetails `json:"transfer"`

	// BeneficiaryIBAN and BeneficiaryName are used by SendTransfer to call
	// POST /sepa/verify_payee before the transfer. They are NOT sent to Qonto
	// as part of the transfer body (json:"-").
	BeneficiaryIBAN string `json:"-"`
	BeneficiaryName string `json:"-"`
}

// TransferDetails holds the per-transfer payment fields.
type TransferDetails struct {
	BankAccountID string `json:"bank_account_id"`          // source Qonto account UUID
	Amount        string `json:"amount"`                   // decimal string e.g. "1100.50"
	Currency      string `json:"currency"`                 // "EUR"
	BeneficiaryID string `json:"beneficiary_id,omitempty"` // pre-saved beneficiary UUID
	Reference     string `json:"reference"`                // free-text; also used as idempotency key
	Note          string `json:"note,omitempty"`           // internal memo (not sent to recipient)
}

// TransferResponse is returned after a transfer is accepted by Qonto.
type TransferResponse struct {
	Transfer struct {
		ID          string `json:"id"`
		Status      string `json:"status"` // "pending" | "completed" | "declined"
		Amount      string `json:"amount"` // decimal string
		AmountCents int64  `json:"amount_cents"`
		Reference   string `json:"reference"`
	} `json:"transfer"`
}
