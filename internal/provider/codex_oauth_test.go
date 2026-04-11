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
)

func TestCodexProviderRefreshesAccessTokenAndProxiesNonStream(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm hatası: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Fatalf("grant_type = %q, want refresh_token", got)
		}
		if got := r.Form.Get("refresh_token"); got != "refresh-token" {
			t.Fatalf("refresh_token = %q, want refresh-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh-access","refresh_token":"fresh-refresh","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	codexServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer fresh-access" {
			t.Fatalf("authorization = %q, want Bearer fresh-access", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll hatası: %v", err)
		}
		if !strings.Contains(string(body), `"model":"gpt-5-codex"`) {
			t.Fatalf("istek gövdesinde model yok: %s", string(body))
		}
		if !strings.Contains(string(body), `"instructions":""`) {
			t.Fatalf("istek gövdesinde boş instructions zorunlu: %s", string(body))
		}
		if !strings.Contains(string(body), `"stream":true`) {
			t.Fatalf("istek gövdesinde stream true zorunlu: %s", string(body))
		}
		if strings.Contains(string(body), `"max_output_tokens"`) {
			t.Fatalf("istek gövdesinde max_output_tokens olmamalı: %s", string(body))
		}
		if !strings.Contains(string(body), `"type":"message"`) {
			t.Fatalf("istek gövdesinde codex input message yok: %s", string(body))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"model\":\"gpt-5-codex\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"merhaba codex\"}]}],\"usage\":{\"input_tokens\":11,\"output_tokens\":7}}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer codexServer.Close()

	restoreTokenURL := codexTokenURL
	codexTokenURL = tokenServer.URL
	defer func() { codexTokenURL = restoreTokenURL }()

	var persisted config.OAuthConfig
	p := NewCodex(config.Provider{
		Name:    "codex",
		Type:    "codex",
		BaseURL: codexServer.URL,
		OAuth: &config.OAuthConfig{
			RefreshToken: "refresh-token",
			AccessToken:  "expired-access",
			ExpiresAt:    time.Now().Add(-time.Minute).Format(time.RFC3339),
		},
		PersistOAuth: func(oauth config.OAuthConfig) error {
			persisted = oauth
			return nil
		},
	}, http.DefaultClient, slog.New(slog.NewTextHandler(io.Discard, nil)))

	w := httptest.NewRecorder()
	err := p.Proxy(context.Background(), w, nil, anthropic.Request{
		Model:     "gpt-5-codex",
		MaxTokens: 512,
		Messages: []anthropic.Message{
			{Role: "user", Content: json.RawMessage(`"selam"`)}},
	})
	if err != nil {
		t.Fatalf("Proxy hatası: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `"text":"merhaba codex"`) {
		t.Fatalf("yanıt beklenen metni içermiyor: %s", w.Body.String())
	}
	if persisted.RefreshToken != "fresh-refresh" {
		t.Fatalf("persisted refresh token = %q, want fresh-refresh", persisted.RefreshToken)
	}
	if persisted.AccessToken != "fresh-access" {
		t.Fatalf("persisted access token = %q, want fresh-access", persisted.AccessToken)
	}
}

func TestCodexProviderStreamEmitsTextFromOutputItemDone(t *testing.T) {
	codexServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-1\",\"model\":\"gpt-5.4\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"selam kanka\"}]}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"model\":\"gpt-5.4\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer codexServer.Close()

	p := NewCodex(config.Provider{
		Name:     "codex",
		Type:     "codex",
		BaseURL:  codexServer.URL,
		Priority: 1,
		OAuth: &config.OAuthConfig{
			AccessToken: "access-token",
			ExpiresAt:   time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}, http.DefaultClient, slog.New(slog.NewTextHandler(io.Discard, nil)))

	w := httptest.NewRecorder()
	err := p.Proxy(context.Background(), w, nil, anthropic.Request{
		Model:     "gpt-5.4",
		Stream:    true,
		MaxTokens: 128,
		Messages: []anthropic.Message{
			{Role: "user", Content: json.RawMessage(`"merhaba"`)},
		},
	})
	if err != nil {
		t.Fatalf("Proxy hatası: %v", err)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"text_delta"`) || !strings.Contains(body, `"text":"selam kanka"`) {
		t.Fatalf("stream text delta yok: %s", body)
	}
	if !strings.Contains(body, `"message_stop"`) {
		t.Fatalf("message_stop yok: %s", body)
	}
}

func TestBuildCodexRequestSerializesToolArgumentsAsString(t *testing.T) {
	body, _, err := buildCodexRequest(anthropic.Request{
		Model:     "gpt-5.4",
		MaxTokens: 128,
		Messages: []anthropic.Message{
			{
				Role: "assistant",
				Content: json.RawMessage(`[
					{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/tmp/a.txt","limit":1}}
				]`),
			},
		},
	})
	if err != nil {
		t.Fatalf("buildCodexRequest hatası: %v", err)
	}

	raw := string(body)
	if !strings.Contains(raw, `"arguments":"{\"file_path\":\"/tmp/a.txt\",\"limit\":1}"`) {
		t.Fatalf("arguments string olarak serialize edilmedi: %s", raw)
	}
}

func TestBuildCodexRequestIncludesToolResultBlockContent(t *testing.T) {
	body, _, err := buildCodexRequest(anthropic.Request{
		Model: "gpt-5.4",
		Messages: []anthropic.Message{
			{
				Role: "assistant",
				Content: json.RawMessage(`[
					{"type":"tool_use","id":"call_123","name":"Read","input":{"file_path":"/tmp/a.txt"}}
				]`),
			},
			{
				Role: "user",
				Content: json.RawMessage(`[
					{"type":"tool_result","tool_use_id":"call_123","content":[{"type":"text","text":"dosya içeriği"}]}
				]`),
			},
		},
	})
	if err != nil {
		t.Fatalf("buildCodexRequest hatası: %v", err)
	}

	raw := string(body)
	if !strings.Contains(raw, `"type":"function_call_output"`) {
		t.Fatalf("function_call_output bekleniyordu: %s", raw)
	}
	if !strings.Contains(raw, `"call_id":"call_123"`) {
		t.Fatalf("call_id korunmadı: %s", raw)
	}
}

func TestCodexProviderStreamEmitsToolArgumentsOnlyOnce(t *testing.T) {
	codexServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-1\",\"model\":\"gpt-5.4\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call-1\",\"name\":\"Read\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.function_call_arguments.done\",\"arguments\":\"{\\\"file_path\\\":\\\"/tmp/a.txt\\\"}\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call-1\",\"name\":\"Read\",\"arguments\":\"{\\\"file_path\\\":\\\"/tmp/a.txt\\\"}\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"model\":\"gpt-5.4\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer codexServer.Close()

	p := NewCodex(config.Provider{
		Name:     "codex",
		Type:     "codex",
		BaseURL:  codexServer.URL,
		Priority: 1,
		OAuth: &config.OAuthConfig{
			AccessToken: "access-token",
			ExpiresAt:   time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}, http.DefaultClient, slog.New(slog.NewTextHandler(io.Discard, nil)))

	w := httptest.NewRecorder()
	err := p.Proxy(context.Background(), w, nil, anthropic.Request{
		Model:  "gpt-5.4",
		Stream: true,
		Messages: []anthropic.Message{
			{Role: "user", Content: json.RawMessage(`"merhaba"`)},
		},
	})
	if err != nil {
		t.Fatalf("Proxy hatası: %v", err)
	}
	body := w.Body.String()
	if strings.Count(body, `"partial_json":"{\"file_path\":\"/tmp/a.txt\"}"`) != 1 {
		t.Fatalf("tool arguments bir kez emit edilmeli: %s", body)
	}
}
