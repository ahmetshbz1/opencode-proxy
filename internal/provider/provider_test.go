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
	"opencode-proxy/internal/openai"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAnthropicPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-test" {
			t.Errorf("api key = %q, want %q", r.Header.Get("x-api-key"), "sk-test")
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("anthropic-version = %q", r.Header.Get("anthropic-version"))
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg-123","type":"message"}`))
	}))
	defer upstream.Close()

	p := NewAnthropic(config.Provider{
		Name: "test", Type: "anthropic",
		BaseURL: upstream.URL, APIKey: "sk-test", Priority: 1,
	}, http.DefaultClient, newTestLogger())

	w := httptest.NewRecorder()
	body, _ := json.Marshal(anthropic.Request{Model: "test-model", MaxTokens: 100})
	err := p.Proxy(context.Background(), w, body, anthropic.Request{Model: "test-model"})

	if err != nil {
		t.Fatalf("Proxy hatası: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestAnthropicFailover(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	p := NewAnthropic(config.Provider{
		Name: "rate-limited", Type: "anthropic",
		BaseURL: upstream.URL, APIKey: "sk-test", Priority: 1,
	}, http.DefaultClient, newTestLogger())

	w := httptest.NewRecorder()
	err := p.Proxy(context.Background(), w, nil, anthropic.Request{})

	if err == nil {
		t.Fatal("beklenen hata yok")
	}
	pe, ok := err.(*ProxyError)
	if !ok {
		t.Fatalf("beklenen *ProxyError, got %T", err)
	}
	if !pe.Retryable {
		t.Error("429 yeniden denenebilir olmalı")
	}
}

func TestOpenAINonStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("authorization = %q, want %q", r.Header.Get("Authorization"), "Bearer sk-test")
		}

		content := "merhaba"
		finish := "stop"
		resp := openai.Response{ID: "chatcmpl-123"}
		resp.Choices = make([]struct {
			Message struct {
				Role      string            `json:"role"`
				Content   *string           `json:"content"`
				ToolCalls []openai.ToolCall `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason *string `json:"finish_reason"`
		}, 1)
		resp.Choices[0].Message.Role = "assistant"
		resp.Choices[0].Message.Content = &content
		resp.Choices[0].FinishReason = &finish
		resp.Usage.PromptTokens = 10
		resp.Usage.CompletionTokens = 5

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer upstream.Close()

	p := NewOpenAI(config.Provider{
		Name: "test-oai", Type: "openai",
		BaseURL: upstream.URL, APIKey: "sk-test", Priority: 1,
	}, http.DefaultClient, newTestLogger())

	w := httptest.NewRecorder()
	antReq := anthropic.Request{Model: "test-model", MaxTokens: 100}
	err := p.Proxy(context.Background(), w, nil, antReq)

	if err != nil {
		t.Fatalf("Proxy hatası: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestRegistryOrdered(t *testing.T) {
	registry := NewRegistry(http.DefaultClient, newTestLogger())
	registry.RebuildFromConfig([]config.Provider{
		{Name: "c", Type: "anthropic", BaseURL: "http://c", APIKey: "k", Priority: 3},
		{Name: "a", Type: "anthropic", BaseURL: "http://a", APIKey: "k", Priority: 1},
		{Name: "b", Type: "anthropic", BaseURL: "http://b", APIKey: "k", Priority: 2},
	})

	ordered := registry.Ordered()
	if len(ordered) != 3 {
		t.Fatalf("count = %d, want 3", len(ordered))
	}
	if ordered[0].Name() != "a" {
		t.Errorf("ilk = %q, want %q", ordered[0].Name(), "a")
	}
	if ordered[1].Name() != "b" {
		t.Errorf("ikinci = %q, want %q", ordered[1].Name(), "b")
	}
	if ordered[2].Name() != "c" {
		t.Errorf("üçüncü = %q, want %q", ordered[2].Name(), "c")
	}
}

func TestRegistryOrderedForModel(t *testing.T) {
	registry := NewRegistry(http.DefaultClient, newTestLogger())
	registry.RebuildFromConfig([]config.Provider{
		{Name: "glm", Type: "anthropic", BaseURL: "http://glm", APIKey: "k", Priority: 1, Models: []string{"glm-5.1"}},
		{Name: "codex", Type: "codex", BaseURL: "http://codex", Priority: 2, OAuth: &config.OAuthConfig{RefreshToken: "r"}, Models: []string{"gpt-5.4", "gpt-5.4-*"}},
		{Name: "fallback", Type: "anthropic", BaseURL: "http://fallback", APIKey: "k", Priority: 3},
	})

	gotGPT := registry.OrderedForModel("gpt-5.4")
	if len(gotGPT) != 2 {
		t.Fatalf("gpt providers = %d, want 2", len(gotGPT))
	}
	if gotGPT[0].Name() != "codex" {
		t.Fatalf("gpt ilk provider = %q, want codex", gotGPT[0].Name())
	}
	if gotGPT[1].Name() != "fallback" {
		t.Fatalf("gpt ikinci provider = %q, want fallback", gotGPT[1].Name())
	}

	gotGLM := registry.OrderedForModel("glm-5.1")
	if len(gotGLM) != 2 {
		t.Fatalf("glm providers = %d, want 2", len(gotGLM))
	}
	if gotGLM[0].Name() != "glm" {
		t.Fatalf("glm ilk provider = %q, want glm", gotGLM[0].Name())
	}
	if gotGLM[1].Name() != "fallback" {
		t.Fatalf("glm ikinci provider = %q, want fallback", gotGLM[1].Name())
	}

	gotWildcard := registry.OrderedForModel("gpt-5.4-mini")
	if len(gotWildcard) != 2 {
		t.Fatalf("wildcard providers = %d, want 2", len(gotWildcard))
	}
	if gotWildcard[0].Name() != "codex" {
		t.Fatalf("wildcard ilk provider = %q, want codex", gotWildcard[0].Name())
	}
	if gotWildcard[1].Name() != "fallback" {
		t.Fatalf("wildcard ikinci provider = %q, want fallback", gotWildcard[1].Name())
	}
}

func TestRegistryOrderedForModelPrefersExplicitMatchesOverCatchAll(t *testing.T) {
	registry := NewRegistry(http.DefaultClient, newTestLogger())
	registry.RebuildFromConfig([]config.Provider{
		{Name: "glm", Type: "anthropic", BaseURL: "http://glm", APIKey: "k", Priority: 1, Models: []string{"glm-5.1"}},
		{Name: "catch-all-openai", Type: "openai", BaseURL: "http://oai", APIKey: "k", Priority: 2},
		{Name: "codex", Type: "codex", BaseURL: "http://codex", Priority: 3, OAuth: &config.OAuthConfig{RefreshToken: "r"}, Models: []string{"gpt-5.4", "gpt-5.4-*"}},
	})

	got := registry.OrderedForModel("gpt-5.4")
	if len(got) != 2 {
		t.Fatalf("providers = %d, want 2", len(got))
	}
	if got[0].Name() != "codex" {
		t.Fatalf("ilk provider = %q, want codex", got[0].Name())
	}
	if got[1].Name() != "catch-all-openai" {
		t.Fatalf("ikinci provider = %q, want catch-all-openai", got[1].Name())
	}
}
