package qonto

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient returns a client pointed at the given test server URL.
func newTestClient(t *testing.T, serverURL string) *client {
	t.Helper()
	return &client{
		cfg: Config{
			BaseURL:   serverURL,
			Login:     "test-org",
			SecretKey: "test-secret",
			IBAN:      "FR7614508059400000000000000",
		},
		httpClient: &http.Client{},
	}
}

// ============================================================================
// verifyPayee
// ============================================================================

func TestVerifyPayee_Match(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/sepa/verify_payee", r.URL.Path)

		var req verifyPayeeReq
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "DE89370400440532013000", req.IBAN)
		assert.Equal(t, "Crypto.com OTC", req.BeneficiaryName)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(verifyPayeeResp{
			MatchResult: "MATCH_RESULT_MATCH",
			ProofToken: struct {
				Token string `json:"token"`
			}{Token: "tok-match-abc"},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	tok, err := c.verifyPayee(context.Background(), "DE89370400440532013000", "Crypto.com OTC")
	require.NoError(t, err)
	assert.Equal(t, "tok-match-abc", tok)
}

func TestVerifyPayee_CloseMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(verifyPayeeResp{
			MatchResult: "MATCH_RESULT_CLOSE_MATCH",
			ProofToken: struct {
				Token string `json:"token"`
			}{Token: "tok-close"},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	tok, err := c.verifyPayee(context.Background(), "DE89370400440532013000", "Crypto COM OTC")
	require.NoError(t, err)
	assert.Equal(t, "tok-close", tok)
}

func TestVerifyPayee_NotPossible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(verifyPayeeResp{
			MatchResult: "MATCH_RESULT_NOT_POSSIBLE",
			ProofToken: struct {
				Token string `json:"token"`
			}{Token: "tok-not-possible"},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	tok, err := c.verifyPayee(context.Background(), "DE89370400440532013000", "Crypto.com OTC")
	require.NoError(t, err)
	assert.Equal(t, "tok-not-possible", tok)
}

func TestVerifyPayee_NoMatch_HardError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(verifyPayeeResp{
			MatchResult: "MATCH_RESULT_NO_MATCH",
			ProofToken: struct {
				Token string `json:"token"`
			}{Token: "tok-should-not-be-used"},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	tok, err := c.verifyPayee(context.Background(), "DE89370400440532013000", "Wrong Name")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name does not match IBAN")
	assert.Empty(t, tok)
}

func TestVerifyPayee_400WithProofToken(t *testing.T) {
	// Bank-side error: 400 but the proof_token is present in error meta.
	// Per EPC103-24 the transfer may still proceed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(qontoErrResp{
			Errors: []struct {
				Code string `json:"code"`
				Meta struct {
					ProofToken struct {
						Token string `json:"token"`
					} `json:"proof_token"`
				} `json:"meta"`
			}{
				{
					Code: "BANK_UNAVAILABLE",
					Meta: struct {
						ProofToken struct {
							Token string `json:"token"`
						} `json:"proof_token"`
					}{ProofToken: struct {
						Token string `json:"token"`
					}{Token: "tok-400-bank"}},
				},
			},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	tok, err := c.verifyPayee(context.Background(), "DE89370400440532013000", "Crypto.com OTC")
	require.NoError(t, err)
	assert.Equal(t, "tok-400-bank", tok)
}

func TestVerifyPayee_503WithProofToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(qontoErrResp{
			Errors: []struct {
				Code string `json:"code"`
				Meta struct {
					ProofToken struct {
						Token string `json:"token"`
					} `json:"proof_token"`
				} `json:"meta"`
			}{
				{
					Code: "BANK_SERVICE_UNAVAILABLE",
					Meta: struct {
						ProofToken struct {
							Token string `json:"token"`
						} `json:"proof_token"`
					}{ProofToken: struct {
						Token string `json:"token"`
					}{Token: "tok-503-bank"}},
				},
			},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	tok, err := c.verifyPayee(context.Background(), "DE89370400440532013000", "Crypto.com OTC")
	require.NoError(t, err)
	assert.Equal(t, "tok-503-bank", tok)
}

func TestVerifyPayee_400NoToken_Error(t *testing.T) {
	// 400 but no proof_token in the error body — hard error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"errors":[{"code":"INVALID_IBAN"}]}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	tok, err := c.verifyPayee(context.Background(), "INVALID", "Name")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no proof_token in error response")
	assert.Empty(t, tok)
}

func TestVerifyPayee_500_HardError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	tok, err := c.verifyPayee(context.Background(), "DE89370400440532013000", "Name")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
	assert.Empty(t, tok)
}

func TestVerifyPayee_EmptyIBAN(t *testing.T) {
	c := newTestClient(t, "http://unused")
	tok, err := c.verifyPayee(context.Background(), "", "Name")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires iban and beneficiary_name")
	assert.Empty(t, tok)
}

func TestVerifyPayee_EmptyName(t *testing.T) {
	c := newTestClient(t, "http://unused")
	tok, err := c.verifyPayee(context.Background(), "DE89370400440532013000", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires iban and beneficiary_name")
	assert.Empty(t, tok)
}

// ============================================================================
// SendTransfer
// ============================================================================

// setupTransferServer returns a test server that handles both /sepa/verify_payee
// and /sepa/transfers with configurable behaviour.
type transferServerConfig struct {
	vopStatus int
	vopBody   string
	// If transferStatus is 0 the transfer endpoint is not expected to be called.
	transferStatus int
	transferBody   string
}

func setupTransferServer(t *testing.T, cfg transferServerConfig) *httptest.Server {
	t.Helper()
	transferCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sepa/verify_payee":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(cfg.vopStatus)
			w.Write([]byte(cfg.vopBody))
		case "/sepa/transfers":
			transferCalled = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(cfg.transferStatus)
			w.Write([]byte(cfg.transferBody))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(func() {
		if cfg.transferStatus == 0 && transferCalled {
			t.Error("SendTransfer called /sepa/transfers but it was not expected (VOP should have blocked it)")
		}
	})
	return srv
}

func TestSendTransfer_HappyPath(t *testing.T) {
	vopResp := `{"match_result":"MATCH_RESULT_MATCH","proof_token":{"token":"tok-happy"}}`
	xferResp := `{"transfer":{"id":"xfer-1","status":"pending","amount":"100.00","amount_cents":10000,"reference":"ref-001"}}`

	srv := setupTransferServer(t, transferServerConfig{
		vopStatus:      http.StatusOK,
		vopBody:        vopResp,
		transferStatus: http.StatusCreated,
		transferBody:   xferResp,
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	req := TransferRequest{
		BeneficiaryIBAN: "DE89370400440532013000",
		BeneficiaryName: "Crypto.com OTC",
		Transfer: TransferDetails{
			BankAccountID: "acct-uuid",
			Amount:        "100.00",
			Currency:      "EUR",
			Reference:     "ref-001",
		},
	}

	resp, err := c.SendTransfer(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "xfer-1", resp.Transfer.ID)
	assert.Equal(t, "pending", resp.Transfer.Status)
	assert.Equal(t, "ref-001", resp.Transfer.Reference)
}

func TestSendTransfer_VOPTokenInjected(t *testing.T) {
	// Verify the proof_token from VOP is placed in the transfer request body.
	const expectedToken = "tok-injected-xyz"
	vopResp := `{"match_result":"MATCH_RESULT_MATCH","proof_token":{"token":"` + expectedToken + `"}}`

	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sepa/verify_payee":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(vopResp))
		case "/sepa/transfers":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedBody))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"transfer":{"id":"xfer-2","status":"pending","amount":"50.00","reference":"ref-002"}}`))
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.SendTransfer(context.Background(), TransferRequest{
		BeneficiaryIBAN: "DE89370400440532013000",
		BeneficiaryName: "Crypto.com OTC",
		Transfer: TransferDetails{
			BankAccountID: "acct-uuid",
			Amount:        "50.00",
			Currency:      "EUR",
			Reference:     "ref-002",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, expectedToken, capturedBody["vop_proof_token"])
}

func TestSendTransfer_IdempotencyKeyHeader(t *testing.T) {
	const ref = "unique-ref-idempotency"
	vopResp := `{"match_result":"MATCH_RESULT_MATCH","proof_token":{"token":"tok-ok"}}`

	var capturedKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sepa/verify_payee":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(vopResp))
		case "/sepa/transfers":
			capturedKey = r.Header.Get("X-Qonto-Idempotency-Key")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"transfer":{"id":"xfer-3","status":"pending","amount":"200.00","reference":"` + ref + `"}}`))
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.SendTransfer(context.Background(), TransferRequest{
		BeneficiaryIBAN: "DE89370400440532013000",
		BeneficiaryName: "Crypto.com OTC",
		Transfer: TransferDetails{
			BankAccountID: "acct-uuid",
			Amount:        "200.00",
			Currency:      "EUR",
			Reference:     ref,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, ref, capturedKey)
}

func TestSendTransfer_VOPNoMatch_BlocksTransfer(t *testing.T) {
	vopResp := `{"match_result":"MATCH_RESULT_NO_MATCH","proof_token":{"token":"tok-no-match"}}`

	transferCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sepa/verify_payee":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(vopResp))
		case "/sepa/transfers":
			transferCalled = true
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	resp, err := c.SendTransfer(context.Background(), TransferRequest{
		BeneficiaryIBAN: "DE89370400440532013000",
		BeneficiaryName: "Wrong Name",
		Transfer: TransferDetails{
			BankAccountID: "acct-uuid",
			Amount:        "100.00",
			Currency:      "EUR",
			Reference:     "ref-blocked",
		},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "payee verification")
	assert.Nil(t, resp)
	assert.False(t, transferCalled, "transfer endpoint must not be called after VOP no-match")
}

func TestSendTransfer_TransferHTTPError(t *testing.T) {
	vopResp := `{"match_result":"MATCH_RESULT_MATCH","proof_token":{"token":"tok-ok"}}`

	srv := setupTransferServer(t, transferServerConfig{
		vopStatus:      http.StatusOK,
		vopBody:        vopResp,
		transferStatus: http.StatusUnprocessableEntity,
		transferBody:   `{"errors":[{"code":"INSUFFICIENT_BALANCE"}]}`,
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	resp, err := c.SendTransfer(context.Background(), TransferRequest{
		BeneficiaryIBAN: "DE89370400440532013000",
		BeneficiaryName: "Crypto.com OTC",
		Transfer: TransferDetails{
			BankAccountID: "acct-uuid",
			Amount:        "999999.00",
			Currency:      "EUR",
			Reference:     "ref-fail",
		},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 422")
	assert.Nil(t, resp)
}

// ============================================================================
// GetAccount
// ============================================================================

func TestGetAccount_Success_FirstAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/organization", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(organization{
			Organization: struct {
				BankAccounts []Account `json:"bank_accounts"`
			}{
				BankAccounts: []Account{
					{ID: "acct-1", IBAN: "FR7614508059400000000000000", Currency: "EUR", BalanceCents: 500000},
				},
			},
		})
	}))
	defer srv.Close()

	c := &client{
		cfg:        Config{BaseURL: srv.URL, Login: "org", SecretKey: "sec"},
		httpClient: &http.Client{},
	}
	acct, err := c.GetAccount(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "acct-1", acct.ID)
	assert.Equal(t, int64(500000), acct.BalanceCents)
}

func TestGetAccount_MatchByIBAN(t *testing.T) {
	targetIBAN := "DE89370400440532013000"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(organization{
			Organization: struct {
				BankAccounts []Account `json:"bank_accounts"`
			}{
				BankAccounts: []Account{
					{ID: "acct-1", IBAN: "FR7614508059400000000000000"},
					{ID: "acct-2", IBAN: targetIBAN, BalanceCents: 1234567},
				},
			},
		})
	}))
	defer srv.Close()

	c := &client{
		cfg:        Config{BaseURL: srv.URL, Login: "org", SecretKey: "sec", IBAN: targetIBAN},
		httpClient: &http.Client{},
	}
	acct, err := c.GetAccount(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "acct-2", acct.ID)
}

func TestGetAccount_NoAccounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(organization{})
	}))
	defer srv.Close()

	c := &client{cfg: Config{BaseURL: srv.URL}, httpClient: &http.Client{}}
	_, err := c.GetAccount(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no bank accounts")
}

func TestGetAccount_IBANNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(organization{
			Organization: struct {
				BankAccounts []Account `json:"bank_accounts"`
			}{
				BankAccounts: []Account{{ID: "acct-1", IBAN: "FR7614508059400000000000000"}},
			},
		})
	}))
	defer srv.Close()

	c := &client{
		cfg:        Config{BaseURL: srv.URL, IBAN: "XX0000000000000000000000000"},
		httpClient: &http.Client{},
	}
	_, err := c.GetAccount(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no bank account with IBAN")
}

// ============================================================================
// ListTransactions
// ============================================================================

func TestListTransactions_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/transactions", r.URL.Path)
		assert.Equal(t, "credit", r.URL.Query().Get("side"))
		assert.Equal(t, "completed", r.URL.Query().Get("status[]"))
		assert.Equal(t, "1", r.URL.Query().Get("page"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TransactionListResponse{
			Transactions: []Transaction{
				{ID: "tx-1", Amount: 100.0, Currency: "EUR", Side: "credit"},
			},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	result, err := c.ListTransactions(context.Background(), "credit", "completed", 1)
	require.NoError(t, err)
	require.Len(t, result.Transactions, 1)
	assert.Equal(t, "tx-1", result.Transactions[0].ID)
}

func TestListTransactions_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.ListTransactions(context.Background(), "", "", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 401")
}
