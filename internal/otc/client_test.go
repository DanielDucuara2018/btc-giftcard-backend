package otc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient returns a Client pointed at the given test server URL.
func newTestClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	return &Client{
		cfg: Config{
			BaseURL:   serverURL,
			APIKey:    "test-api-key",
			SecretKey: "test-secret-key",
		},
		httpClient: &http.Client{},
	}
}

// successEnvelope wraps a result value in the standard Crypto.com API envelope.
func successEnvelope(t *testing.T, result any) []byte {
	t.Helper()
	resultJSON, err := json.Marshal(result)
	require.NoError(t, err)
	envelope := apiResponse{
		ID:     1,
		Method: "test",
		Code:   0,
		Result: resultJSON,
	}
	b, err := json.Marshal(envelope)
	require.NoError(t, err)
	return b
}

// errorEnvelope returns a Crypto.com API error envelope with a non-zero code.
func errorEnvelope(t *testing.T, code int) []byte {
	t.Helper()
	envelope := apiResponse{
		ID:     1,
		Method: "test",
		Code:   code,
	}
	b, err := json.Marshal(envelope)
	require.NoError(t, err)
	return b
}

// ============================================================================
// sign / objectToString (pure functions — no HTTP needed)
// ============================================================================

func TestObjectToString_EmptyMap(t *testing.T) {
	assert.Equal(t, "", objectToString(map[string]any{}))
}

func TestObjectToString_SortedKeys(t *testing.T) {
	// Keys must be sorted alphabetically before concatenation.
	result := objectToString(map[string]any{
		"z_key": "z_val",
		"a_key": "a_val",
		"m_key": "m_val",
	})
	assert.Equal(t, "a_keya_valm_keym_valz_keyz_val", result)
}

func TestObjectToString_NestedMap(t *testing.T) {
	result := objectToString(map[string]any{
		"params": map[string]any{
			"b": "2",
			"a": "1",
		},
	})
	assert.Equal(t, "paramsa1b2", result)
}

func TestObjectToString_Slice(t *testing.T) {
	result := objectToString(map[string]any{
		"items": []any{"x", "y"},
	})
	assert.Equal(t, "itemsxy", result)
}

func TestSign_ReturnsHexString(t *testing.T) {
	c := &Client{cfg: Config{APIKey: "mykey", SecretKey: "mysecret"}}
	sig := c.sign("private/otc/request-deal", "42", 1700000000000, map[string]any{"side": "BUY"})
	// Must be a 64-char hex string (SHA-256 → 32 bytes → 64 hex chars).
	assert.Len(t, sig, 64)
	for _, ch := range sig {
		assert.True(t, (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f'),
			"unexpected character %c in signature", ch)
	}
}

func TestSign_Deterministic(t *testing.T) {
	// Same inputs must always produce the same signature.
	c := &Client{cfg: Config{APIKey: "key", SecretKey: "secret"}}
	params := map[string]any{"instrument_name": "BTC_EUR", "side": "BUY"}
	sig1 := c.sign("method", "req-1", 123456789, params)
	sig2 := c.sign("method", "req-1", 123456789, params)
	assert.Equal(t, sig1, sig2)
}

func TestSign_DifferentNonce_DifferentSig(t *testing.T) {
	c := &Client{cfg: Config{APIKey: "key", SecretKey: "secret"}}
	params := map[string]any{"side": "BUY"}
	sig1 := c.sign("method", "req-1", 1000, params)
	sig2 := c.sign("method", "req-1", 1001, params)
	assert.NotEqual(t, sig1, sig2)
}

// ============================================================================
// GetOTCInstruments
// ============================================================================

func TestGetOTCInstruments_Success(t *testing.T) {
	instruments := otcInstruments{
		InstrumentList: []OTCInstrument{
			{InstrumentName: "BTC_EUR", BaseCurrency: "BTC", QuoteCurrency: "EUR", Type: "SPOT"},
			{InstrumentName: "BTC_USD", BaseCurrency: "BTC", QuoteCurrency: "USD", Type: "SPOT"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(successEnvelope(t, instruments))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	list, err := c.GetOTCInstruments(context.Background())
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "BTC_EUR", list[0].InstrumentName)
	assert.Equal(t, "BTC_USD", list[1].InstrumentName)
}

func TestGetOTCInstruments_APIErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(errorEnvelope(t, 10001))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.GetOTCInstruments(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "API error code 10001")
}

func TestGetOTCInstruments_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.GetOTCInstruments(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}

// ============================================================================
// RequestDeal
// ============================================================================

func TestRequestDeal_Success(t *testing.T) {
	deal := Deal{
		DealID:     "deal-abc-123",
		ClDealID:   "cl-deal-001",
		QuoteID:    "quote-xyz",
		DealType:   "QUOTE_REQUEST",
		DealStatus: DealAccepted,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the method path is correct.
		var req apiRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "private/otc/request-deal", req.Method)

		w.Header().Set("Content-Type", "application/json")
		w.Write(successEnvelope(t, deal))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	params := RequestDealParams{
		DealType: "QUOTE_REQUEST",
		ClDealID: "cl-deal-001",
		QuoteID:  "quote-xyz",
		LegList: []DealLegRequest{
			{InstrumentName: "BTC_EUR", Price: "45000.00", Side: "BUY", Notional: "1350.00"},
		},
	}

	result, err := c.RequestDeal(context.Background(), params)
	require.NoError(t, err)
	assert.Equal(t, "deal-abc-123", result.DealID)
	assert.Equal(t, DealAccepted, result.DealStatus)
}

func TestRequestDeal_APIErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(errorEnvelope(t, 20007)) // QUOTE_NOT_FOUND
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.RequestDeal(context.Background(), RequestDealParams{
		DealType: "QUOTE_REQUEST",
		ClDealID: "cl-deal-expired",
		QuoteID:  "expired-quote",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "API error code 20007")
}

// ============================================================================
// Withdraw
// ============================================================================

func TestWithdraw_Success(t *testing.T) {
	withdrawal := Withdrawal{
		ID:         "wd-001",
		ClientWdID: "client-wd-001",
		Currency:   "BTC",
		Amount:     0.015,
		Address:    "bc1qexampleaddr",
		Status:     WithdrawalPending,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req apiRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "private/create-withdrawal", req.Method)

		w.Header().Set("Content-Type", "application/json")
		w.Write(successEnvelope(t, withdrawal))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	req := WithdrawalRequest{
		Currency:   "BTC",
		Amount:     "0.01500000",
		Address:    "bc1qexampleaddr",
		NetworkID:  "BTC",
		ClientWdID: "client-wd-001",
	}

	result, err := c.Withdraw(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "wd-001", result.ID)
	assert.Equal(t, WithdrawalPending, result.Status)
}

func TestWithdraw_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(errorEnvelope(t, 10006)) // INSUFFICIENT_BALANCE
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.Withdraw(context.Background(), WithdrawalRequest{
		Currency:   "BTC",
		Amount:     "9999.0",
		Address:    "bc1qaddr",
		NetworkID:  "BTC",
		ClientWdID: "wd-insufficient",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "API error code 10006")
}

// ============================================================================
// GetWithdrawal
// ============================================================================

func TestGetWithdrawal_Found(t *testing.T) {
	targetID := "wd-target-999"
	networkID := "BTC"
	history := withdrawalHistory{
		WithdrawalList: []Withdrawal{
			{ID: "wd-other-1", Status: WithdrawalCompleted},
			{ID: targetID, Status: WithdrawalCompleted, TxID: "txhash-abc123", Currency: "BTC", Amount: 0.015, NetworkID: &networkID},
			{ID: "wd-other-2", Status: WithdrawalPending},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req apiRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "private/get-withdrawal-history", req.Method)

		w.Header().Set("Content-Type", "application/json")
		w.Write(successEnvelope(t, history))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	result, err := c.GetWithdrawal(context.Background(), targetID)
	require.NoError(t, err)
	assert.Equal(t, targetID, result.ID)
	assert.Equal(t, "txhash-abc123", result.TxID)
	assert.True(t, result.IsConfirmed())
}

func TestGetWithdrawal_NotFound(t *testing.T) {
	history := withdrawalHistory{
		WithdrawalList: []Withdrawal{
			{ID: "wd-other-1"},
			{ID: "wd-other-2"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(successEnvelope(t, history))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.GetWithdrawal(context.Background(), "wd-does-not-exist")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetWithdrawal_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(errorEnvelope(t, 10003))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.GetWithdrawal(context.Background(), "wd-api-err")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "API error code 10003")
}

// ============================================================================
// Withdrawal.IsConfirmed
// ============================================================================

func TestWithdrawal_IsConfirmed(t *testing.T) {
	cases := []struct {
		name      string
		status    WithdrawalStatus
		txid      string
		confirmed bool
	}{
		{"completed with txid", WithdrawalCompleted, "txhash", true},
		{"completed no txid", WithdrawalCompleted, "", false},
		{"pending with txid", WithdrawalPending, "txhash", false},
		{"processing", WithdrawalProcessing, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &Withdrawal{Status: tc.status, TxID: tc.txid}
			assert.Equal(t, tc.confirmed, w.IsConfirmed())
		})
	}
}
