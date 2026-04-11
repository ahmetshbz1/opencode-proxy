package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"

	"opencode-proxy/internal/config"
)

type HealthHandler struct {
	manager *config.Manager
}

func NewHealthHandler(manager *config.Manager) *HealthHandler {
	return &HealthHandler{manager: manager}
}

func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	cfg := h.manager.Get()
	providers := make([]map[string]string, len(cfg.Providers))
	for i, p := range cfg.Providers {
		providers[i] = map[string]string{
			"name":     p.Name,
			"type":     p.Type,
			"priority": fmt.Sprintf("%d", p.Priority),
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":    "ok",
		"providers": providers,
	})
}
