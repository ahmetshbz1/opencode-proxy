package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
)

type requestIDKey struct{}
type requestInfoKey struct{}

// RequestInfo, istek süresince mutasyona açık paylaşımlı bilgi taşır.
// Handler seçtiği provider'ı buraya yazar, logging middleware okur.
type RequestInfo struct {
	mu       sync.Mutex
	provider string
	cluster  string
}

func (i *RequestInfo) Set(provider, cluster string) {
	i.mu.Lock()
	i.provider = provider
	i.cluster = cluster
	i.mu.Unlock()
}

func (i *RequestInfo) Get() (string, string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.provider, i.cluster
}

// generateID, kriptografik olarak güvenli bir istek ID'si üretir.
func generateID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

// RequestID, her isteğe benzersiz bir X-Request-ID ve boş bir RequestInfo ekler.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = generateID()
		}
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		ctx = context.WithValue(ctx, requestInfoKey{}, &RequestInfo{})
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetRequestID, context'ten istek ID'sini çeker.
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// GetRequestInfo, context'ten paylaşımlı RequestInfo'yu çeker.
func GetRequestInfo(ctx context.Context) *RequestInfo {
	if info, ok := ctx.Value(requestInfoKey{}).(*RequestInfo); ok {
		return info
	}
	return nil
}
