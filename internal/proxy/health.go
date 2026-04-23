package proxy

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opencode-proxy/internal/config"
	"opencode-proxy/internal/provider"
)

type HealthHandler struct {
	manager  *config.Manager
	registry *provider.Registry
	assets   http.Handler
}

type healthProvider struct {
	Name                   string                  `json:"name"`
	Type                   string                  `json:"type"`
	Priority               int                     `json:"priority"`
	Models                 []string                `json:"models,omitempty"`
	APIKeyConfigured       bool                    `json:"api_key_configured"`
	OAuthConfigured        bool                    `json:"oauth_configured"`
	IncomingAPIKeyRequired bool                    `json:"incoming_api_key_required"`
	Exhausted              bool                    `json:"exhausted"`
	ExhaustedUntil         string                  `json:"exhausted_until,omitempty"`
	ResetInSeconds         int64                   `json:"reset_in_seconds,omitempty"`
	Usage                  *provider.UsageSnapshot `json:"usage,omitempty"`
	UsageError             string                  `json:"usage_error,omitempty"`
}

type healthResponse struct {
	Status        string           `json:"status"`
	Port          int              `json:"port"`
	ProviderCount int              `json:"provider_count"`
	GeneratedAt   string           `json:"generated_at"`
	Providers     []healthProvider `json:"providers"`
}

//go:embed health_assets/* health_assets/assets/*
var healthAssets embed.FS

func NewHealthHandler(manager *config.Manager, registry *provider.Registry) *HealthHandler {
	assets, err := fs.Sub(healthAssets, "health_assets")
	if err != nil {
		panic(err)
	}
	return &HealthHandler{
		manager:  manager,
		registry: registry,
		assets:   http.FileServer(http.FS(assets)),
	}
}

func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/health/assets/") || r.URL.Path == "/health/favicon.svg" || r.URL.Path == "/health/icons.svg" {
		h.serveAsset(w, r)
		return
	}
	if r.URL.Path == "/health.json" || wantsJSON(r) {
		h.serveJSON(w)
		return
	}
	h.serveIndex(w, r)
}

func (h *HealthHandler) serveJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.response())
}

func (h *HealthHandler) serveIndex(w http.ResponseWriter, _ *http.Request) {
	data, err := healthAssets.ReadFile("health_assets/index.html")
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (h *HealthHandler) serveAsset(w http.ResponseWriter, r *http.Request) {
	r = r.Clone(r.Context())
	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/health")
	h.assets.ServeHTTP(w, r)
}

func (h *HealthHandler) response() healthResponse {
	cfg := h.manager.Get()
	now := time.Now()
	providers := make([]healthProvider, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		hp := healthProvider{
			Name:                   p.Name,
			Type:                   p.Type,
			Priority:               p.Priority,
			Models:                 p.Models,
			APIKeyConfigured:       p.APIKey != "",
			OAuthConfigured:        p.OAuth != nil && (p.OAuth.AccessToken != "" || p.OAuth.RefreshToken != ""),
			IncomingAPIKeyRequired: p.Type == "anthropic_passthrough",
		}
		if until, exhausted := h.registry.Active().ExhaustedUntil(p.Name, now); exhausted {
			hp.Exhausted = true
			hp.ExhaustedUntil = until.Format(time.RFC3339)
			hp.ResetInSeconds = int64(time.Until(until).Seconds())
		}
		usageCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		usage, err := h.registry.Usage(usageCtx, p.Name)
		cancel()
		if err != nil {
			hp.UsageError = err.Error()
		} else if usage != nil {
			hp.Usage = usage
			if h.registry.Active().ApplyUsageLimit(p.Name, usage, now) {
				if until, exhausted := h.registry.Active().ExhaustedUntil(p.Name, now); exhausted {
					hp.Exhausted = true
					hp.ExhaustedUntil = until.Format(time.RFC3339)
					hp.ResetInSeconds = int64(time.Until(until).Seconds())
				}
			}
		}
		providers = append(providers, hp)
	}
	return healthResponse{
		Status:        "ok",
		Port:          cfg.Port,
		ProviderCount: len(cfg.Providers),
		GeneratedAt:   now.Format(time.RFC3339),
		Providers:     providers,
	}
}

func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}

func formatSeconds(seconds int64) string {
	if seconds <= 0 {
		return "0s"
	}
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	remainingSeconds := seconds % 60
	if hours > 0 {
		return strconv.FormatInt(hours, 10) + "s " + strconv.FormatInt(minutes, 10) + "dk"
	}
	if minutes > 0 {
		return strconv.FormatInt(minutes, 10) + "dk " + strconv.FormatInt(remainingSeconds, 10) + "sn"
	}
	return strconv.FormatInt(remainingSeconds, 10) + "sn"
}
