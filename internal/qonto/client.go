// Package qonto provides an HTTP client for the Qonto banking API.
//
// Authentication uses the "login:secret-key" scheme in the Authorization header.
// API reference: https://api-doc.qonto.com/
//
// All money-movement methods accept a Reference field that acts as an
// idempotency key. Passing the same Reference twice will not double-execute
// the transfer on Qonto's side.
package qonto

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Config holds Qonto API credentials, sourced from config.toml [qonto] section.
type Config struct {
	BaseURL      string        // "https://thirdparty.qonto.com/v2"
	Login        string        // organisation slug (e.g. "acme-corp-1234")
	SecretKey    string        // API secret key
	IBAN         string        // our Qonto IBAN (source account for SEPA transfers)
	StagingToken string        // X-Qonto-Staging-Token; required for sandbox only
	HTTPTimeout  time.Duration // defaults to 15s if zero
}

// client is the concrete HTTP implementation of QontoService. It is unexported
// so callers always program against the QontoService interface.
type client struct {
	cfg        Config
	httpClient *http.Client
}

// NewClient constructs a Qonto API client that satisfies QontoService.
func NewClient(cfg Config) QontoService {
	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	return &client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// authHeader returns the value for the Authorization HTTP header.
// Format: "login:secret-key" (Qonto's Basic-style scheme).
func (c *client) authHeader() string {
	return c.cfg.Login + ":" + c.cfg.SecretKey
}

// get is a helper for authenticated GET requests. It decodes the JSON response
// body into dst and returns an error if the status code is >= 400.
func (c *client) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("qonto: build request: %w", err)
	}
	req.Header.Set("Authorization", c.authHeader())
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.StagingToken != "" {
		req.Header.Set("X-Qonto-Staging-Token", c.cfg.StagingToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qonto: GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("qonto: GET %s returned HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

// post is a helper for authenticated POST requests. It encodes body as JSON,
// sends it to path, decodes the response into dst, and returns an error if the
// status code is >= 400.
func (c *client) post(ctx context.Context, path string, body, dst any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("qonto: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("qonto: build request: %w", err)
	}
	req.Header.Set("Authorization", c.authHeader())
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.StagingToken != "" {
		req.Header.Set("X-Qonto-Staging-Token", c.cfg.StagingToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qonto: POST %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("qonto: POST %s returned HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

type organization struct {
	Organization struct {
		BankAccounts []Account `json:"bank_accounts"`
	} `json:"organization"`
}

func (c *client) GetAccount(ctx context.Context) (*Account, error) {
	var resp organization
	if err := c.get(ctx, "/organization", &resp); err != nil {
		return nil, fmt.Errorf("qonto: get organization: %w", err)
	}
	accounts := resp.Organization.BankAccounts
	if len(accounts) == 0 {
		return nil, fmt.Errorf("qonto: organization has no bank accounts")
	}
	if c.cfg.IBAN == "" {
		return &accounts[0], nil
	}
	for i := range accounts {
		if accounts[i].IBAN == c.cfg.IBAN {
			return &accounts[i], nil
		}
	}
	return nil, fmt.Errorf("qonto: no bank account with IBAN %s", c.cfg.IBAN)
}

func (c *client) ListTransactions(ctx context.Context, side, status string, page int) (*TransactionListResponse, error) {
	params := url.Values{}
	params.Set("iban", c.cfg.IBAN)
	if side != "" {
		params.Set("side", side)
	}
	if status != "" {
		params.Set("status[]", status)
	}
	if page > 0 {
		params.Set("page", strconv.Itoa(page))
	}
	var resp TransactionListResponse
	if err := c.get(ctx, "/transactions?"+params.Encode(), &resp); err != nil {
		return nil, fmt.Errorf("qonto: list transactions: %w", err)
	}
	return &resp, nil
}

func (c *client) SendTransfer(ctx context.Context, req TransferRequest) (*TransferResponse, error) {
	// TODO: POST /sepa/transfers
	// This endpoint requires Strong Customer Authentication (SCA). You must first obtain
	// a vop_proof_token from the verify-payee endpoint, then include it at the top level.
	// Body: { "vop_proof_token": "<token>", "transfer": { "bank_account_id": ..., "amount": "1100.50", ... } }
	// Set header X-Qonto-Idempotency-Key to req.Reference.
	// Reference: https://docs.qonto.com/api-reference/business-api/payments-transfers/sepa-transfers/sepa-transfers/create

	// if err := c.post(ctx, "/sepa/transfers")
	panic("not implemented")
}
