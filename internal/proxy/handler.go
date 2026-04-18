package proxy

import (
	"context"
	"encoding/json"
	"errors"
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

type requestContext struct {
	body         []byte
	request      anthropic.Request
	apiKey       string
	isTokenCount bool
}

func NewHandler(registry *provider.Registry, logger *slog.Logger) *Handler {
	return &Handler{registry: registry, logger: logger}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reqCtx, err := h.parseRequest(r)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, io.ErrUnexpectedEOF) {
			status = http.StatusBadRequest
		}
		if strings.HasPrefix(err.Error(), "isteğin okunması başarısız") {
			status = http.StatusInternalServerError
		}
		sse.WriteError(w, status, err.Error())
		return
	}

	ctx := provider.WithAPIKey(r.Context(), reqCtx.apiKey)
	if reqCtx.isTokenCount {
		h.handleTokenCount(ctx, w, reqCtx)
		return
	}
	h.handleMessage(ctx, w, reqCtx)
}

func (h *Handler) parseRequest(r *http.Request) (*requestContext, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, errors.New("isteğin okunması başarısız: " + err.Error())
	}

	var antReq anthropic.Request
	if err := json.Unmarshal(body, &antReq); err != nil {
		return nil, errors.New("geçersiz istek: " + err.Error())
	}
	if err := validateRequest(antReq, strings.HasSuffix(r.URL.Path, "/count_tokens")); err != nil {
		return nil, err
	}

	return &requestContext{
		body:         body,
		request:      antReq,
		apiKey:       extractAPIKey(r),
		isTokenCount: strings.HasSuffix(r.URL.Path, "/count_tokens"),
	}, nil
}

func (h *Handler) handleTokenCount(ctx context.Context, w http.ResponseWriter, reqCtx *requestContext) {
	providers := h.registry.OrderedForModel(reqCtx.request.Model)
	active := h.registry.Active()
	reqID := middleware.GetRequestID(ctx)
	for _, p := range providers {
		if active.IsExhausted(p.Name()) {
			continue
		}
		h.attachRequestInfo(ctx, p.Name())
		if err := p.Proxy(ctx, w, reqCtx.body, reqCtx.request); err != nil {
			h.logger.Warn("token sayma başarısız",
				slog.String("provider", p.Name()),
				slog.String("error", err.Error()),
				slog.String("request_id", reqID),
			)
		}
		return
	}
	sse.WriteError(w, http.StatusBadGateway, "aktif sağlayıcı yok (token sayma)")
}

func (h *Handler) handleMessage(ctx context.Context, w http.ResponseWriter, reqCtx *requestContext) {
	providers := h.registry.OrderedForModel(reqCtx.request.Model)
	if len(providers) == 0 {
		sse.WriteError(w, http.StatusBadGateway, "yapılandırılmış sağlayıcı yok (model: "+reqCtx.request.Model+")")
		return
	}
	startIdx := h.registry.Active().NextRRIndex(reqCtx.request.Model, len(providers))
	lastErr := h.tryProviders(ctx, w, providers, reqCtx, startIdx)
	if lastErr == nil {
		return
	}
	h.logger.Warn("tüm sağlayıcılar limit dolmuş, sıfırlanıyor ve tekrar deneniyor",
		slog.String("request_id", middleware.GetRequestID(ctx)),
	)
	h.registry.Active().ResetAll()
	lastErr = h.tryProviders(ctx, w, providers, reqCtx, 0)
	if lastErr != nil {
		sse.WriteError(w, http.StatusBadGateway, "tüm sağlayıcılar başarısız oldu: "+lastErr.Error())
		return
	}
	sse.WriteError(w, http.StatusBadGateway, "yapılandırılmış sağlayıcı yok (model: "+reqCtx.request.Model+")")
}

func (h *Handler) tryProviders(ctx context.Context, w http.ResponseWriter, providers []provider.Provider, reqCtx *requestContext, startIdx int) error {
	active := h.registry.Active()
	reqID := middleware.GetRequestID(ctx)
	var lastErr error
	for i := range len(providers) {
		idx := (startIdx + i) % len(providers)
		p := providers[idx]
		if active.IsExhausted(p.Name()) {
			h.logger.Debug("sağlayıcı limiti dolmuş, atlanıyor",
				slog.String("provider", p.Name()),
				slog.String("request_id", reqID),
			)
			continue
		}
		h.attachRequestInfo(ctx, p.Name())
		err := p.Proxy(ctx, w, reqCtx.body, reqCtx.request)
		if err == nil {
			active.ClearExhausted(p.Name())
			return nil
		}
		lastErr = err
		if provider.IsQuotaError(err) {
			h.logger.Warn("sağlayıcı limiti doldu, sonrakine geçiliyor",
				slog.String("provider", p.Name()),
				slog.String("error", err.Error()),
				slog.String("request_id", reqID),
			)
			active.MarkExhausted(p.Name())
			continue
		}
		h.logger.Warn("sağlayıcı hata, sonrakine geçiliyor",
			slog.String("provider", p.Name()),
			slog.String("error", err.Error()),
			slog.String("request_id", reqID),
		)
	}
	return lastErr
}

func (h *Handler) attachRequestInfo(ctx context.Context, providerName string) {
	if info := middleware.GetRequestInfo(ctx); info != nil {
		info.Set(providerName, h.registry.TypeFor(providerName))
	}
}

func extractAPIKey(r *http.Request) string {
	apiKey := r.Header.Get("x-api-key")
	if apiKey == "" {
		apiKey = r.Header.Get("X-Api-Key")
	}
	if apiKey == "" {
		apiKey = r.Header.Get("Authorization")
		if len(apiKey) > 7 && strings.HasPrefix(strings.ToLower(apiKey), "bearer ") {
			apiKey = apiKey[7:]
		}
	}
	return apiKey
}

func validateRequest(req anthropic.Request, isTokenCount bool) error {
	if strings.TrimSpace(req.Model) == "" {
		return errors.New("geçersiz istek: model boş olamaz")
	}
	if len(req.Messages) == 0 {
		return errors.New("geçersiz istek: messages boş olamaz")
	}
	if !isTokenCount && req.MaxTokens <= 0 {
		return errors.New("geçersiz istek: max_tokens sıfırdan büyük olmalı")
	}
	return nil
}
