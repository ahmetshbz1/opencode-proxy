package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

type Config struct {
	Port      int        `json:"port"`
	Providers []Provider `json:"providers"`
}

type Provider struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Priority int    `json:"priority"`
}

func (c *Config) Validate() error {
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("geçersiz port: %d", c.Port)
	}
	if len(c.Providers) == 0 {
		return fmt.Errorf("en az bir sağlayıcı tanımlanmalı")
	}
	for i, p := range c.Providers {
		if p.Name == "" {
			return fmt.Errorf("sağlayıcı %d: isim boş olamaz", i)
		}
		if p.Type != "anthropic" && p.Type != "openai" {
			return fmt.Errorf("sağlayıcı %d (%s): bilinmeyen tip %q", i, p.Name, p.Type)
		}
		if p.BaseURL == "" {
			return fmt.Errorf("sağlayıcı %d (%s): base_url boş olamaz", i, p.Name)
		}
		if p.APIKey == "" {
			return fmt.Errorf("sağlayıcı %d (%s): api_key boş olamaz", i, p.Name)
		}
	}
	return nil
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
				m.mu.Unlock()
				m.logger.Info("config yeniden yüklendi")
				if m.onChange != nil {
					m.onChange(cfg)
				}
			}
		}
	}
}

func (m *Manager) Close() {
	close(m.done)
}
