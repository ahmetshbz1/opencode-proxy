package provider

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"opencode-proxy/internal/anthropic"
	"opencode-proxy/internal/config"
)

func TestAnthropicPassthroughRejectsMissingAPIKey(t *testing.T) {
	p := NewAnthropicPassthrough(config.Provider{Name: "claude-native", BaseURL: "https://api.anthropic.com"}, http.DefaultClient, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w := httptest.NewRecorder()
	err := p.Proxy(context.Background(), w, []byte(`{"model":"claude-opus-4-7"}`), anthropic.Request{Model: "claude-opus-4-7"})
	if err == nil {
		t.Fatal("beklenen hata yok")
	}
	proxyErr, ok := err.(*ProxyError)
	if !ok {
		t.Fatalf("hata tipi = %T, want *ProxyError", err)
	}
	if proxyErr.Retryable {
		t.Fatal("API anahtarı yokken hata retryable olmamalı")
	}
}

func TestAnthropicPassthroughRewritesBodyAndForwardsHeaders(t *testing.T) {
	var receivedBody map[string]any
	var receivedAPIKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAPIKey = r.Header.Get("x-api-key")
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("body decode hatası: %v", err)
		}
		w.Header().Set("x-upstream-header", "ok")
		w.Header().Set("content-length", "999")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg-test"}`))
	}))
	defer server.Close()

	p := NewAnthropicPassthrough(config.Provider{Name: "claude-native", BaseURL: server.URL}, http.DefaultClient, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := WithAPIKey(context.Background(), "secret-key")
	w := httptest.NewRecorder()
	body := []byte(`{"model":"claude-opus-4-7","messages":[],"context_management":{"foo":"bar"}}`)

	err := p.Proxy(ctx, w, body, anthropic.Request{Model: "claude-opus-4-7"})
	if err != nil {
		t.Fatalf("Proxy hatası: %v", err)
	}
	if receivedAPIKey != "secret-key" {
		t.Fatalf("x-api-key = %q, want secret-key", receivedAPIKey)
	}
	if _, ok := receivedBody["context_management"]; ok {
		t.Fatal("context_management upstream'e gönderilmemeli")
	}
	if receivedBody["max_tokens"] != float64(1) {
		t.Fatalf("max_tokens = %v, want 1", receivedBody["max_tokens"])
	}
	if w.Header().Get("x-upstream-header") != "ok" {
		t.Fatal("upstream header kopyalanmadı")
	}
	if got := w.Header().Get("content-length"); got != "" {
		t.Fatalf("content-length kopyalanmamalı, got %q", got)
	}
}

func TestAnthropicPassthroughMapsRetryableStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`quota exceeded`))
	}))
	defer server.Close()

	p := NewAnthropicPassthrough(config.Provider{Name: "claude-native", BaseURL: server.URL}, http.DefaultClient, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := WithAPIKey(context.Background(), "secret-key")
	w := httptest.NewRecorder()
	err := p.Proxy(ctx, w, []byte(`{"model":"claude-opus-4-7","messages":[],"max_tokens":1}`), anthropic.Request{Model: "claude-opus-4-7"})
	if err == nil {
		t.Fatal("beklenen hata yok")
	}
	proxyErr, ok := err.(*ProxyError)
	if !ok {
		t.Fatalf("hata tipi = %T, want *ProxyError", err)
	}
	if !proxyErr.Retryable {
		t.Fatal("429 için hata retryable olmalı")
	}
	if proxyErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", proxyErr.StatusCode, http.StatusTooManyRequests)
	}
}
