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
	"opencode-proxy/internal/config"
	"opencode-proxy/internal/provider"
)

type mockProvider struct {
	name      string
	priority  int
	err       error
	calls     *int
	statusCode int
}

func (m *mockProvider) Name() string  { return m.name }
func (m *mockProvider) Priority() int { return m.priority }
func (m *mockProvider) Proxy(_ context.Context, w http.ResponseWriter, _ []byte, _ anthropic.Request) error {
	if m.calls != nil {
		*m.calls = *m.calls + 1
	}
	if m.err != nil {
		return m.err
	}
	w.Header().Set("Content-Type", "application/json")
	if m.statusCode != 0 {
		w.WriteHeader(m.statusCode)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_, _ = w.Write([]byte(`{"id":"msg-test","type":"message"}`))
	return nil
}

func newTestHandler(_ []provider.Provider) *Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := provider.NewRegistry(http.DefaultClient, logger)
	registry.RebuildFromConfig(nil)
	return NewHandler(registry, logger)
}

func newConfiguredHandler(cfgs []config.Provider) *Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := provider.NewRegistry(http.DefaultClient, logger)
	registry.RebuildFromConfig(cfgs)
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

func TestHandlerSkipsCatchAllForExplicitModel(t *testing.T) {
	explicitCalls := 0
	catchAllCalls := 0

	explicit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		explicitCalls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"codex failed"}`))
	}))
	defer explicit.Close()

	catchAll := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		catchAllCalls++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg-fallback","type":"message"}`))
	}))
	defer catchAll.Close()

	h := newConfiguredHandler([]config.Provider{
		{Name: "codex-like", Type: "anthropic", BaseURL: explicit.URL, APIKey: "k", Priority: 0, Models: []string{"gpt-5.4", "gpt-5.4-*"}},
		{Name: "catch-all", Type: "anthropic", BaseURL: catchAll.URL, APIKey: "k", Priority: 0},
	})

	body, _ := json.Marshal(anthropic.Request{Model: "gpt-5.4", MaxTokens: 100})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if explicitCalls == 0 {
		t.Fatal("explicit provider hiç çağrılmadı")
	}
	if catchAllCalls != 0 {
		t.Fatalf("catch-all provider %d kez çağrıldı, want 0", catchAllCalls)
	}
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
}

func TestHandlerCountTokensSkipsCatchAllForExplicitModel(t *testing.T) {
	explicitCalls := 0
	catchAllCalls := 0

	explicit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		explicitCalls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"count failed"}`))
	}))
	defer explicit.Close()

	catchAll := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		catchAllCalls++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg-fallback","type":"message"}`))
	}))
	defer catchAll.Close()

	h := newConfiguredHandler([]config.Provider{
		{Name: "codex-like", Type: "anthropic", BaseURL: explicit.URL, APIKey: "k", Priority: 0, Models: []string{"gpt-5.4", "gpt-5.4-*"}},
		{Name: "catch-all", Type: "anthropic", BaseURL: catchAll.URL, APIKey: "k", Priority: 0},
	})

	body, _ := json.Marshal(anthropic.Request{Model: "gpt-5.4", MaxTokens: 100})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if explicitCalls == 0 {
		t.Fatal("explicit provider hiç çağrılmadı")
	}
	if catchAllCalls != 0 {
		t.Fatalf("catch-all provider %d kez çağrıldı, want 0", catchAllCalls)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandlerRoutesGLMOnlyWithinGLMProviders(t *testing.T) {
	zaiCalls := 0
	opencodeCalls := 0
	codexCalls := 0

	zai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zaiCalls++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"quota"}`))
	}))
	defer zai.Close()

	opencode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opencodeCalls++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg-glm","type":"message"}`))
	}))
	defer opencode.Close()

	codex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		codexCalls++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg-codex","type":"message"}`))
	}))
	defer codex.Close()

	h := newConfiguredHandler([]config.Provider{
		{Name: "z.ai", Type: "anthropic", BaseURL: zai.URL, APIKey: "k", Priority: 0, Models: []string{"glm-5.1", "glm-*"}},
		{Name: "opencode-go", Type: "anthropic", BaseURL: opencode.URL, APIKey: "k", Priority: 0, Models: []string{"glm-5.1", "glm-*"}},
		{Name: "codex-oauth", Type: "anthropic", BaseURL: codex.URL, APIKey: "k", Priority: 0, Models: []string{"gpt-5.4", "gpt-5.4-*"}},
	})

	body, _ := json.Marshal(anthropic.Request{Model: "glm-5.1", MaxTokens: 100})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if zaiCalls == 0 {
		t.Fatal("z.ai hiç çağrılmadı")
	}
	if opencodeCalls == 0 {
		t.Fatal("opencode-go hiç çağrılmadı")
	}
	if codexCalls != 0 {
		t.Fatalf("codex %d kez çağrıldı, want 0", codexCalls)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
