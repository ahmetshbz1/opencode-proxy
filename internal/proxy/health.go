package proxy

import (
	"encoding/json"
	"net/http"

	"opencode-proxy/internal/config"
	"opencode-proxy/internal/provider"
)

type HealthHandler struct {
	manager  *config.Manager
	registry *provider.Registry
}

type healthProvider struct {
	Name                   string   `json:"name"`
	Type                   string   `json:"type"`
	Priority               int      `json:"priority"`
	Models                 []string `json:"models,omitempty"`
	APIKeyConfigured       bool     `json:"api_key_configured"`
	OAuthConfigured        bool     `json:"oauth_configured"`
	IncomingAPIKeyRequired bool     `json:"incoming_api_key_required"`
	Exhausted              bool     `json:"exhausted"`
}

type healthResponse struct {
	Status        string           `json:"status"`
	Port          int              `json:"port"`
	ProviderCount int              `json:"provider_count"`
	Providers     []healthProvider `json:"providers"`
}

func NewHealthHandler(manager *config.Manager, registry *provider.Registry) *HealthHandler {
	return &HealthHandler{manager: manager, registry: registry}
}

func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	cfg := h.manager.Get()
	providers := make([]healthProvider, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		providers = append(providers, healthProvider{
			Name:                   p.Name,
			Type:                   p.Type,
			Priority:               p.Priority,
			Models:                 p.Models,
			APIKeyConfigured:       p.APIKey != "",
			OAuthConfigured:        p.OAuth != nil && (p.OAuth.AccessToken != "" || p.OAuth.RefreshToken != ""),
			IncomingAPIKeyRequired: p.Type == "anthropic_passthrough",
			Exhausted:              h.registry.Active().IsExhausted(p.Name),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthResponse{
		Status:        "ok",
		Port:          cfg.Port,
		ProviderCount: len(cfg.Providers),
		Providers:     providers,
	})
}
