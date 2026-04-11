package proxy

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"opencode-proxy/internal/anthropic"
	"opencode-proxy/internal/middleware"
	"opencode-proxy/internal/provider"
	"opencode-proxy/internal/sse"
)

const (
	maxRetries     = 10
	retryBaseDelay = 3 * time.Second
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
	isTokenCount := strings.HasSuffix(r.URL.Path, "/count_tokens")
	active := h.registry.Active()

	// Token sayma — sadece aktif provider, retry yok, failover yok
	if isTokenCount {
		p := h.findActiveProvider(antReq.Model, active)
		if p == nil {
			sse.WriteError(w, http.StatusBadGateway, "aktif sağlayıcı yok (token sayma)")
			return
		}
		if err := p.Proxy(r.Context(), w, body, antReq); err != nil {
			h.logger.Warn("token sayma başarısız",
				slog.String("provider", p.Name()),
				slog.String("error", err.Error()),
				slog.String("request_id", reqID),
			)
			sse.WriteError(w, http.StatusBadGateway, "token sayma başarısız: "+err.Error())
			return
		}
		return
	}

	// Normal istek — son başarılı provider'ı öne al, retry + kota yönetimi
	providers := h.prioritizeProviders(antReq.Model, active)
	var lastErr error

	for _, p := range providers {
		if active.IsExhausted(p.Name()) {
			h.logger.Info("sağlayıcı limiti dolmuş, atlanıyor",
				slog.String("provider", p.Name()),
				slog.String("request_id", reqID),
			)
			continue
		}

		// Geçici hatalarda aynı provider'da retry
		success := false
		for attempt := 0; attempt <= maxRetries; attempt++ {
			if attempt > 0 {
				delay := retryBaseDelay * time.Duration(attempt)
				h.logger.Info("provider tekrar deneniyor",
					slog.String("provider", p.Name()),
					slog.Int("deneme", attempt+1),
					slog.Duration("bekleme", delay),
					slog.String("request_id", reqID),
				)
				time.Sleep(delay)
			}

			err := p.Proxy(r.Context(), w, body, antReq)
			if err == nil {
				// Başarılı — provider'ı aktif olarak kaydet
				active.ClearExhausted(p.Name())
				active.SetLastActive(antReq.Model, p.Name())
				success = true
				return
			}

			lastErr = err

			// Kota hatası → provider'ı tükenmiş olarak işaretle, retry yapma
			if provider.IsQuotaError(err) {
				h.logger.Warn("sağlayıcı limiti doldu, sonrakine geçiliyor",
					slog.String("provider", p.Name()),
					slog.String("error", err.Error()),
					slog.String("request_id", reqID),
				)
				active.MarkExhausted(p.Name())
				break // retry döngüsünden çık, sıradaki provider'a geç
			}

			// Geçici hata — retry devam etsin
			h.logger.Warn("sağlayıcı geçici hata",
				slog.String("provider", p.Name()),
				slog.String("error", err.Error()),
				slog.Int("deneme", attempt+1),
				slog.String("request_id", reqID),
			)
		}

		if !success && !provider.IsQuotaError(lastErr) {
			// Retry'lar tükendi ama kota hatası değil — sonrakine geç
			h.logger.Warn("sağlayıcı retry'lar sonrası başarısız",
				slog.String("provider", p.Name()),
				slog.String("request_id", reqID),
			)
		}
	}

	// Tüm provider'lar exhausted ise, resetleyip tekrar dene
	if lastErr == nil && len(providers) > 0 {
		h.logger.Warn("tüm sağlayıcılar limit dolmuş, sıfırlanıyor ve tekrar deneniyor",
			slog.String("request_id", reqID),
		)
		active.ResetAll()
		for _, p := range providers {
			err := p.Proxy(r.Context(), w, body, antReq)
			if err == nil {
				active.SetLastActive(antReq.Model, p.Name())
				return
			}
			lastErr = err
			h.logger.Warn("sıfırlama sonrası sağlayıcı başarısız",
				slog.String("provider", p.Name()),
				slog.String("error", err.Error()),
				slog.String("request_id", reqID),
			)
		}
	}

	if lastErr != nil {
		h.logger.Error("tüm sağlayıcılar başarısız",
			slog.String("last_error", lastErr.Error()),
			slog.String("request_id", reqID),
		)
		sse.WriteError(w, http.StatusBadGateway, "tüm sağlayıcılar başarısız oldu: "+lastErr.Error())
	} else {
		h.logger.Error("sağlayıcı yok",
			slog.String("model", antReq.Model),
			slog.String("request_id", reqID),
		)
		sse.WriteError(w, http.StatusBadGateway, "yapılandırılmış sağlayıcı yok (model: "+antReq.Model+")")
	}
}

// findActiveProvider, limiti dolmamış ilk provider'ı bulur (son başarılıyı önceliklendirir).
func (h *Handler) findActiveProvider(model string, active *provider.ActiveTracker) provider.Provider {
	for _, p := range h.prioritizeProviders(model, active) {
		return p
	}
	return nil
}

// prioritizeProviders, son başarılı provider'ı listenin başına taşır.
func (h *Handler) prioritizeProviders(model string, active *provider.ActiveTracker) []provider.Provider {
	providers := h.registry.OrderedForModel(model)
	last := active.LastActiveForModel(model)
	if last == "" || len(providers) <= 1 {
		return providers
	}

	// Son başarılı provider'ı bul ve öne taşı
	for i, p := range providers {
		if p.Name() == last {
			if i == 0 {
				return providers // zaten başta
			}
			reordered := make([]provider.Provider, 0, len(providers))
			reordered = append(reordered, p)
			reordered = append(reordered, providers[:i]...)
			reordered = append(reordered, providers[i+1:]...)
			return reordered
		}
	}
	return providers
}
