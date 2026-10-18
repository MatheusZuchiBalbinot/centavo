package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"

	"centavo/bank/internal/platform/correlation"
)

const correlationHeader = "X-Correlation-Id"

// requestDeadline bounds each request (docs/architecture.md, section 7).
const requestDeadline = 5 * time.Second

// correlationFormat keeps log lines and stored ids free of control characters.
var correlationFormat = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// withRequestContext attaches the correlation id and the request deadline.
func withRequestContext(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID, err := correlationIDOf(r)
		if err != nil {
			writeError(w, r, logger, err)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), requestDeadline)
		defer cancel()

		ctx = correlation.With(ctx, correlationID)
		w.Header().Set(correlationHeader, correlationID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// correlationIDOf takes the client's id (the load generator sends the
// operation id) or generates one.
func correlationIDOf(r *http.Request) (string, error) {
	received := r.Header.Get(correlationHeader)
	if received == "" {
		return newCorrelationID()
	}

	if !correlationFormat.MatchString(received) {
		return "", fmt.Errorf("%w: %s must have 1 to 128 characters among letters, digits and . _ : -", errValidation, correlationHeader)
	}

	return received, nil
}

func newCorrelationID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generating correlation id: %w", err)
	}

	return id.String(), nil
}

func correlationIDFrom(ctx context.Context) string {
	return correlation.From(ctx)
}

// securityHeaders lock a JSON API down: nothing it answers is a page to
// render, frame or sniff.
var securityHeaders = map[string]string{
	"X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer",
	"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
}

// withSecurityHeaders sets securityHeaders on every answer.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, value := range securityHeaders {
			w.Header().Set(name, value)
		}

		next.ServeHTTP(w, r)
	})
}
