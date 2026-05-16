package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"btc-giftcard/internal/card"
	"btc-giftcard/pkg/cache"
	"btc-giftcard/pkg/logger"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	requestIDKey    = "request_id"
	rateLimitPrefix = "ratelimit:"
	rateLimitValue  = 100
	rateLimitTTL    = 60 * time.Second
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
// downstream middleware). Must sit inside requestIDMiddleware so the
// request ID is already in context when the log line is written.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := newResponseWriter(w)
		next.ServeHTTP(rw, r)
		reqID, _ := r.Context().Value(requestIDKey).(string)
		logger.Info("request",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", rw.status),
			zap.Duration("duration", time.Since(start)),
			zap.String("request_id", reqID),
		)
	})
}

// recoveryMiddleware catches panics in handlers and returns a 500 JSON
// response instead of crashing the process. Must be placed inside
// loggingMiddleware so the 500 status is captured in the access log.
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				reqID, _ := r.Context().Value(requestIDKey).(string)
				logger.Error("panic recovered",
					zap.Any("panic", rec),
					zap.Stack("stack"),
					zap.String("request_id", reqID),
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
		w.Header().Set("Access-Control-Allow-Origin", Cfg.Api.CorsOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// requestIDMiddleware injects a correlation ID into every request.
// It reuses the X-Request-ID header sent by the client (e.g. from a load
// balancer) if present; otherwise it generates a new UUID. The ID is stored
// in context and echoed back so callers can trace a request end-to-end.
//
// Must be the outermost middleware so the ID is available in every
// downstream middleware (logging, recovery, rate limiting).
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.New().String()
		}
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// rateLimitMiddleware limits requests per remote IP using a fixed-window
// counter backed by Redis.
//
// On the first request within a window, Incr returns 1 and we set a TTL
// (rateLimitTTL) so the counter resets automatically without a background job.
// On subsequent requests within the same window, Incr returns 2, 3, … and we
// compare against rateLimitValue.
//
// Fail-open strategy: if Redis is unavailable we let the request through
// rather than blocking all traffic during a cache outage.
func rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ctx := r.Context()

		count, err := cache.Incr(ctx, rateLimitPrefix+host)
		if err != nil {
			// Fail open: Redis is down, let the request through.
			logger.Warn("rate limit check failed, failing open",
				zap.String("ip", host),
				zap.Error(err),
			)
			next.ServeHTTP(w, r)
			return
		}

		// Set the window TTL on the first increment so the counter expires
		// automatically. Ignore the error — worst case the key has no TTL
		// and resets on next restart.
		if count == 1 {
			_ = cache.Expire(ctx, rateLimitPrefix+host, rateLimitTTL)
		}

		if count > rateLimitValue {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded", nil)
			return
		}

		next.ServeHTTP(w, r)
	})
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
	card.ErrMissingEmail:      http.StatusBadRequest,
	card.ErrEmptyItems:        http.StatusBadRequest,
	card.ErrInvalidQuantity:   http.StatusBadRequest,
	card.ErrCardNotFound:      http.StatusNotFound,
	card.ErrCardNotActive:     http.StatusConflict,
	card.ErrCardAlreadyUsed:   http.StatusConflict,
	card.ErrInsufficientFunds: http.StatusUnprocessableEntity,
	card.ErrLightningInvoice:  http.StatusBadRequest,
	card.ErrInvalidMethod:     http.StatusBadRequest,
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
