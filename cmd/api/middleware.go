package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"btc-giftcard/internal/card"
	"btc-giftcard/pkg/logger"

	"go.uber.org/zap"
)

// ============================================================================
// Response writer wrapper
// ============================================================================

// responseWriter wraps http.ResponseWriter to capture the status code written
// by the handler. http.ResponseWriter has no status getter, so without this
// wrapper loggingMiddleware would always log 0.
//
// All other methods (Header, Write) are promoted from the embedded
// http.ResponseWriter automatically — only WriteHeader needs to be intercepted.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{ResponseWriter: w, status: http.StatusOK}
}

// WriteHeader captures the status before delegating to the real writer.
func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

// ============================================================================
// Middleware
// ============================================================================

// loggingMiddleware logs every request AFTER it completes so it captures
// the actual status code and total duration (including time spent in
// downstream middleware). Must be the outermost middleware in the chain.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := newResponseWriter(w)
		next.ServeHTTP(rw, r)
		logger.Info("request",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", rw.status),
			zap.Duration("duration", time.Since(start)),
		)
	})
}

// recoveryMiddleware catches panics in handlers and returns a 500 JSON
// response instead of crashing the process. Must be placed inside
// loggingMiddleware so the 500 is captured in the access log.
//
// TODO: Once requestIDMiddleware is added, include the request ID in the
// panic log for easier incident correlation:
//
//	logger.Error("panic", zap.Any("panic", rec), zap.Stack("stack"),
//	    zap.String("request_id", r.Context().Value(requestIDKey).(string)))
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("panic recovered",
					zap.Any("panic", rec),
					zap.Stack("stack"),
				)
				writeJSON(w, http.StatusInternalServerError, APIError{
					Code:    http.StatusInternalServerError,
					Message: "internal server error",
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware sets CORS headers and short-circuits OPTIONS preflight requests.
//
// TODO: Move allowed origin to config.toml under [api] cors_origin.
// TODO: For production, restrict to specific origins instead of "*".
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", Cfg.Cors.Origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// TODO: requestIDMiddleware
// Injects a unique correlation ID into every request so logs across services
// can be tied together.
//
// Implementation:
//  1. Read X-Request-ID header; generate a UUID if absent (crypto/rand or google/uuid)
//  2. Store in context: ctx := context.WithValue(r.Context(), requestIDKey, id)
//  3. Echo back: w.Header().Set("X-Request-ID", id)
//  4. Call next.ServeHTTP(w, r.WithContext(ctx))
func requestIDMiddleware(next http.Handler) http.Handler {
	panic("not implemented yet")
}

// TODO: rateLimitMiddleware
// Limits requests per remote IP using a Redis sliding window counter.
//
// Implementation:
//  1. Extract client IP from r.RemoteAddr (net.SplitHostPort)
//  2. cache.Incr(ctx, "ratelimit:"+ip) — set TTL on first increment (e.g. 60s)
//  3. If count > limit (e.g. 100 req/min): writeError(w, 429, "rate limit exceeded", nil)
//  4. Otherwise call next.ServeHTTP(w, r)
func rateLimitMiddleware(next http.Handler) http.Handler {
	panic("not implemented yet")
}

// ============================================================================
// HTTP response helpers (used by handlers and middleware)
// ============================================================================

// APIError is the standard JSON error envelope returned by all endpoints.
type APIError struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Errors  map[string]string `json:"errors,omitempty"`
}

// errorStatusMap maps known service errors to HTTP status codes.
// Looked up via errors.Is (not direct key access) so wrapped errors
// like fmt.Errorf("...: %w", ErrCardNotFound) are matched correctly.
var errorStatusMap = map[error]int{
	card.ErrInvalidCurrency:   http.StatusBadRequest,
	card.ErrInvalidFiatAmount: http.StatusBadRequest,
	card.ErrInvalidPurchase:   http.StatusBadRequest,
	card.ErrMissingEmail:      http.StatusBadRequest,
	card.ErrCardNotFound:      http.StatusNotFound,
	card.ErrCardNotActive:     http.StatusConflict,
	card.ErrCardAlreadyUsed:   http.StatusConflict,
	card.ErrInsufficientFunds: http.StatusUnprocessableEntity,
	card.ErrInvalidMethod:     http.StatusBadRequest,
	card.ErrInvalidAddress:    http.StatusBadRequest,
	card.ErrLightningInvoice:  http.StatusBadRequest,
}

// writeJSON writes data as a JSON response with the given HTTP status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// writeError writes a structured JSON error response.
func writeError(w http.ResponseWriter, status int, msg string, fieldErrors map[string]string) {
	writeJSON(w, status, APIError{
		Code:    status,
		Message: msg,
		Errors:  fieldErrors,
	})
}

// handleError maps a service error to an HTTP response and writes it.
// Returns true if an error was handled (response written), false if err is nil.
func handleError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	for target, status := range errorStatusMap {
		if errors.Is(err, target) {
			writeError(w, status, err.Error(), nil)
			return true
		}
	}
	logger.Error("unexpected error", zap.Error(err))
	writeError(w, http.StatusInternalServerError, "internal server error", nil)
	return true
}
