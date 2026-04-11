package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recovery, handler'daki panic'leri yakalar ve 500 döner.
func Recovery(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic kurtarıldı",
						slog.Any("error", err),
						slog.String("stack", string(debug.Stack())),
						slog.String("request_id", GetRequestID(r.Context())),
					)
					http.Error(w, "iç sunucu hatası", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
