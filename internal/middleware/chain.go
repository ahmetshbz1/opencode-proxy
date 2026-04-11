package middleware

import "net/http"

// Middleware, bir HTTP handler'ı saran fonksiyon tipidir.
type Middleware func(http.Handler) http.Handler

// Chain, middleware'leri dıştan içe doğru uygular.
// Chain(handler, a, b, c) → a(b(c(handler)))
func Chain(final http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		final = mws[i](final)
	}
	return final
}
