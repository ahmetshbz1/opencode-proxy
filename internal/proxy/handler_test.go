package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"opencode-proxy/internal/anthropic"
	"opencode-proxy/internal/provider"
)

type mockProvider struct {
	name     string
	priority int
	err      error
}

func (m *mockProvider) Name() string  { return m.name }
func (m *mockProvider) Priority() int { return m.priority }
func (m *mockProvider) Proxy(_ context.Context, w http.ResponseWriter, _ []byte, _ anthropic.Request) error {
	if m.err != nil {
		return m.err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"id":"msg-test","type":"message"}`))
	return nil
}

func newTestHandler(_ []provider.Provider) *Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := provider.NewRegistry(http.DefaultClient, logger)
	registry.RebuildFromConfig(nil)
	return NewHandler(registry, logger)
}

func TestHandlerMethodNotAllowed(t *testing.T) {
	h := newTestHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandlerInvalidBody(t *testing.T) {
	h := newTestHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte("invalid")))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandlerAllProvidersFail(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := provider.NewRegistry(http.DefaultClient, logger)
	registry.RebuildFromConfig(nil)

	h := NewHandler(registry, logger)

	body, _ := json.Marshal(anthropic.Request{Model: "test", MaxTokens: 100})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
}
