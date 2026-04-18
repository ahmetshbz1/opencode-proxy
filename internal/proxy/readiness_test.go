package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"opencode-proxy/internal/config"
	"opencode-proxy/internal/provider"
)

func TestReadinessHandlerReadyWhenProvidersExist(t *testing.T) {
	cfg := &config.Config{
		Port: 8787,
		Providers: []config.Provider{
			{Name: "z.ai", Type: "anthropic", BaseURL: "https://api.z.ai/api/anthropic", APIKey: "key", Models: []string{"glm-*"}},
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

	h := NewReadinessHandler(mgr, registry)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestReadinessHandlerFailsWithoutProviders(t *testing.T) {
	cfg := &config.Config{Port: 8787, Providers: []config.Provider{{Name: "z.ai", Type: "anthropic", BaseURL: "https://api.z.ai/api/anthropic", APIKey: "key", Models: []string{"glm-*"}}}}
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

	h := NewReadinessHandler(mgr, registry)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}
