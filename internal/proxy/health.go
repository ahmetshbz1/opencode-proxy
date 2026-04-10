package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"

	"opencode-proxy/internal/config"
)

func HealthCheck(w http.ResponseWriter, r *http.Request) {
	cfg := config.Get()
	providers := make([]map[string]string, len(cfg.Providers))
	for i, p := range cfg.Providers {
		providers[i] = map[string]string{
			"name":     p.Name,
			"type":     p.Type,
			"priority": fmt.Sprintf("%d", p.Priority),
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"providers": providers,
	})
}
