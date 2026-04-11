package proxy

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"opencode-proxy/internal/anthropic"
	"opencode-proxy/internal/middleware"
	"opencode-proxy/internal/provider"
	"opencode-proxy/internal/sse"
)

type Handler struct {
	registry *provider.Registry
	logger   *slog.Logger
}

func NewHandler(registry *provider.Registry, logger *slog.Logger) *Handler {
	return &Handler{registry: registry, logger: logger}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		sse.WriteError(w, http.StatusInternalServerError, "isteğin okunması başarısız: "+err.Error())
		return
	}

	var antReq anthropic.Request
	if err := json.Unmarshal(body, &antReq); err != nil {
		sse.WriteError(w, http.StatusBadRequest, "geçersiz istek: "+err.Error())
		return
	}

	reqID := middleware.GetRequestID(r.Context())
	providers := h.registry.OrderedForModel(antReq.Model)
	var lastErr error

	for _, p := range providers {
		h.logger.Debug("sağlayıcı deneniyor",
			slog.String("provider", p.Name()),
			slog.String("request_id", reqID),
		)

		if err := p.Proxy(r.Context(), w, body, antReq); err != nil {
			lastErr = err
			h.logger.Warn("sağlayıcı başarısız, sonrakine geçiliyor",
				slog.String("provider", p.Name()),
				slog.String("error", err.Error()),
				slog.String("request_id", reqID),
			)
			continue
		}
		return
	}

	if lastErr != nil {
		h.logger.Error("tüm sağlayıcılar başarısız",
			slog.String("last_error", lastErr.Error()),
			slog.String("request_id", reqID),
		)
		sse.WriteError(w, http.StatusBadGateway, "tüm sağlayıcılar başarısız oldu: "+lastErr.Error())
	} else {
		h.logger.Error("sağlayıcı yok",
			slog.String("request_id", reqID),
		)
		sse.WriteError(w, http.StatusBadGateway, "yapılandırılmış sağlayıcı yok")
	}
}
