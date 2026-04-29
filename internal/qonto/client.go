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
	"io"
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

// newRequest builds an authenticated HTTP request with standard Qonto headers
// (Authorization, Content-Type, X-Qonto-Staging-Token). body may be nil.
func (c *client) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("qonto: build request: %w", err)
	}
	req.Header.Set("Authorization", c.authHeader())
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.StagingToken != "" {
		req.Header.Set("X-Qonto-Staging-Token", c.cfg.StagingToken)
	}
	return req, nil
}

// get is a helper for authenticated GET requests. It decodes the JSON response
// body into dst and returns an error if the status code is >= 400.
func (c *client) get(ctx context.Context, path string, dst any) error {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
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
	req, err := c.newRequest(ctx, http.MethodPost, path, encoded)
	if err != nil {
		return err
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

// verifyPayeeReq is the POST /sepa/verify_payee request body.
type verifyPayeeReq struct {
	IBAN            string `json:"iban"`
	BeneficiaryName string `json:"beneficiary_name"`
}

// verifyPayeeResp is the HTTP 200 response from POST /sepa/verify_payee.
type verifyPayeeResp struct {
	MatchResult string `json:"match_result"`
	ProofToken  struct {
		Token string `json:"token"`
	} `json:"proof_token"`
}

// qontoErrResp is Qonto's error envelope on 4xx/5xx responses.
// For verify_payee, bank-side errors (400/503) include a proof_token in
// errors[0].meta — the token is valid and the transfer can proceed.
type qontoErrResp struct {
	Errors []struct {
		Code string `json:"code"`
		Meta struct {
			ProofToken struct {
				Token string `json:"token"`
			} `json:"proof_token"`
		} `json:"meta"`
	} `json:"errors"`
}

// verifyPayee calls POST /sepa/verify_payee and returns the proof_token.
//
// Error handling per Qonto + EPC VOP spec (EPC103-24 v1.0.1):
//   - 200 MATCH_RESULT_NO_MATCH  → hard error (definitive mismatch; do not transfer)
//   - 200 any other result       → return token (MATCH, CLOSE_MATCH, NOT_POSSIBLE)
//   - 400/503 bank errors        → extract proof_token from error meta; VOP was not
//     performed but the token is valid and the transfer may proceed
//   - other 4xx/5xx              → hard error
func (c *client) verifyPayee(ctx context.Context, iban, beneficiaryName string) (string, error) {
	if iban == "" || beneficiaryName == "" {
		return "", fmt.Errorf("qonto: verifyPayee requires iban and beneficiary_name")
	}
	body, err := json.Marshal(verifyPayeeReq{IBAN: iban, BeneficiaryName: beneficiaryName})
	if err != nil {
		return "", fmt.Errorf("qonto: marshal verify_payee request: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/sepa/verify_payee", body)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("qonto: POST /sepa/verify_payee: %w", err)
	}
	defer resp.Body.Close()

	// Read body for all status codes — error responses also carry a proof_token.
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("qonto: read verify_payee response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var vop verifyPayeeResp
		if err := json.Unmarshal(raw, &vop); err != nil {
			return "", fmt.Errorf("qonto: decode verify_payee response: %w", err)
		}
		if vop.MatchResult == "MATCH_RESULT_NO_MATCH" {
			return "", fmt.Errorf("qonto: payee verification failed: name does not match IBAN %s", iban)
		}
		return vop.ProofToken.Token, nil

	case http.StatusBadRequest, http.StatusServiceUnavailable:
		// Bank-side error: per EPC VOP spec, proof_token is in error meta.
		// VOP was not performed; the token still authorises the transfer.
		var errResp qontoErrResp
		if json.Unmarshal(raw, &errResp) == nil && len(errResp.Errors) > 0 {
			if tok := errResp.Errors[0].Meta.ProofToken.Token; tok != "" {
				return tok, nil
			}
		}
		return "", fmt.Errorf("qonto: verify_payee HTTP %d: no proof_token in error response", resp.StatusCode)

	default:
		return "", fmt.Errorf("qonto: verify_payee HTTP %d", resp.StatusCode)
	}
}

func (c *client) SendTransfer(ctx context.Context, req TransferRequest) (*TransferResponse, error) {
	// Step 1: Verification of Payee (VOP) — required before every transfer.
	// The proof_token is injected into the request body.
	token, err := c.verifyPayee(ctx, req.BeneficiaryIBAN, req.BeneficiaryName)
	if err != nil {
		return nil, fmt.Errorf("qonto: payee verification: %w", err)
	}
	req.VopProofToken = token

	// Step 2: POST /sepa/transfers with X-Qonto-Idempotency-Key.
	// Cannot use the generic post() helper because we need the extra header.
	encoded, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("qonto: marshal transfer request: %w", err)
	}
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/sepa/transfers", encoded)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("X-Qonto-Idempotency-Key", req.Transfer.Reference)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("qonto: POST /sepa/transfers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("qonto: POST /sepa/transfers returned HTTP %d", resp.StatusCode)
	}
	var result TransferResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("qonto: decode transfer response: %w", err)
	}
	return &result, nil
}
