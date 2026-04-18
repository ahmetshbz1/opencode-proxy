package proxy

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"opencode-proxy/internal/config"
	"opencode-proxy/internal/provider"
)

func TestHealthHandlerReturnsOperationalMetadata(t *testing.T) {
	cfg := &config.Config{
		Port: 8787,
		Providers: []config.Provider{
			{
				Name:     "claude-native",
				Type:     "anthropic_passthrough",
				BaseURL:  "https://api.anthropic.com",
				Models:   []string{"claude-opus-*"},
				Priority: 0,
			},
			{
				Name:     "codex-oauth",
				Type:     "codex",
				BaseURL:  "https://chatgpt.com/backend-api/codex",
				Priority: 1,
				Models:   []string{"gpt-5.4", "gpt-5.4-*"},
				OAuth: &config.OAuthConfig{
					RefreshToken: "refresh-token",
				},
			},
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("config kaydedilemedi: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := config.NewManager(path, logger)
	if err != nil {
		t.Fatalf("manager oluşturulamadı: %v", err)
	}
	defer mgr.Close()

	registry := provider.NewRegistry(http.DefaultClient, logger)
	registry.RebuildFromConfig(cfg.Providers)
	registry.Active().MarkExhausted("codex-oauth")

	h := NewHealthHandler(mgr, registry)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		Status        string `json:"status"`
		Port          int    `json:"port"`
		ProviderCount int    `json:"provider_count"`
		Providers     []struct {
			Name                   string   `json:"name"`
			Type                   string   `json:"type"`
			Priority               int      `json:"priority"`
			Models                 []string `json:"models"`
			APIKeyConfigured       bool     `json:"api_key_configured"`
			OAuthConfigured        bool     `json:"oauth_configured"`
			IncomingAPIKeyRequired bool     `json:"incoming_api_key_required"`
			Exhausted              bool     `json:"exhausted"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("health yanıtı ayrıştırılamadı: %v", err)
	}

	if resp.Status != "ok" {
		t.Fatalf("status = %q, want ok", resp.Status)
	}
	if resp.Port != 8787 {
		t.Fatalf("port = %d, want 8787", resp.Port)
	}
	if resp.ProviderCount != 2 {
		t.Fatalf("provider_count = %d, want 2", resp.ProviderCount)
	}
	if len(resp.Providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(resp.Providers))
	}

	if !resp.Providers[0].IncomingAPIKeyRequired {
		t.Fatal("anthropic_passthrough provider için incoming_api_key_required true olmalı")
	}
	if resp.Providers[0].APIKeyConfigured {
		t.Fatal("anthropic_passthrough provider için api_key_configured false olmalı")
	}
	if !resp.Providers[1].OAuthConfigured {
		t.Fatal("codex provider için oauth_configured true olmalı")
	}
	if !resp.Providers[1].Exhausted {
		t.Fatal("işaretlenen provider exhausted görünmeli")
	}
}
