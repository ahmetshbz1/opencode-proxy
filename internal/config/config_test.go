package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	cfg := &Config{
		Port: 8787,
		Providers: []Provider{
			{Name: "test", Type: "anthropic", BaseURL: "https://api.test.com", APIKey: "sk-test", Priority: 1},
		},
	}
	data, _ := json.Marshal(cfg)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, data, 0644)

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load hatası: %v", err)
	}
	if loaded.Port != 8787 {
		t.Errorf("port = %d, want 8787", loaded.Port)
	}
	if len(loaded.Providers) != 1 {
		t.Fatalf("providers count = %d, want 1", len(loaded.Providers))
	}
	if loaded.Providers[0].Name != "test" {
		t.Errorf("provider name = %q, want %q", loaded.Providers[0].Name, "test")
	}
}

func TestLoadNotFound(t *testing.T) {
	_, err := Load("/nonexistent/config.json")
	if err == nil {
		t.Fatal("beklenen hata yok")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte("{invalid"), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("beklenen hata yok")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name:    "geçerli config",
			modify:  func(_ *Config) {},
			wantErr: false,
		},
		{
			name:    "port sıfır",
			modify:  func(c *Config) { c.Port = 0 },
			wantErr: true,
		},
		{
			name:    "port çok büyük",
			modify:  func(c *Config) { c.Port = 70000 },
			wantErr: true,
		},
		{
			name:    "sağlayıcı yok",
			modify:  func(c *Config) { c.Providers = nil },
			wantErr: true,
		},
		{
			name:    "bilinmeyen tip",
			modify:  func(c *Config) { c.Providers[0].Type = "google" },
			wantErr: true,
		},
		{
			name: "codex oauth refresh token ile geçerli",
			modify: func(c *Config) {
				c.Providers[0] = Provider{
					Name:     "codex",
					Type:     "codex",
					BaseURL:  "https://chatgpt.com/backend-api/codex",
					Priority: 1,
					OAuth: &OAuthConfig{
						RefreshToken: "refresh-token",
					},
				}
			},
			wantErr: false,
		},
		{
			name:    "boş isim",
			modify:  func(c *Config) { c.Providers[0].Name = "" },
			wantErr: true,
		},
		{
			name:    "boş base_url",
			modify:  func(c *Config) { c.Providers[0].BaseURL = "" },
			wantErr: true,
		},
		{
			name:    "boş api_key",
			modify:  func(c *Config) { c.Providers[0].APIKey = "" },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Port: 8787,
				Providers: []Provider{
					{Name: "test", Type: "anthropic", BaseURL: "https://api.test.com", APIKey: "sk-test", Priority: 1},
				},
			}
			tt.modify(cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestManagerGet(t *testing.T) {
	cfg := &Config{
		Port: 8787,
		Providers: []Provider{
			{Name: "test", Type: "anthropic", BaseURL: "https://api.test.com", APIKey: "sk-test", Priority: 1},
		},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data, _ := json.Marshal(cfg)
	os.WriteFile(path, data, 0644)

	mgr, err := NewManager(path, nil)
	if err != nil {
		t.Fatalf("NewManager hatası: %v", err)
	}
	defer mgr.Close()

	got := mgr.Get()
	if got.Port != 8787 {
		t.Errorf("Get().Port = %d, want 8787", got.Port)
	}
}

func TestUpsertProviderReplacesByName(t *testing.T) {
	cfg := &Config{
		Port: 8787,
		Providers: []Provider{
			{Name: "codex-oauth", Type: "codex", BaseURL: "https://old.example.com", OAuth: &OAuthConfig{RefreshToken: "old"}, Priority: 2},
		},
	}

	cfg.UpsertProvider(Provider{
		Name:     "codex-oauth",
		Type:     "codex",
		BaseURL:  "https://new.example.com",
		OAuth:    &OAuthConfig{RefreshToken: "new"},
		Priority: 2,
	})

	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(cfg.Providers))
	}
	if cfg.Providers[0].BaseURL != "https://new.example.com" {
		t.Fatalf("base_url = %q, want https://new.example.com", cfg.Providers[0].BaseURL)
	}
	if cfg.Providers[0].OAuth == nil || cfg.Providers[0].OAuth.RefreshToken != "new" {
		t.Fatalf("oauth.refresh_token = %v, want new", cfg.Providers[0].OAuth)
	}
}

func TestManagerUpdateProviderOAuthPersistsTokens(t *testing.T) {
	cfg := &Config{
		Port: 8787,
		Providers: []Provider{
			{Name: "codex-oauth", Type: "codex", BaseURL: "https://chatgpt.com/backend-api/codex", Priority: 1, OAuth: &OAuthConfig{RefreshToken: "old"}},
		},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data, _ := json.Marshal(cfg)
	os.WriteFile(path, data, 0644)

	mgr, err := NewManager(path, nil)
	if err != nil {
		t.Fatalf("NewManager hatası: %v", err)
	}
	defer mgr.Close()

	err = mgr.UpdateProviderOAuth("codex-oauth", OAuthConfig{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		ExpiresAt:    "2026-04-11T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("UpdateProviderOAuth hatası: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load hatası: %v", err)
	}
	got := loaded.Providers[0].OAuth
	if got == nil || got.RefreshToken != "new-refresh" || got.AccessToken != "new-access" {
		t.Fatalf("persisted oauth = %+v", got)
	}
}
