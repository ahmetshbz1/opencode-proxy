package provider

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestQuotaResetTimeUsesResetsInSeconds(t *testing.T) {
	now := time.Unix(1000, 0)
	err := &ProxyError{Message: `{"error":{"resets_at":9999,"resets_in_seconds":42}}`}

	got := QuotaResetTime(err, now)
	want := now.Add(42 * time.Second)

	if !got.Equal(want) {
		t.Fatalf("reset = %s, want %s", got, want)
	}
}

func TestQuotaResetTimeUsesResetsAt(t *testing.T) {
	now := time.Unix(1000, 0)
	err := &ProxyError{Message: `{"error":{"resets_at":1600}}`}

	got := QuotaResetTime(err, now)
	want := time.Unix(1600, 0)

	if !got.Equal(want) {
		t.Fatalf("reset = %s, want %s", got, want)
	}
}

func TestQuotaResetTimeFallsBackToDefault(t *testing.T) {
	now := time.Unix(1000, 0)
	err := &ProxyError{Message: `{"error":{"message":"usage limit reached"}}`}

	got := QuotaResetTime(err, now)
	want := now.Add(defaultExhaustedResetInterval)

	if !got.Equal(want) {
		t.Fatalf("reset = %s, want %s", got, want)
	}
}

func TestActiveTrackerMarkExhaustedUntil(t *testing.T) {
	tracker := NewActiveTracker()
	tracker.MarkExhaustedUntil("codex", time.Now().Add(50*time.Millisecond))

	if !tracker.IsExhausted("codex") {
		t.Fatal("provider exhausted değil")
	}
	time.Sleep(70 * time.Millisecond)
	if tracker.IsExhausted("codex") {
		t.Fatal("provider reset süresi sonrası hâlâ exhausted")
	}
}

func TestActiveTrackerChangeHandlerRunsOnStateChange(t *testing.T) {
	tracker := NewActiveTracker()
	changes := 0
	tracker.SetChangeHandler(func() { changes++ })

	tracker.MarkExhaustedUntil("codex", time.Now().Add(time.Hour))
	tracker.SetCurrent("gpt-5.4", "codex")
	tracker.ClearCurrent("gpt-5.4", "codex")
	tracker.ClearExhausted("codex")

	if changes != 4 {
		t.Fatalf("changes = %d, want 4", changes)
	}
}

func TestOpenAINonStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("authorization = %q, want %q", r.Header.Get("Authorization"), "Bearer sk-test")
		}

		content := "merhaba"
		finish := "stop"
		reasoning := "düşünce"
		resp := openai.Response{ID: "chatcmpl-123"}
		resp.Choices = []struct {
			Message struct {
				Role             string            `json:"role"`
				Content          *string           `json:"content"`
				ReasoningContent *string           `json:"reasoning_content,omitempty"`
				Reasoning        *string           `json:"reasoning,omitempty"`
				Thinking         *string           `json:"thinking,omitempty"`
				ToolCalls        []openai.ToolCall `json:"tool_calls,omitempty"`
			} `json:"message"`
			FinishReason *string `json:"finish_reason"`
		}{
			{
				Message: struct {
					Role             string            `json:"role"`
					Content          *string           `json:"content"`
					ReasoningContent *string           `json:"reasoning_content,omitempty"`
					Reasoning        *string           `json:"reasoning,omitempty"`
					Thinking         *string           `json:"thinking,omitempty"`
					ToolCalls        []openai.ToolCall `json:"tool_calls,omitempty"`
				}{
					Role:      "assistant",
					Content:   &content,
					Reasoning: &reasoning,
				},
				FinishReason: &finish,
			},
		}
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
	if !strings.Contains(w.Body.String(), "düşünce") {
		t.Fatalf("thinking içeriği yok: %s", w.Body.String())
	}
}

func TestRegistryUsageCachesWithinTTL(t *testing.T) {
	calls := 0
	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"email":"cache@example.com",
			"plan_type":"plus",
			"rate_limit_reached_type":null,
			"rate_limit":{
				"allowed":true,
				"limit_reached":false,
				"primary_window":{"used_percent":10,"limit_window_seconds":18000,"reset_after_seconds":3600,"reset_at":1777017600},
				"secondary_window":{"used_percent":20,"limit_window_seconds":604800,"reset_after_seconds":129552,"reset_at":1777449364}
			}
		}`))
	}))
	defer usageServer.Close()

	registry := NewRegistry(usageServer.Client(), newTestLogger())
	registry.RebuildFromConfig([]config.Provider{{
		Name:    "codex",
		Type:    "codex",
		BaseURL: usageServer.URL + "/backend-api/codex",
		OAuth: &config.OAuthConfig{
			AccessToken: "access-token",
			ExpiresAt:   time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}})

	first, err := registry.Usage(context.Background(), "codex")
	if err != nil {
		t.Fatalf("ilk usage hatası: %v", err)
	}
	second, err := registry.Usage(context.Background(), "codex")
	if err != nil {
		t.Fatalf("ikinci usage hatası: %v", err)
	}
	if calls != 1 {
		t.Fatalf("usage endpoint çağrısı = %d, want 1", calls)
	}
	if first.Email != "cache@example.com" || second.Email != "cache@example.com" {
		t.Fatalf("usage cache beklenen veriyi döndürmedi: %#v %#v", first, second)
	}
}

func TestRegistryOrderedPreservesConfigOrder(t *testing.T) {
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
	if ordered[0].Name() != "c" {
		t.Errorf("ilk = %q, want %q", ordered[0].Name(), "c")
	}
	if ordered[1].Name() != "a" {
		t.Errorf("ikinci = %q, want %q", ordered[1].Name(), "a")
	}
	if ordered[2].Name() != "b" {
		t.Errorf("üçüncü = %q, want %q", ordered[2].Name(), "b")
	}
}

func TestRegistryOrderedForModel(t *testing.T) {
	registry := NewRegistry(http.DefaultClient, newTestLogger())
	registry.RebuildFromConfig([]config.Provider{
		{Name: "glm", Type: "anthropic", BaseURL: "http://glm", APIKey: "k", Priority: 0, Models: []string{"glm-5.1"}},
		{Name: "codex", Type: "codex", BaseURL: "http://codex", Priority: 0, OAuth: &config.OAuthConfig{RefreshToken: "r"}, Models: []string{"gpt-5.4", "gpt-5.4-*"}},
		{Name: "fallback", Type: "anthropic", BaseURL: "http://fallback", APIKey: "k", Priority: 0},
	})

	gotGPT := registry.OrderedForModel("gpt-5.4")
	if len(gotGPT) != 1 {
		t.Fatalf("gpt providers = %d, want 1", len(gotGPT))
	}
	if gotGPT[0].Name() != "codex" {
		t.Fatalf("gpt ilk provider = %q, want codex", gotGPT[0].Name())
	}

	gotGLM := registry.OrderedForModel("glm-5.1")
	if len(gotGLM) != 1 {
		t.Fatalf("glm providers = %d, want 1", len(gotGLM))
	}
	if gotGLM[0].Name() != "glm" {
		t.Fatalf("glm ilk provider = %q, want glm", gotGLM[0].Name())
	}

	gotWildcard := registry.OrderedForModel("gpt-5.4-mini")
	if len(gotWildcard) != 1 {
		t.Fatalf("wildcard providers = %d, want 1", len(gotWildcard))
	}
	if gotWildcard[0].Name() != "codex" {
		t.Fatalf("wildcard ilk provider = %q, want codex", gotWildcard[0].Name())
	}
}

func TestRegistryOrderedForModelSkipsCatchAllWhenExplicitExists(t *testing.T) {
	registry := NewRegistry(http.DefaultClient, newTestLogger())
	registry.RebuildFromConfig([]config.Provider{
		{Name: "glm", Type: "anthropic", BaseURL: "http://glm", APIKey: "k", Priority: 0, Models: []string{"glm-5.1"}},
		{Name: "catch-all-openai", Type: "openai", BaseURL: "http://oai", APIKey: "k", Priority: 0},
		{Name: "codex", Type: "codex", BaseURL: "http://codex", Priority: 0, OAuth: &config.OAuthConfig{RefreshToken: "r"}, Models: []string{"gpt-5.4", "gpt-5.4-*"}},
	})

	got := registry.OrderedForModel("gpt-5.4")
	if len(got) != 1 {
		t.Fatalf("providers = %d, want 1", len(got))
	}
	if got[0].Name() != "codex" {
		t.Fatalf("ilk provider = %q, want codex", got[0].Name())
	}
}

func TestRegistryOrderedForModelKeepsExplicitModelGroupOrder(t *testing.T) {
	registry := NewRegistry(http.DefaultClient, newTestLogger())
	registry.RebuildFromConfig([]config.Provider{
		{Name: "opencode-go", Type: "openai", BaseURL: "http://oai", APIKey: "k", Priority: 0, Models: []string{"glm-5.1", "glm-*"}},
		{Name: "z.ai", Type: "anthropic", BaseURL: "http://glm", APIKey: "k", Priority: 0, Models: []string{"glm-5.1", "glm-*"}},
		{Name: "codex", Type: "codex", BaseURL: "http://codex", Priority: 0, OAuth: &config.OAuthConfig{RefreshToken: "r"}, Models: []string{"gpt-5.4", "gpt-5.4-*"}},
	})

	got := registry.OrderedForModel("glm-5.1")
	if len(got) != 2 {
		t.Fatalf("providers = %d, want 2", len(got))
	}
	if got[0].Name() != "opencode-go" {
		t.Fatalf("ilk provider = %q, want opencode-go", got[0].Name())
	}
	if got[1].Name() != "z.ai" {
		t.Fatalf("ikinci provider = %q, want z.ai", got[1].Name())
	}
}

func TestRegistryOrderedForModelFallsBackToCatchAllWithoutExplicitMatch(t *testing.T) {
	registry := NewRegistry(http.DefaultClient, newTestLogger())
	registry.RebuildFromConfig([]config.Provider{
		{Name: "codex", Type: "codex", BaseURL: "http://codex", Priority: 0, OAuth: &config.OAuthConfig{RefreshToken: "r"}, Models: []string{"gpt-5.4", "gpt-5.4-*"}},
		{Name: "catch-all-openai", Type: "openai", BaseURL: "http://oai", APIKey: "k", Priority: 0},
		{Name: "catch-all-anthropic", Type: "anthropic", BaseURL: "http://anthropic", APIKey: "k", Priority: 0},
	})

	got := registry.OrderedForModel("bilinmeyen-model")
	if len(got) != 2 {
		t.Fatalf("providers = %d, want 2", len(got))
	}
	if got[0].Name() != "catch-all-openai" {
		t.Fatalf("ilk provider = %q, want catch-all-openai", got[0].Name())
	}
	if got[1].Name() != "catch-all-anthropic" {
		t.Fatalf("ikinci provider = %q, want catch-all-anthropic", got[1].Name())
	}
}

func TestRegistryOrderedForModelTrimsWhitespace(t *testing.T) {
	registry := NewRegistry(http.DefaultClient, newTestLogger())
	registry.RebuildFromConfig([]config.Provider{
		{Name: "codex", Type: "codex", BaseURL: "http://codex", Priority: 0, OAuth: &config.OAuthConfig{RefreshToken: "r"}, Models: []string{"gpt-5.4", "gpt-5.4-*"}},
	})

	got := registry.OrderedForModel("  gpt-5.4  ")
	if len(got) != 1 {
		t.Fatalf("providers = %d, want 1", len(got))
	}
	if got[0].Name() != "codex" {
		t.Fatalf("ilk provider = %q, want codex", got[0].Name())
	}
}
