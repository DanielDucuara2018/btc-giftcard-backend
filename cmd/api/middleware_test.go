package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"btc-giftcard/internal/card"
	"btc-giftcard/pkg/logger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	_ = logger.Init("development")
}

// ============================================================================
// responseWriter tests
// ============================================================================

func TestResponseWriter_DefaultStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)

	assert.Equal(t, http.StatusOK, rw.status, "default status should be 200")
}

func TestResponseWriter_WriteHeaderCaptures(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)

	rw.WriteHeader(http.StatusCreated)

	assert.Equal(t, http.StatusCreated, rw.status)
	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestResponseWriter_WriteDelegates(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)

	n, err := rw.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", rec.Body.String())
}

func TestResponseWriter_HeaderDelegates(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newResponseWriter(rec)

	rw.Header().Set("X-Custom", "test-value")
	assert.Equal(t, "test-value", rec.Header().Get("X-Custom"))
}

// ============================================================================
// loggingMiddleware tests
// ============================================================================

func TestLoggingMiddleware_CallsNext(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	h := loggingMiddleware(next)
	r := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestLoggingMiddleware_CapturesStatusCode(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	h := loggingMiddleware(next)
	r := httptest.NewRequest("GET", "/missing", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	// The recorder should reflect the 404 even though logging wraps it.
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ============================================================================
// recoveryMiddleware tests
// ============================================================================

func TestRecoveryMiddleware_PassThrough(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h := recoveryMiddleware(next)
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRecoveryMiddleware_CatchesPanic(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	h := recoveryMiddleware(next)
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	// Should not propagate the panic.
	require.NotPanics(t, func() { h.ServeHTTP(w, r) })

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body APIError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, http.StatusInternalServerError, body.Code)
	assert.Equal(t, "internal server error", body.Message)
}

// ============================================================================
// corsMiddleware tests
// ============================================================================

func TestCorsMiddleware_SetsHeaders(t *testing.T) {
	Cfg.Api.CorsOrigin = "*"
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h := corsMiddleware(next)
	r := httptest.NewRequest("GET", "/api/cards", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "GET, POST, OPTIONS", w.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Content-Type, Authorization", w.Header().Get("Access-Control-Allow-Headers"))
}

func TestCorsMiddleware_OptionsReturns204(t *testing.T) {
	Cfg.Api.CorsOrigin = "*"
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	h := corsMiddleware(next)
	r := httptest.NewRequest("OPTIONS", "/api/cards", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.False(t, called, "next should not be called for OPTIONS")
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestCorsMiddleware_CallsNextForNonOptions(t *testing.T) {
	Cfg.Api.CorsOrigin = "*"
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		called = false
		h := corsMiddleware(next)
		r := httptest.NewRequest(method, "/", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assert.True(t, called, "next should be called for %s", method)
	}
}

// ============================================================================
// requestIDMiddleware tests
// ============================================================================

func TestRequestIDMiddleware_GeneratesIDWhenAbsent(t *testing.T) {
	var capturedID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID, _ = r.Context().Value(requestIDKey).(string)
		w.WriteHeader(http.StatusOK)
	})

	h := requestIDMiddleware(next)
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.NotEmpty(t, capturedID, "should generate a UUID if header absent")
	assert.Equal(t, capturedID, w.Header().Get("X-Request-ID"), "echoed header should match context")
}

func TestRequestIDMiddleware_ReusesIncomingHeader(t *testing.T) {
	const incomingID = "my-custom-request-id-12345"
	var capturedID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID, _ = r.Context().Value(requestIDKey).(string)
		w.WriteHeader(http.StatusOK)
	})

	h := requestIDMiddleware(next)
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Request-ID", incomingID)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, incomingID, capturedID, "should reuse the X-Request-ID from the client")
	assert.Equal(t, incomingID, w.Header().Get("X-Request-ID"))
}

func TestRequestIDMiddleware_InjectsIntoContext(t *testing.T) {
	var ctxID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID, _ = r.Context().Value(requestIDKey).(string)
		w.WriteHeader(http.StatusOK)
	})

	h := requestIDMiddleware(next)
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.NotEmpty(t, ctxID)
	assert.Equal(t, w.Header().Get("X-Request-ID"), ctxID)
}

// ============================================================================
// rateLimitMiddleware tests
// ============================================================================

func TestRateLimitMiddleware_FailOpenWhenRedisUnavailable(t *testing.T) {
	// cache.Client is nil in unit tests; Incr returns error → fail open.
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h := rateLimitMiddleware(next)
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code, "should pass through when Redis is unavailable")
}

// ============================================================================
// writeJSON tests
// ============================================================================

func TestWriteJSON_SetsContentTypeAndStatus(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusCreated, map[string]string{"key": "val"})

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
}

func TestWriteJSON_EncodesBody(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, payload{Name: "bitcoin"})

	var result payload
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.Equal(t, "bitcoin", result.Name)
}

// ============================================================================
// writeError tests
// ============================================================================

func TestWriteError_ReturnsAPIError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusNotFound, "not found", nil)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var body APIError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, http.StatusNotFound, body.Code)
	assert.Equal(t, "not found", body.Message)
	assert.Nil(t, body.Errors)
}

func TestWriteError_WithFieldErrors(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "validation failed", map[string]string{"field": "required"})

	var body APIError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "required", body.Errors["field"])
}

// ============================================================================
// handleError tests
// ============================================================================

func TestHandleError_NilReturnsFalse(t *testing.T) {
	w := httptest.NewRecorder()
	handled := handleError(w, nil)

	assert.False(t, handled)
	assert.Equal(t, 200, w.Code) // no response written
}

func TestHandleError_KnownErrorMapsToStatus(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus int
	}{
		{card.ErrCardNotFound, http.StatusNotFound},
		{card.ErrCardNotActive, http.StatusConflict},
		{card.ErrCardAlreadyUsed, http.StatusConflict},
		{card.ErrInsufficientFunds, http.StatusUnprocessableEntity},
		{card.ErrInvalidCurrency, http.StatusBadRequest},
		{card.ErrInvalidFiatAmount, http.StatusBadRequest},
		{card.ErrLightningInvoice, http.StatusBadRequest},
		{card.ErrInvalidAddress, http.StatusBadRequest},
		{card.ErrInvalidMethod, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			w := httptest.NewRecorder()
			handled := handleError(w, tt.err)

			assert.True(t, handled)
			assert.Equal(t, tt.wantStatus, w.Code)
			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
		})
	}
}

func TestHandleError_UnknownErrorReturns500(t *testing.T) {
	w := httptest.NewRecorder()
	handled := handleError(w, assert.AnError)

	assert.True(t, handled)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var body APIError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "internal server error", body.Message)
}

func TestHandleError_ReturnsTrueWhenWritten(t *testing.T) {
	w := httptest.NewRecorder()
	assert.True(t, handleError(w, card.ErrCardNotFound))
}
