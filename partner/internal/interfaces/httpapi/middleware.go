package httpapi

import "net/http"

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
