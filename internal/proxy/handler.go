package proxy

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

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

	// API anahtarını istek başlıklarından al ve passthrough provider'lar için context'e ekle.
	apiKey := r.Header.Get("x-api-key")
	if apiKey == "" {
		apiKey = r.Header.Get("X-Api-Key")
	}
	if apiKey == "" {
		apiKey = r.Header.Get("Authorization")
		// Varsa "Bearer " önekini kaldır.
		if len(apiKey) > 7 && strings.HasPrefix(strings.ToLower(apiKey), "bearer ") {
			apiKey = apiKey[7:]
		}
	}
	ctx := provider.WithAPIKey(r.Context(), apiKey)

	reqID := middleware.GetRequestID(ctx)
	isTokenCount := strings.HasSuffix(r.URL.Path, "/count_tokens")
	active := h.registry.Active()

	// Token sayma — sadece ilk uygun provider, retry yok
	if isTokenCount {
		providers := h.registry.OrderedForModel(antReq.Model)
		for _, p := range providers {
			if active.IsExhausted(p.Name()) {
				continue
			}
			if info := middleware.GetRequestInfo(ctx); info != nil {
				info.Set(p.Name(), h.registry.TypeFor(p.Name()))
			}
			if err := p.Proxy(ctx, w, body, antReq); err != nil {
				h.logger.Warn("token sayma başarısız",
					slog.String("provider", p.Name()),
					slog.String("error", err.Error()),
					slog.String("request_id", reqID),
				)
			}
			return
		}
		sse.WriteError(w, http.StatusBadGateway, "aktif sağlayıcı yok (token sayma)")
		return
	}

	// Normal istek — round-robin ile provider seçimi, rate limit = hemen sonrakine geç
	providers := h.registry.OrderedForModel(antReq.Model)
	if len(providers) == 0 {
		sse.WriteError(w, http.StatusBadGateway, "yapılandırılmış sağlayıcı yok (model: "+antReq.Model+")")
		return
	}

	// Round-robin: her istekte farklı provider'dan başla
	startIdx := active.NextRRIndex(antReq.Model, len(providers))

	var lastErr error
	for i := 0; i < len(providers); i++ {
		idx := (startIdx + i) % len(providers)
		p := providers[idx]

		if active.IsExhausted(p.Name()) {
			h.logger.Debug("sağlayıcı limiti dolmuş, atlanıyor",
				slog.String("provider", p.Name()),
				slog.String("request_id", reqID),
			)
			continue
		}

		if info := middleware.GetRequestInfo(ctx); info != nil {
			info.Set(p.Name(), h.registry.TypeFor(p.Name()))
		}

		err := p.Proxy(ctx, w, body, antReq)
		if err == nil {
			// Başarılı — provider'ın exhausted işaretini temizle
			active.ClearExhausted(p.Name())
			return
		}

		lastErr = err

		// Rate limit / kota hatası → provider'ı cooldown'a al, hemen sonrakine geç
		if provider.IsQuotaError(err) {
			h.logger.Warn("sağlayıcı limiti doldu, sonrakine geçiliyor",
				slog.String("provider", p.Name()),
				slog.String("error", err.Error()),
				slog.String("request_id", reqID),
			)
			active.MarkExhausted(p.Name())
			continue
		}

		// Geçici hata — retry yok, hemen sonrakine geç
		h.logger.Warn("sağlayıcı hata, sonrakine geçiliyor",
			slog.String("provider", p.Name()),
			slog.String("error", err.Error()),
			slog.String("request_id", reqID),
		)
	}

	// Tüm provider'lar exhausted ise — cooldown'u sıfırlayıp bir daha dene
	h.logger.Warn("tüm sağlayıcılar limit dolmuş, sıfırlanıyor ve tekrar deneniyor",
		slog.String("request_id", reqID),
	)
	active.ResetAll()

	for _, p := range providers {
		if info := middleware.GetRequestInfo(ctx); info != nil {
			info.Set(p.Name(), h.registry.TypeFor(p.Name()))
		}
		err := p.Proxy(ctx, w, body, antReq)
		if err == nil {
			return
		}
		lastErr = err
		h.logger.Warn("sıfırlama sonrası sağlayıcı başarısız",
			slog.String("provider", p.Name()),
			slog.String("error", err.Error()),
			slog.String("request_id", reqID),
		)
	}

	if lastErr != nil {
		sse.WriteError(w, http.StatusBadGateway, "tüm sağlayıcılar başarısız oldu: "+lastErr.Error())
	} else {
		sse.WriteError(w, http.StatusBadGateway, "yapılandırılmış sağlayıcı yok (model: "+antReq.Model+")")
	}
}
