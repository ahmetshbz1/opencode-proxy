package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// responseWriter, http.ResponseWriter'ı sararak durum kodunu yakalar.
type responseWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if rw.wrote {
		return
	}
	rw.status = code
	rw.wrote = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Logging, slog ile her isteği loglar.
func Logging(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			var providerName, cluster string
			if info := GetRequestInfo(r.Context()); info != nil {
				providerName, cluster = info.Get()
			}

			logger.Info("istek tamamlandı",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", wrapped.status),
				slog.Duration("duration", time.Since(start)),
				slog.String("cluster", cluster),
				slog.String("provider", providerName),
				slog.String("request_id", GetRequestID(r.Context())),
			)
		})
	}
}
