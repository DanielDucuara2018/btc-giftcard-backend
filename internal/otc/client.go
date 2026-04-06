// Package otc provides a REST client for the Crypto.com OTC API.
//
// Authentication uses HMAC-SHA256 request signing.
// OTC API reference: https://exchange-docs.crypto.com/exchange/v1/rest-ws/index_OTC2.html
//
// The full RFQ flow requires WebSocket for private/otc/request-quote (future work).
// This package implements the REST portions: get-otc-instruments, request-deal,
// create-withdrawal, and get-withdrawal.
package otc

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config holds Crypto.com API credentials, sourced from config.toml [cryptocom].
type Config struct {
	BaseURL     string        // "https://api.crypto.com/exchange/v1"
	APIKey      string        // public API key
	SecretKey   string        // secret key used for HMAC signing
	HTTPTimeout time.Duration // defaults to 15s if zero
}

// Client is the concrete HTTP implementation of the Crypto.com OTC REST API.
type Client struct {
	cfg        Config
	httpClient *http.Client
}

// NewClient returns a Client configured with the given credentials.
func NewClient(cfg Config) CryptocomService {
	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// ============================================================================
// Request signing (HMAC-SHA256)
// ============================================================================

// sign generates the HMAC-SHA256 signature required for private endpoints.
//
// Algorithm (per OTC API docs §Digital Signature):
//  1. Sort parameter keys alphabetically; recursively for nested objects.
//  2. Concatenate: method + id + api_key + param_string + nonce
//  3. HMAC-SHA256 with the secret key; hex-encode the result.
func (c *Client) sign(method, requestID string, nonce int64, params map[string]any) string {
	payload := method + requestID + c.cfg.APIKey + objectToString(params) + strconv.FormatInt(nonce, 10)
	mac := hmac.New(sha256.New, []byte(c.cfg.SecretKey))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// objectToString recursively serialises a params map into the canonical signing
// string: keys sorted alphabetically, values stringified recursively.
func objectToString(obj map[string]any) string {
	if len(obj) == 0 {
		return ""
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString(valueToString(obj[k]))
	}
	return sb.String()
}

// valueToString serialises a single value into the canonical HMAC signing
// string. Handles nested maps and slices recursively.
func valueToString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case map[string]any:
		return objectToString(val)
	case []any:
		var sb strings.Builder
		for _, elem := range val {
			sb.WriteString(valueToString(elem))
		}
		return sb.String()
	default:
		return fmt.Sprintf("%v", val)
	}
}

// ============================================================================
// HTTP helpers
// ============================================================================

// apiRequest is the standard JSON envelope for Crypto.com REST requests.
type apiRequest struct {
	ID      int64          `json:"id"`
	Method  string         `json:"method"`
	APIKey  string         `json:"api_key"`
	Params  map[string]any `json:"params"`
	Nonce   int64          `json:"nonce"`
	SigHash string         `json:"sig"`
}

// apiResponse is the standard JSON envelope returned by the API.
type apiResponse struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Code   int             `json:"code"`
	Result json.RawMessage `json:"result"`
}

// structToParams converts a request struct to map[string]any via JSON
// round-trip so that json tags and omitempty rules are honoured consistently.
func structToParams(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// post sends a signed request to a private endpoint and unmarshals the result
// payload into dst. Returns an error if the API response code is non-zero.
func (c *Client) post(ctx context.Context, method string, params map[string]any, dst any) error {
	nonce := time.Now().UnixMilli()
	requestID := strconv.FormatInt(nonce, 10)

	body := apiRequest{
		ID:      nonce,
		Method:  method,
		APIKey:  c.cfg.APIKey,
		Params:  params,
		Nonce:   nonce,
		SigHash: c.sign(method, requestID, nonce, params),
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("otc: marshal request: %w", err)
	}

	url := strings.TrimRight(c.cfg.BaseURL, "/") + "/" + strings.TrimLeft(method, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("otc: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("otc: POST %s: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("otc: POST %s returned HTTP %d", method, resp.StatusCode)
	}

	var envelope apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("otc: decode response: %w", err)
	}
	if envelope.Code != 0 {
		return fmt.Errorf("otc: API error code %d for method %s", envelope.Code, method)
	}
	if dst != nil {
		return json.Unmarshal(envelope.Result, dst)
	}
	return nil
}

// ============================================================================
// Client methods
// ============================================================================

// otcInstruments is the result envelope for private/otc/get-otc-instruments.
type otcInstruments struct {
	InstrumentList []OTCInstrument `json:"instrument_list"`
}

// GetOTCInstruments returns the full list of instruments available for OTC
// trading. Use the InstrumentName values to validate a pair before constructing
// a request-quote on the WebSocket channel.
func (c *Client) GetOTCInstruments(ctx context.Context) ([]OTCInstrument, error) {
	var result otcInstruments
	if err := c.post(ctx, "private/otc/get-otc-instruments", map[string]any{}, &result); err != nil {
		return nil, fmt.Errorf("otc: get instruments: %w", err)
	}
	return result.InstrumentList, nil
}

// RequestDeal executes an OTC deal against an active quote received via the
// user.otc_qr.quotes WebSocket channel. The leg prices must match the quote
// exactly; any deviation causes a QUOTE_NOT_FOUND or INVALID_PRICE error.
// The exchange acknowledges the deal synchronously (DealStatus == ACCEPTED);
// settlement confirmation arrives later via the user.otc.deals WebSocket channel.
func (c *Client) RequestDeal(ctx context.Context, params RequestDealParams) (*Deal, error) {
	var result Deal
	paramsMapping, err := structToParams(params)
	if err != nil {
		return nil, fmt.Errorf("otc: failed to convert request deal params : %w", err)
	}
	if err := c.post(ctx, "private/otc/request-deal", paramsMapping, &result); err != nil {
		return nil, fmt.Errorf("otc: request deal: %w", err)
	}
	return &result, nil
}

// Withdraw initiates an on-chain BTC withdrawal from the Crypto.com account to
// the address specified in req. The returned Withdrawal will have
// Status == WithdrawalPending ("0"); poll GetWithdrawal until IsConfirmed()
// returns true before crediting the LND node.
func (c *Client) Withdraw(ctx context.Context, req WithdrawalRequest) (*Withdrawal, error) {
	var result Withdrawal
	paramsMapping, err := structToParams(req)
	if err != nil {
		return nil, fmt.Errorf("otc: failed to convert create withdrawal params : %w", err)
	}
	if err := c.post(ctx, "private/create-withdrawal", paramsMapping, &result); err != nil {
		return nil, fmt.Errorf("otc: create withdrawal: %w", err)
	}
	return &result, nil
}

// withdrawalHistory is the result envelope for private/get-withdrawal-history.
type withdrawalHistory struct {
	WithdrawalList []Withdrawal `json:"withdrawal_list"`
}

// GetWithdrawal fetches the current state of a withdrawal from
// private/get-withdrawal-history and returns the entry whose exchange-assigned
// ID matches withdrawalID. Returns a non-nil error if the ID is not present
// in the history window (API default: 90 days).
func (c *Client) GetWithdrawal(ctx context.Context, withdrawalID string) (*Withdrawal, error) {
	var result withdrawalHistory
	if err := c.post(ctx, "private/get-withdrawal-history", map[string]any{}, &result); err != nil {
		return nil, fmt.Errorf("otc: get withdrawal: %w", err)
	}
	for i := range result.WithdrawalList {
		if result.WithdrawalList[i].ID == withdrawalID {
			return &result.WithdrawalList[i], nil
		}
	}
	return nil, fmt.Errorf("otc: get withdrawal: id %q not found", withdrawalID)
}
