package proxy

import (
	"context"
	"encoding/json"
	"errors"
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

type Handler struct {
	registry *provider.Registry
	logger   *slog.Logger
}

const providerRetryAttempts = 3

type requestContext struct {
	body         []byte
	request      anthropic.Request
	apiKey       string
	isTokenCount bool
}

type providerAttemptResult struct {
	err     error
	limited bool
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
	result := h.tryProviders(ctx, w, providers, reqCtx)
	if result.err == nil {
		return
	}
	if provider.IsCanceledError(result.err) {
		return
	}
	if result.limited {
		sse.WriteError(w, http.StatusTooManyRequests, "sağlayıcı limiti doldu: "+result.err.Error())
		return
	}
	if !h.hasAvailableProvider(providers) {
		sse.WriteError(w, http.StatusBadGateway, "aktif sağlayıcı yok (model: "+reqCtx.request.Model+")")
		return
	}
	sse.WriteError(w, http.StatusBadGateway, "tüm sağlayıcılar başarısız oldu: "+result.err.Error())
}

func (h *Handler) hasAvailableProvider(providers []provider.Provider) bool {
	active := h.registry.Active()
	for _, p := range providers {
		if !active.IsExhausted(p.Name()) {
			return true
		}
	}
	return false
}

func (h *Handler) stickyOrder(model string, providers []provider.Provider) []provider.Provider {
	current := h.registry.Active().Current(model)
	if current == "" {
		return providers
	}
	ordered := make([]provider.Provider, 0, len(providers))
	found := false
	for _, p := range providers {
		if p.Name() == current {
			ordered = append(ordered, p)
			found = true
			break
		}
	}
	if found {
		for _, p := range providers {
			if p.Name() != current {
				ordered = append(ordered, p)
			}
		}
		return ordered
	}
	h.registry.Active().ClearCurrent(model, current)
	return providers
}

func (h *Handler) tryProviders(ctx context.Context, w http.ResponseWriter, providers []provider.Provider, reqCtx *requestContext) providerAttemptResult {
	active := h.registry.Active()
	reqID := middleware.GetRequestID(ctx)
	ordered := h.stickyOrder(reqCtx.request.Model, providers)
	result := providerAttemptResult{}
	for _, p := range ordered {
		if err := ctx.Err(); err != nil {
			return providerAttemptResult{err: err}
		}
		providerType := h.registry.TypeFor(p.Name())
		if active.IsExhausted(p.Name()) && providerType != "codex" {
			result = providerAttemptResult{
				err:     errors.New("sağlayıcı limiti dolu: " + p.Name()),
				limited: true,
			}
			h.logger.Debug("sağlayıcı limiti dolmuş, atlanıyor",
				slog.String("provider", p.Name()),
				slog.String("request_id", reqID),
			)
			continue
		}
		if providerType != "codex" && h.refreshUsageLimit(ctx, p) {
			result = providerAttemptResult{
				err:     errors.New("sağlayıcı usage endpoint'e göre limitte: " + p.Name()),
				limited: true,
			}
			active.ClearCurrent(reqCtx.request.Model, p.Name())
			continue
		}
		attempt := h.tryProvider(ctx, w, p, reqCtx)
		if attempt.err == nil {
			active.ClearExhausted(p.Name())
			active.SetCurrent(reqCtx.request.Model, p.Name())
			return attempt
		}
		result = attempt
		if provider.IsCanceledError(attempt.err) {
			h.logger.Warn("istemci isteği iptal etti, failover durduruluyor",
				slog.String("provider", p.Name()),
				slog.String("error", attempt.err.Error()),
				slog.String("request_id", reqID),
			)
			return attempt
		}
		if attempt.limited {
			active.MarkExhaustedUntil(p.Name(), provider.QuotaResetTime(attempt.err, time.Now()))
			active.ClearCurrent(reqCtx.request.Model, p.Name())
			if providerType != "codex" {
				return attempt
			}
			continue
		}
		h.logger.Warn("sağlayıcı hata, sonrakine geçiliyor",
			slog.String("provider", p.Name()),
			slog.String("error", attempt.err.Error()),
			slog.String("request_id", reqID),
		)
	}
	if result.err == nil {
		result.err = errors.New("denenebilir sağlayıcı bulunamadı")
	}
	return result
}

func (h *Handler) refreshUsageLimit(ctx context.Context, p provider.Provider) bool {
	usageProvider, ok := p.(provider.UsageProvider)
	if !ok {
		return false
	}
	usageCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	usage, err := usageProvider.Usage(usageCtx)
	if err != nil {
		h.logger.Debug("sağlayıcı kullanım limiti okunamadı",
			slog.String("provider", p.Name()),
			slog.String("error", err.Error()),
			slog.String("request_id", middleware.GetRequestID(ctx)),
		)
		return false
	}
	if h.registry.Active().ApplyUsageLimit(p.Name(), usage, time.Now()) {
		h.logger.Warn("sağlayıcı usage endpoint'e göre limitte, atlanıyor",
			slog.String("provider", p.Name()),
			slog.String("request_id", middleware.GetRequestID(ctx)),
		)
		return true
	}
	return false
}

func (h *Handler) tryProvider(ctx context.Context, w http.ResponseWriter, p provider.Provider, reqCtx *requestContext) providerAttemptResult {
	reqID := middleware.GetRequestID(ctx)
	attempts := 1
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return providerAttemptResult{err: err}
		}
		h.attachRequestInfo(ctx, p.Name())
		err := p.Proxy(ctx, w, reqCtx.body, reqCtx.request)
		if err == nil {
			return providerAttemptResult{}
		}
		if provider.IsQuotaError(err) {
			h.logger.Warn("sağlayıcı limiti doldu, sonrakine geçiliyor",
				slog.String("provider", p.Name()),
				slog.String("error", err.Error()),
				slog.String("request_id", reqID),
			)
			return providerAttemptResult{err: err, limited: true}
		}
		if provider.IsCanceledError(err) {
			return providerAttemptResult{err: err}
		}
		if attempt == 1 && provider.IsRetryableError(err) {
			attempts = providerRetryAttempts
		}
		if attempt < attempts {
			h.logger.Warn("sağlayıcı geçici hata verdi, yeniden deneniyor",
				slog.String("provider", p.Name()),
				slog.Int("attempt", attempt),
				slog.Int("max_attempts", attempts),
				slog.String("error", err.Error()),
				slog.String("request_id", reqID),
			)
			continue
		}
		return providerAttemptResult{err: err}
	}
	return providerAttemptResult{err: errors.New("sağlayıcı denemesi tamamlanamadı")}
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
