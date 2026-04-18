package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Port      int        `json:"port"`
	Providers []Provider `json:"providers"`
}

type Provider struct {
	Name         string                  `json:"name"`
	Type         string                  `json:"type"`
	BaseURL      string                  `json:"base_url"`
	APIKey       string                  `json:"api_key"`
	OAuth        *OAuthConfig            `json:"oauth,omitempty"`
	Models       []string                `json:"models,omitempty"`
	Priority     int                     `json:"priority"`
	PersistOAuth func(OAuthConfig) error `json:"-"`
}

type OAuthConfig struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	Email        string `json:"email,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

func (c *Config) Validate() error {
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("geçersiz port: %d", c.Port)
	}
	if len(c.Providers) == 0 {
		return fmt.Errorf("en az bir sağlayıcı tanımlanmalı")
	}

	seenNames := make(map[string]struct{}, len(c.Providers))
	for i, p := range c.Providers {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			return fmt.Errorf("sağlayıcı %d: isim boş olamaz", i)
		}
		if _, exists := seenNames[name]; exists {
			return fmt.Errorf("sağlayıcı %d (%s): isim benzersiz olmalı", i, name)
		}
		seenNames[name] = struct{}{}
		if p.Type != "anthropic" && p.Type != "openai" && p.Type != "codex" && p.Type != "anthropic_passthrough" {
			return fmt.Errorf("sağlayıcı %d (%s): bilinmeyen tip %q", i, name, p.Type)
		}
		baseURL := strings.TrimSpace(p.BaseURL)
		if baseURL == "" {
			return fmt.Errorf("sağlayıcı %d (%s): base_url boş olamaz", i, name)
		}
		parsed, err := url.Parse(baseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("sağlayıcı %d (%s): base_url geçerli bir http/https adresi olmalı", i, name)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return fmt.Errorf("sağlayıcı %d (%s): base_url yalnız http/https olabilir", i, name)
		}
		if p.requiresAPIKey() && strings.TrimSpace(p.APIKey) == "" {
			return fmt.Errorf("sağlayıcı %d (%s): api_key boş olamaz", i, name)
		}
		if p.Type == "codex" && !p.hasCodexCredentials() {
			return fmt.Errorf("sağlayıcı %d (%s): codex için api_key veya oauth access/refresh token gerekli", i, name)
		}
	}
	return nil
}

func (p Provider) requiresAPIKey() bool {
	return p.Type == "anthropic" || p.Type == "openai"
}

func (p Provider) requiresIncomingAPIKey() bool {
	return p.Type == "anthropic_passthrough"
}

func (p Provider) hasCodexCredentials() bool {
	if p.APIKey != "" {
		return true
	}
	if p.OAuth == nil {
		return false
	}
	return p.OAuth.AccessToken != "" || p.OAuth.RefreshToken != ""
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config okunamadı: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config ayrıştırılamadı: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func LoadForAuth(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{
				Port:      8787,
				Providers: []Provider{},
			}, nil
		}
		return nil, fmt.Errorf("config okunamadı: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config ayrıştırılamadı: %w", err)
	}
	if cfg.Port == 0 {
		cfg.Port = 8787
	}
	if cfg.Providers == nil {
		cfg.Providers = []Provider{}
	}
	return &cfg, nil
}

func (c *Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("config yazılamadı: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("config dizini oluşturulamadı: %w", err)
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return fmt.Errorf("geçici config dosyası oluşturulamadı: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("geçici config dosyasına yazılamadı: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("geçici config dosyası kapatılamadı: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("config kaydedilemedi: %w", err)
	}
	return nil
}

func (c *Config) UpsertProvider(next Provider) {
	for i := range c.Providers {
		if c.Providers[i].Name == next.Name {
			c.Providers[i] = next
			return
		}
	}
	c.Providers = append(c.Providers, next)
}

type Manager struct {
	mu         sync.RWMutex
	config     *Config
	configPath string
	done       chan struct{}
	logger     *slog.Logger
	onChange   func(*Config)
}

func NewManager(path string, logger *slog.Logger) (*Manager, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		config:     cfg,
		configPath: path,
		done:       make(chan struct{}),
		logger:     logger,
	}
	return m, nil
}

func (m *Manager) Get() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

func (m *Manager) OnChange(fn func(*Config)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = fn
}

func (m *Manager) Watch() {
	var lastMod time.Time
	if info, err := os.Stat(m.configPath); err == nil {
		lastMod = info.ModTime()
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.done:
			return
		case <-ticker.C:
			info, err := os.Stat(m.configPath)
			if err != nil {
				continue
			}
			if info.ModTime().After(lastMod) {
				lastMod = info.ModTime()
				cfg, err := Load(m.configPath)
				if err != nil {
					m.logger.Error("config reload hatası", slog.String("error", err.Error()))
					continue
				}
				m.mu.Lock()
				m.config = cfg
				onChange := m.onChange
				m.mu.Unlock()
				m.logger.Info("config yeniden yüklendi")
				if onChange != nil {
					onChange(cfg)
				}
			}
		}
	}
}

func (m *Manager) Close() {
	close(m.done)
}

func (m *Manager) UpdateProviderOAuth(name string, oauth OAuthConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfgCopy := *m.config
	cfgCopy.Providers = append([]Provider(nil), m.config.Providers...)
	updated := false
	for i := range cfgCopy.Providers {
		if cfgCopy.Providers[i].Name != name {
			continue
		}
		oauthCopy := oauth
		cfgCopy.Providers[i].OAuth = &oauthCopy
		updated = true
		break
	}
	if !updated {
		return fmt.Errorf("sağlayıcı bulunamadı: %s", name)
	}
	if err := cfgCopy.Save(m.configPath); err != nil {
		return err
	}
	onChange := m.onChange
	m.config = &cfgCopy
	if onChange != nil {
		onChange(&cfgCopy)
	}
	return nil
}
