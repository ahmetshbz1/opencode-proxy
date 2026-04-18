package proxy

import (
	"encoding/json"
	"net/http"

	"opencode-proxy/internal/config"
	"opencode-proxy/internal/provider"
)

type ReadinessHandler struct {
	manager  *config.Manager
	registry *provider.Registry
}

type readinessResponse struct {
	Status        string `json:"status"`
	Ready         bool   `json:"ready"`
	Reason        string `json:"reason,omitempty"`
	ProviderCount int    `json:"provider_count"`
}

func NewReadinessHandler(manager *config.Manager, registry *provider.Registry) *ReadinessHandler {
	return &ReadinessHandler{manager: manager, registry: registry}
}

func (h *ReadinessHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	cfg := h.manager.Get()
	resp := readinessResponse{
		Status:        "ok",
		Ready:         true,
		ProviderCount: len(cfg.Providers),
	}

	w.Header().Set("Content-Type", "application/json")
	if len(cfg.Providers) == 0 || len(h.registry.Ordered()) == 0 {
		resp.Status = "degraded"
		resp.Ready = false
		resp.Reason = "aktif sağlayıcı yok"
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	_ = json.NewEncoder(w).Encode(resp)
}
